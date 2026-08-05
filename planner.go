package lamigrate

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"sort"
)

// MigrationPlan is an immutable ordered plan for a migration operation.
// Once built, it cannot be modified. Plans are created by the planner
// and consumed by execution or converted to a PlanView for preview.
type MigrationPlan struct {
	migrations []plannedMigration
	batch      int // allocated batch for up plans; 0 for preview/down/reset
	command    string
}

// plannedMigration is one entry in a MigrationPlan.
// The SQL bytes are exact copies retained for execution (§5.5).
type plannedMigration struct {
	name     string
	upPath   string
	downPath string
	upSQL    []byte // exact bytes, retained for execution
	downSQL  []byte // exact bytes, retained for rollback execution
	upSum    [32]byte
	downSum  [32]byte
}

// toPlanView converts an internal MigrationPlan to a read-only PlanView.
func (p *MigrationPlan) toPlanView(dir, tableName string, dryRun bool) PlanView {
	names := make([]string, len(p.migrations))
	for i, m := range p.migrations {
		names[i] = m.name
	}
	return PlanView{
		Command:    p.command,
		Directory:  dir,
		TableName:  tableName,
		Migrations: names,
		DryRun:     dryRun,
		Batch:      p.batch,
	}
}

// buildUpPlan scans the migration directory, reads metadata with global
// drift detection, validates all preflight conditions, reads selected
// pending up SQL, and returns an immutable ordered plan.
//
// Called while holding the advisory lock. Does NOT allocate a batch;
// batch allocation is a separate step during execution.
func (m *Migrator) buildUpPlan(ctx context.Context, conn *sql.Conn, caps *SessionCapabilities, limit StepLimit) (*MigrationPlan, error) {
	// 1. Scan and validate the migration directory.
	sourceFiles, err := scanMigrations(m.directory)
	if err != nil {
		return nil, err
	}
	if err := validateSourceFiles(sourceFiles, m.maxFile); err != nil {
		return nil, err
	}

	// 2. Build source file lookup map.
	sourceMap := make(map[string]*migrationFile, len(sourceFiles))
	for i := range sourceFiles {
		sourceMap[sourceFiles[i].Name] = &sourceFiles[i]
	}

	// 3. Read all applied migrations from metadata.
	applied, err := readAppliedMigrations(ctx, conn, m.tableName)
	if err != nil {
		return nil, err
	}

	// 4. Global integrity check: verify ALL applied records.
	if err := globalDriftCheck(applied, sourceMap, m.ignoreMissingSource, m.ignoreChecksumDrift); err != nil {
		return nil, err
	}

	// 5. Determine pending migrations.
	appliedSet := make(map[string]bool, len(applied))
	for _, a := range applied {
		appliedSet[a.Migration] = true
	}
	var pending []migrationFile
	for _, f := range sourceFiles {
		if !appliedSet[f.Name] {
			pending = append(pending, f)
		}
	}

	// 6. Apply limit.
	selected := applyLimit(pending, limit)
	if len(selected) == 0 {
		return &MigrationPlan{command: "up"}, nil
	}

	// 7. Check for duplicate timestamps in selected pending.
	if err := checkDuplicateTimestamps(selected); err != nil {
		return nil, err
	}

	// 8. Read selected up SQL into immutable storage and compute checksums.
	planned := make([]plannedMigration, len(selected))
	for i, f := range selected {
		upSQL, err := readFile(f.UpPath)
		if err != nil {
			return nil, fmt.Errorf("lamigrate: read up file %s: %w", f.UpPath, err)
		}
		downSQL, err := readFile(f.DownPath)
		if err != nil {
			return nil, fmt.Errorf("lamigrate: read down file %s: %w", f.DownPath, err)
		}
		planned[i] = plannedMigration{
			name:     f.Name,
			upPath:   f.UpPath,
			downPath: f.DownPath,
			upSQL:    upSQL,
			downSQL:  downSQL,
			upSum:    checksumBytes(upSQL),
			downSum:  checksumBytes(downSQL),
		}
	}

	return &MigrationPlan{
		migrations: planned,
		command:    "up",
	}, nil
}

