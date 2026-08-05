package lamigrate

import (
	"context"
	"database/sql"
	"fmt"
)

// buildRefreshPlan builds a refresh plan: a down plan followed by an up plan.
// Both plans are built within the same lock session for consistency.
func (m *Migrator) buildRefreshPlan(
	ctx context.Context,
	conn *sql.Conn,
	caps *SessionCapabilities,
	target RefreshTarget,
) (*RefreshPlan, error) {
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

	// 3. Global drift check.
	if err := globalDriftCheck(applied, sourceMap, m.ignoreMissingSource); err != nil {
		return nil, err
	}

	// 4. Build the down plan (all non-baseline, reverse order).
	var rollback []AppliedMigration
	for i := len(applied) - 1; i >= 0; i-- {
		if !applied[i].IsBaseline {
			rollback = append(rollback, applied[i])
		}
	}

	if len(rollback) == 0 {
		return nil, fmt.Errorf("%w: no applied migrations to refresh", ErrRefreshNothingToRollback)
	}

	// 5. Apply step limit for refresh --step N (counts globally).
	if target.kind() == "limit" && !target.Limit.isAll() && target.Limit.count() > 0 {
		n := target.Limit.count()
		if n > len(rollback) {
			n = len(rollback)
		}
		rollback = rollback[:n]
	}

	// 6. Read down SQL for the rollback set.
	rbPlanned := make([]plannedMigration, len(rollback))
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
		rbPlanned[i] = plannedMigration{
			name:     a.Migration,
			upPath:   src.UpPath,
			downPath: src.DownPath,
			upSQL:    upSQL,
			downSQL:  downSQL,
			upSum:    checksumBytes(upSQL),
			downSum:  checksumBytes(downSQL),
		}
	}

	downPlan := &MigrationPlan{
		migrations: rbPlanned,
		command:    "down",
	}

	// 7. Build the up plan (re-apply after rollback).
	// Determine which pending migrations to apply.
	appliedSet := make(map[string]bool)
	for _, a := range applied {
		appliedSet[a.Migration] = true
	}

	// After rollback, the rollback set becomes pending.
	// Build the full pending list: original pending + rollback set (in timestamp order).
	var pending []migrationFile
	for _, f := range sourceFiles {
		if !appliedSet[f.Name] {
			pending = append(pending, f)
		}
	}

	// For by-name refresh: only apply up to and including the named migration.
	if target.kind() == "name" {
		cutoff := -1
		for i, f := range pending {
			if f.Name == target.Name {
				cutoff = i
				break
			}
		}
		if cutoff < 0 {
			return nil, fmt.Errorf(
				"%w: migration %s not found in pending set after rollback",
				ErrMigrationNotFoundInLatestBatch, target.Name,
			)
		}
		pending = pending[:cutoff+1]
	}

	// For by-step refresh: clamp to the same N that was rolled back.
	if target.kind() == "limit" && !target.Limit.isAll() && target.Limit.count() > 0 {
		n := target.Limit.count()
		if n > len(pending) {
			n = len(pending)
		}
		pending = pending[:n]
	}

	if len(pending) == 0 {
		// Rollback succeeded but nothing to re-apply.
		return &RefreshPlan{
			downPlan: downPlan,
			upPlan:   &MigrationPlan{command: "up"},
			command:  "refresh",
		}, nil
	}

	// Check for duplicate timestamps.
	if err := checkDuplicateTimestamps(pending); err != nil {
		return nil, err
	}

	// Read up SQL for the forward set.
	apPlanned := make([]plannedMigration, len(pending))
	for i, f := range pending {
		upSQL, err := readFile(f.UpPath)
		if err != nil {
			return nil, fmt.Errorf("lamigrate: read up file %s: %w", f.UpPath, err)
		}
		downSQL, err := readFile(f.DownPath)
		if err != nil {
			return nil, fmt.Errorf("lamigrate: read down file %s: %w", f.DownPath, err)
		}
		apPlanned[i] = plannedMigration{
			name:     f.Name,
			upPath:   f.UpPath,
			downPath: f.DownPath,
			upSQL:    upSQL,
			downSQL:  downSQL,
			upSum:    checksumBytes(upSQL),
			downSum:  checksumBytes(downSQL),
		}
	}

	upPlan := &MigrationPlan{
		migrations: apPlanned,
		command:    "up",
	}

	return &RefreshPlan{
		downPlan: downPlan,
		upPlan:   upPlan,
		command:  "refresh",
	}, nil
}

// RefreshPlan is an internal plan for a refresh operation (down + up).
type RefreshPlan struct {
	downPlan *MigrationPlan
	upPlan   *MigrationPlan
	command  string
}

// toRefreshPlanView converts internal refresh plans to a read-only view.
func (rp *RefreshPlan) toRefreshPlanView(dir, tableName string, dryRun bool) RefreshPlanView {
	return toRefreshPlanView(rp.downPlan, rp.upPlan, dir, tableName, dryRun)
}