// buildDownPlan reads metadata, selects rollback candidates from the latest
// batch, validates all preflight conditions, reads selected down SQL, and
// returns an immutable ordered plan.
//
// The DownTarget controls selection:
//   - Limit (legacy): select from latest batch, apply step limit.
//   - Name: select ONLY the named migration (from any batch, not everything
//     newer). Single-migration rollback regardless of batch position.
//   - Batch: select all migrations in the given batch (must == latestBatch).
func (m *Migrator) buildDownPlan(ctx context.Context, conn *sql.Conn, caps *SessionCapabilities, target DownTarget) (*MigrationPlan, error) {
	// 1. Scan and validate the migration directory.
	sourceFiles, err := scanMigrations(m.directory)
	if err != nil {
		return nil, err
	}
	if err := validateSourceFiles(sourceFiles, m.maxFile); err != nil {
		return nil, err
	}

	sourceMap := make(map[string]*migrationFile, len(sourceFiles))
	for i := range sourceFiles {
		sourceMap[sourceFiles[i].Name] = &sourceFiles[i]
	}

	// 2. Read all applied migrations.
	applied, err := readAppliedMigrations(ctx, conn, m.tableName)
	if err != nil {
		return nil, err
	}

	// 3. Global drift check on ALL applied records.
	if err := globalDriftCheck(applied, sourceMap, m.ignoreMissingSource, m.ignoreChecksumDrift); err != nil {
		return nil, err
	}

	// 4. Select rollback candidates from the latest non-baseline batch.
	var latestBatch uint64
	for _, a := range applied {
		if !a.IsBaseline && a.Batch > latestBatch {
			latestBatch = a.Batch
		}
	}

	var candidates []AppliedMigration
	// Collect in reverse execution order (descending ID).
	for i := len(applied) - 1; i >= 0; i-- {
		// For by-name targeting, collect from ANY batch (single-migration rollback).
		// For by-batch/limit, only the latest batch is eligible.
		if target.kind() == "name" {
			if !applied[i].IsBaseline {
				candidates = append(candidates, applied[i])
			}
		} else if applied[i].Batch == latestBatch && !applied[i].IsBaseline {
			candidates = append(candidates, applied[i])
		}
	}

	// 5. Apply target-based selection.
	selected := selectDownCandidates(candidates, target, latestBatch)
	if len(selected) == 0 {
		if target.kind() == "name" {
			return nil, fmt.Errorf(
				"%w: migration %s is not applied or is a baseline",
				ErrMigrationNotFoundInLatestBatch, target.Name,
			)
		}
		// Check for batch-not-latest.
		if target.kind() == "batch" {
			return nil, fmt.Errorf(
				"%w: batch %d is not the latest batch (%d)",
				ErrBatchNotLatest, target.Batch, latestBatch,
			)
		}
		return &MigrationPlan{command: "down"}, nil
	}

	// 6. Read selected down SQL.
	planned := make([]plannedMigration, len(selected))
	for i, a := range selected {
		src, ok := sourceMap[a.Migration]
		if !ok {
			return nil, fmt.Errorf(
				"%w: source file not found for applied migration %s",
				ErrChecksumDrift, a.Migration,
			)
		}
		downSQL, err := readFile(src.DownPath)
		if err != nil {
			return nil, fmt.Errorf("lamigrate: read down file %s: %w", src.DownPath, err)
		}
		upSQL, err := readFile(src.UpPath)
		if err != nil {
			return nil, fmt.Errorf("lamigrate: read up file %s: %w", src.UpPath, err)
		}
		planned[i] = plannedMigration{
			name:     a.Migration,
			upPath:   src.UpPath,
			downPath: src.DownPath,
			upSQL:    upSQL,
			downSQL:  downSQL,
			upSum:    checksumBytes(upSQL),
			downSum:  checksumBytes(downSQL),
		}
	}

	return &MigrationPlan{
		migrations: planned,
		command:    "down",
	}, nil
}

// buildResetPlan reads metadata, selects ALL non-baseline applied
// migrations in reverse execution order, validates preflight conditions,
// reads down SQL, and returns an immutable ordered plan.
func (m *Migrator) buildResetPlan(ctx context.Context, conn *sql.Conn, caps *SessionCapabilities) (*MigrationPlan, error) {
	// 1. Scan and validate the migration directory.
	sourceFiles, err := scanMigrations(m.directory)
	if err != nil {
		return nil, err
	}
	if err := validateSourceFiles(sourceFiles, m.maxFile); err != nil {
		return nil, err
	}

	sourceMap := make(map[string]*migrationFile, len(sourceFiles))
	for i := range sourceFiles {
		sourceMap[sourceFiles[i].Name] = &sourceFiles[i]
	}

	// 2. Read all applied migrations.
	applied, err := readAppliedMigrations(ctx, conn, m.tableName)
	if err != nil {
		return nil, err
	}

	// 3. Global drift check on ALL applied records.
	if err := globalDriftCheck(applied, sourceMap, m.ignoreMissingSource, m.ignoreChecksumDrift); err != nil {
		return nil, err
	}

	// 4. Select ALL non-baseline applied migrations in reverse execution order.
	var rollback []AppliedMigration
	for i := len(applied) - 1; i >= 0; i-- {
		if !applied[i].IsBaseline {
			rollback = append(rollback, applied[i])
		}
	}

	if len(rollback) == 0 {
		return &MigrationPlan{command: "reset"}, nil
	}

	// 5. Read down SQL for each.
	planned := make([]plannedMigration, len(rollback))
	for i, a := range rollback {
		src, ok := sourceMap[a.Migration]
		if !ok {
			return nil, fmt.Errorf(
				"%w: source file not found for applied migration %s",
				ErrChecksumDrift, a.Migration,
			)
		}
		downSQL, err := readFile(src.DownPath)
		if err != nil {
			return nil, fmt.Errorf("lamigrate: read down file %s: %w", src.DownPath, err)
		}
		upSQL, err := readFile(src.UpPath)
		if err != nil {
			return nil, fmt.Errorf("lamigrate: read up file %s: %w", src.UpPath, err)
		}
		planned[i] = plannedMigration{
			name:     a.Migration,
			upPath:   src.UpPath,
			downPath: src.DownPath,
			upSQL:    upSQL,
			downSQL:  downSQL,
			upSum:    checksumBytes(upSQL),
			downSum:  checksumBytes(downSQL),
		}
	}

	return &MigrationPlan{
		migrations: planned,
		command:    "reset",
	}, nil
}

// globalDriftCheck verifies that every applied metadata row matches
// its source file checksums. It also detects orphaned records (applied
// but source file missing) and dirty states.
//
// When ignoreMissingSource is true, applied rows whose source file is
// absent are SKIPPED (not treated as errors) — this supports shared-DB
// workflows where feature-branch migrations are applied to a common
// database and later removed from the trunk. All other checks remain
// enforced: dirty states still block, and any source that IS present with
// a different checksum is still a drift error.
//
// This is the global integrity check: ALL applied records are verified,
// not just selected ones (architecture §11.1 step 4).
func globalDriftCheck(applied []AppliedMigration, sourceMap map[string]*migrationFile, ignoreMissingSource, ignoreChecksumDrift bool) error {
	for _, a := range applied {
		// Reject dirty rows.
		if isDirtyState(a.State) {
			return fmt.Errorf(
				"%w: migration %s is in dirty state %q",
				ErrDirtyState, a.Migration, a.State,
			)
		}

		// Skip baselines for source-file checks (they live in legacyDir).
		if a.IsBaseline {
			continue
		}

		src, ok := sourceMap[a.Migration]
		if !ok {
			// Source file missing.
			if ignoreMissingSource {
				// Intentional in a shared-DB workflow: skip this orphaned
				// record and continue checking the rest.
				continue
			}
			return fmt.Errorf(
				"%w: source file not found for applied migration %s",
				ErrChecksumDrift, a.Migration,
			)
		}

		// Verify up checksum.
		if len(a.UpChecksum) == 32 {
			var storedSum [32]byte
			copy(storedSum[:], a.UpChecksum)
			if storedSum != src.UpChecksum {
				if ignoreChecksumDrift {
					fmt.Fprintf(os.Stderr, "warning: up checksum drift for %s: stored=%s source=%s (ignoring)\n", a.Migration, checksumHex(storedSum), checksumHex(src.UpChecksum))
				} else {
					return fmt.Errorf(
						"%w: up checksum mismatch for %s: stored=%s source=%s",
						ErrChecksumDrift, a.Migration,
						checksumHex(storedSum), checksumHex(src.UpChecksum),
					)
				}
			}
		}

		// Verify down checksum (may be NULL for irreversible).
		if len(a.DownChecksum) == 32 {
			var storedDown [32]byte
			copy(storedDown[:], a.DownChecksum)
			if storedDown != src.DownChecksum {
				if ignoreChecksumDrift {
					fmt.Fprintf(os.Stderr, "warning: down checksum drift for %s: stored=%s source=%s (ignoring)\n", a.Migration, checksumHex(storedDown), checksumHex(src.DownChecksum))
				} else {
					return fmt.Errorf(
						"%w: down checksum mismatch for %s: stored=%s source=%s",
						ErrChecksumDrift, a.Migration,
						checksumHex(storedDown), checksumHex(src.DownChecksum),
					)
				}
			}
		}
	}
	return nil
}

// isDirtyState reports whether the given state indicates an incomplete
// or failed operation that blocks all write operations (§9.1).
func isDirtyState(state string) bool {
	switch state {
	case "applying", "apply_failed", "rolling_back", "rollback_failed":
		return true
	default:
		return false
	}
}

// applyLimit slices the pending list according to the given StepLimit.
func applyLimit(pending []migrationFile, limit StepLimit) []migrationFile {
	if limit.isAll() || limit.count() < 0 {
		return pending
	}
	n := limit.count()
	if n > len(pending) {
		n = len(pending)
	}
	return pending[:n]
}

// applyDownLimit slices the candidates list according to the given StepLimit.
func applyDownLimit(candidates []AppliedMigration, limit StepLimit) []AppliedMigration {
	if limit.isAll() || limit.count() < 0 {
		return candidates
	}
	n := limit.count()
	if n > len(candidates) {
		n = len(candidates)
	}
	return candidates[:n]
}

// selectDownCandidates applies a DownTarget to a list of candidates (already in
// reverse execution order: newest first) from the latest batch.
func selectDownCandidates(candidates []AppliedMigration, target DownTarget, latestBatch uint64) []AppliedMigration {
	switch target.kind() {
	case "name":
		// Single-migration rollback: find the named migration in ANY batch.
		// Only that one migration is rolled back, regardless of its batch.
		for _, c := range candidates {
			if c.Migration == target.Name {
				return []AppliedMigration{c}
			}
		}
		return nil
	case "batch":
		// Validate that the requested batch is the latest.
		if uint64(target.Batch) != latestBatch {
			return nil
		}
		return candidates
	default: // "limit"
		return applyDownLimit(candidates, target.Limit)
	}
}

// checkDuplicateTimestamps rejects duplicate timestamps in the selected
// migration files (architecture §8.1).
func checkDuplicateTimestamps(files []migrationFile) error {
	seen := make(map[int64]bool, len(files))
	for _, f := range files {
		if seen[f.Timestamp] {
			return fmt.Errorf(
				"lamigrate: duplicate timestamp %d in migration %s",
				f.Timestamp, f.Name,
			)
		}
		seen[f.Timestamp] = true
	}
	return nil
}

// sortMigrationsByName sorts applied migrations by name for stable output.
func sortMigrationsByName(migrations []AppliedMigration) {
	sort.Slice(migrations, func(i, j int) bool {
		return migrations[i].Migration < migrations[j].Migration
	})
}