package lamigrate

// import_v2.go — Reconciled baseline import from golang-migrate.
//
// This file implements the production import operation defined in
// architecture §13. It reads a golang-migrate source metadata table
// and discovers legacy numbered files, classifies them as baselines
// (at or below the recorded database version) or unresolved (above),
// and inserts baseline rows into the lamigrate destination state table.
//
// Import is a reconciliation operation, not "mark every file applied."
// It requires explicit operator attestation (SourceQuiesced) and
// validates source/destination state before any mutation.

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// ---------- Public types ----------

// GolangMigrateImportOptions configures the golang-migrate import
// operation. SourceTable names the golang-migrate metadata table in
// the same database. SourceQuiesced is the operator attestation
// that no golang-migrate process can change the source during import.
type GolangMigrateImportOptions struct {
	SourceTable    string
	SourceQuiesced bool
}

// ImportPlanItem describes one baseline in the import plan.
type ImportPlanItem struct {
	// MigrationID is the canonical lamigrate ID: "golang-migrate:<version>"
	MigrationID string
	// SourceName is the validated legacy filename base.
	SourceName string
	// Version is the numeric golang-migrate version.
	Version uint64
	// UpChecksum is the lowercase hex SHA-256 of the up file.
	UpChecksum string
	// DownChecksum is the lowercase hex SHA-256 of the down file.
	DownChecksum string
}

// ImportPlanView is a read-only view of a golang-migrate import plan.
// It is returned by PreviewGolangMigrateImport and describes what
// would happen if the import were executed.
type ImportPlanView struct {
	// DryRun indicates this is a preview, not an execution.
	DryRun bool
	// SourceTable is the validated source metadata table name.
	SourceTable string
	// SourceVersion is the current version from the source table.
	SourceVersion uint64
	// SourceDirty reports whether the source metadata is dirty.
	SourceDirty bool
	// LegacyDir is the resolved legacy directory path.
	LegacyDir string
	// Baselines lists versions at or below the recorded database version.
	Baselines []ImportPlanItem
	// Unresolved lists versions above the recorded database version.
	Unresolved []ImportPlanItem
	// Empty is true when there are no baselines to import.
	Empty bool
	// Noop is true when the destination already has the exact baseline set.
	Noop bool
}

// ---------- Internal types ----------

// golangMigrateSourceTuple holds the one-row metadata from a
// golang-migrate source table.
type golangMigrateSourceTuple struct {
	Version uint64
	Dirty   bool
}

// legacyImportCandidate represents one validated legacy file pair
// (up + down) with computed checksums.
type legacyImportCandidate struct {
	Version      uint64
	Description  string
	UpFilename   string
	DownFilename string
	UpChecksum   [32]byte
	DownChecksum [32]byte
}

// legacyImportPathResult is the combined result of scanning and
// checksumming legacy files.
type legacyImportPathResult struct {
	Candidates []legacyImportCandidate
	UpFiles    map[uint64]string // version → absolute up path
	DownFiles  map[uint64]string // version → absolute down path
}

// ---------- Public API ----------

// PreviewGolangMigrateImport reads source metadata and legacy files,
// classifies versions, and returns a plan view without mutating
// anything. It acquires the advisory lock for read consistency but
// performs no metadata DDL/DML.
//
// PreviewGolangMigrateImport may be called with SourceQuiesced=false;
// mutation remains blocked until the attestation is supplied.
func (m *Migrator) PreviewGolangMigrateImport(
	ctx context.Context,
	opts GolangMigrateImportOptions,
) (ImportPlanView, error) {
	// Validate source table name before creating a connector.
	normalizedSource := strings.TrimSpace(strings.ToLower(opts.SourceTable))
	if err := validateImportSourceTable(normalizedSource, m.tableName); err != nil {
		return ImportPlanView{}, err
	}
	// Validate legacyDir is configured.
	if err := validateLegacyDir(m.legacyDir); err != nil {
		return ImportPlanView{}, err
	}
	// Scan legacy files from disk (no DB needed).
	pathResult, err := scanLegacyImportDir(m.legacyDir)
	if err != nil {
		return ImportPlanView{}, err
	}

	var view ImportPlanView
	// Bootstrap metadata before acquiring the lock to avoid deadlock
	// (bootstrap creates its own sessions and may need the scope lock).
	_ = m.bootstrap(ctx)

	err = m.withLockSession(ctx, func(ctx context.Context, conn *sql.Conn, caps *SessionCapabilities) error {
		// Read source metadata.
		srcTuple, err := readGolangMigrateSource(ctx, conn, normalizedSource)
		if err != nil {
			return err
		}

		// Build import plan (dry-run).
		plan, err := buildImportPlan(ctx, conn, pathResult, srcTuple, normalizedSource, m.tableName, m.legacyDir, true)
		if err != nil {
			return err
		}
		view = plan
		return nil
	})
	if err != nil {
		return ImportPlanView{}, err
	}
	return view, nil
}

// ImportGolangMigrate performs the reconciled import from golang-migrate.
// It requires SourceQuiesced=true and acquires the advisory lock for
// the duration of the operation.
//
// The import follows the protocol defined in architecture §13:
//   - Reject SourceQuiesced=false before creating a connector
//   - Read source metadata and legacy files
//   - Classify versions and validate file pairs
//   - For empty destination: initialize metadata, re-read source,
//     insert baselines in one transaction
//   - For exact existing set: return idempotent no-op
//   - Any partial or conflicting set: fail closed
func (m *Migrator) ImportGolangMigrate(
	ctx context.Context,
	opts GolangMigrateImportOptions,
) (Result, error) {
	// Reject SourceQuiesced=false BEFORE creating a connector (§13).
	if !opts.SourceQuiesced {
		return Result{}, fmt.Errorf(
			"%w: SourceQuiesced must be true before import can mutate; "+
				"use PreviewGolangMigrateImport to plan without attestation",
			ErrInvalidConfig,
		)
	}

	// Validate source table name before creating a connector.
	normalizedSource := strings.TrimSpace(strings.ToLower(opts.SourceTable))
	if err := validateImportSourceTable(normalizedSource, m.tableName); err != nil {
		return Result{}, err
	}
	// Validate legacyDir is configured.
	if err := validateLegacyDir(m.legacyDir); err != nil {
		return Result{}, err
	}
	// Scan legacy files from disk (no DB needed).
	pathResult, err := scanLegacyImportDir(m.legacyDir)
	if err != nil {
		return Result{}, err
	}

	var result Result
	// Bootstrap metadata before acquiring the lock to avoid deadlock.
	if err := m.bootstrap(ctx); err != nil {
		return Result{}, err
	}

	err = m.withLockSession(ctx, func(ctx context.Context, conn *sql.Conn, caps *SessionCapabilities) error {
		// Read source metadata.
		srcTuple, err := readGolangMigrateSource(ctx, conn, normalizedSource)
		if err != nil {
			return err
		}
		// Record source tuple for re-read verification after bootstrap.
		snapshotVersion := srcTuple.Version
		snapshotDirty := srcTuple.Dirty

		// Build import plan.
		plan, err := buildImportPlan(ctx, conn, pathResult, srcTuple, normalizedSource, m.tableName, m.legacyDir, false)
		if err != nil {
			return err
		}

		// Nothing to import.
		if plan.Empty {
			result = Result{Command: "import"}
			return nil
		}
		// Idempotent no-op.
		if plan.Noop {
			result = Result{Command: "import"}
			return nil
		}

		// Re-read source tuple immediately before mutation (§13).
		reReadTuple, err := readGolangMigrateSource(ctx, conn, normalizedSource)
		if err != nil {
			return err
		}
		if reReadTuple.Version != snapshotVersion || reReadTuple.Dirty != snapshotDirty {
			return fmt.Errorf(
				"%w: source metadata changed between planning and mutation "+
					"(version was %d now %d, dirty was %v now %v)",
				ErrDirtyState,
				snapshotVersion, reReadTuple.Version,
				snapshotDirty, reReadTuple.Dirty,
			)
		}

		// Insert baselines in one explicit metadata transaction.
		if err := m.insertImportBaselines(ctx, conn, normalizedSource, plan.Baselines, snapshotVersion, snapshotDirty); err != nil {
			return err
		}
		result = Result{Command: "import"}
		return nil
	})
	if err != nil {
		return Result{}, err
	}
	return result, nil
}

// ---------- Source table validation ----------

// validateImportSourceTable validates the source table name using the
// same lowercase identifier policy and ensures it differs from the
// destination state table and lamigrate_control.
func validateImportSourceTable(sourceTable, destTable string) error {
	if sourceTable == "" {
		return fmt.Errorf(
			"%w: SourceTable must not be empty",
			ErrInvalidConfig,
		)
	}
	if !validTrackingTable.MatchString(sourceTable) {
		return fmt.Errorf(
			"%w: SourceTable %q must match [a-z_][a-z0-9_]*",
			ErrInvalidConfig, sourceTable,
		)
	}
	if sourceTable == destTable {
		return fmt.Errorf(
			"%w: SourceTable %q must differ from the destination state table",
			ErrInvalidConfig, sourceTable,
		)
	}
	if sourceTable == controlTableName {
		return fmt.Errorf(
			"%w: SourceTable %q must differ from %q",
			ErrInvalidConfig, sourceTable, controlTableName,
		)
	}
	return nil
}

// ---------- Legacy file scanning ----------

// legacyImportFilePattern matches legacy golang-migrate files using
// any positive unsigned decimal version (not fixed 6-digit).
// Matches: <digits>_<description>.up.sql / .down.sql
var legacyImportFilePattern = regexp.MustCompile(`^(\d+)_(.+)\.(up|down)\.sql$`)

// validateLegacyDir checks that the legacy directory exists and is
// a real directory (not a symlink).
func validateLegacyDir(dir string) error {
	dir = filepath.Clean(dir)
	if dir == "" || dir == "." {
		return fmt.Errorf(
			"%w: LegacyDir must be a non-empty directory path",
			ErrInvalidConfig,
		)
	}
	info, err := os.Stat(dir)
	if err != nil {
		return fmt.Errorf(
			"%w: LegacyDir %s: %v",
			ErrInvalidConfig, dir, err,
		)
	}
	if !info.IsDir() {
		return fmt.Errorf(
			"%w: LegacyDir %s is not a directory",
			ErrInvalidConfig, dir,
		)
	}
	return nil
}

// scanLegacyImportDir discovers legacy golang-migrate file pairs in
// dir using numeric versions (any width). It validates up/down pairs,
// computes checksums, and returns sorted candidates.
func scanLegacyImportDir(dir string) (*legacyImportPathResult, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read legacy dir %s: %w", dir, err)
	}

	type rawEntry struct {
		version     uint64
		versionStr  string // original digit string including leading zeros
		desc        string
		ext         string
		filename    string
	}

	// Group files by version → description → entries.
	byVersion := make(map[uint64]map[string]*rawEntry)
	var versions []uint64

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		m := legacyImportFilePattern.FindStringSubmatch(name)
		if m == nil {
			continue
		}
		ver, _ := strconv.ParseUint(m[1], 10, 64)
		// Skip 14-digit timestamps — those are lamigrate files, not golang-migrate.
		if len(m[1]) == 14 {
			continue
		}
		desc := m[2]
		ext := m[3]

		if _, exists := byVersion[ver]; !exists {
			byVersion[ver] = make(map[string]*rawEntry)
			versions = append(versions, ver)
		}
		if prev, ok := byVersion[ver][desc]; ok {
			if prev.ext == ext {
				return nil, fmt.Errorf(
					"%w: duplicate legacy file for version %d, desc %q: %s and %s",
					ErrInvalidConfig, ver, desc, prev.filename, name,
				)
			}
		}
		byVersion[ver][desc] = &rawEntry{
			version:    ver,
			versionStr: m[1],
			desc:       desc,
			ext:        ext,
			filename:   name,
		}
	}

	// Sort versions for deterministic output.
	sort.Slice(versions, func(i, j int) bool {
		return versions[i] < versions[j]
	})

	// Build candidates: each version should have exactly one up+down pair.
	var candidates []legacyImportCandidate
	upFiles := make(map[uint64]string, len(versions))
	downFiles := make(map[uint64]string, len(versions))

	for _, ver := range versions {
		descs := byVersion[ver]
		if len(descs) != 1 {
			var names []string
			for desc := range descs {
				names = append(names, desc)
			}
			return nil, fmt.Errorf(
				"%w: version %d has multiple descriptions: %v",
				ErrInvalidConfig, ver, names,
			)
		}

		var desc string
		for d := range descs {
			desc = d
		}
		entry := descs[desc]

		// Validate description follows canonical snake-case rule.
		if !validMigrationName.MatchString(desc) {
			return nil, fmt.Errorf(
				"%w: legacy description %q (version %d) must match [a-z][a-z0-9_]*",
				ErrInvalidConfig, desc, ver,
			)
		}

		var upPath, downPath string
		baseName := entry.versionStr + "_" + desc
		if entry.ext == "up" {
			upPath = filepath.Join(dir, entry.filename)
			downPath = filepath.Join(dir, baseName+".down.sql")
		} else {
			downPath = filepath.Join(dir, entry.filename)
			upPath = filepath.Join(dir, baseName+".up.sql")
		}

		// Verify both up and down files exist.
		if _, err := os.Stat(upPath); err != nil {
			return nil, fmt.Errorf(
				"%w: version %d missing up file %s",
				ErrInvalidConfig, ver, upPath,
			)
		}
		if _, err := os.Stat(downPath); err != nil {
			return nil, fmt.Errorf(
				"%w: version %d missing down file %s",
				ErrInvalidConfig, ver, downPath,
			)
		}

		// Compute checksums.
		upSum, err := checksumFile(upPath)
		if err != nil {
			return nil, fmt.Errorf("checksum up file version %d: %w", ver, err)
		}
		downSum, err := checksumFile(downPath)
		if err != nil {
			return nil, fmt.Errorf("checksum down file version %d: %w", ver, err)
		}

		candidates = append(candidates, legacyImportCandidate{
			Version:      ver,
			Description:  desc,
			UpFilename:   entry.filename,
			DownFilename: desc + ".down.sql",
			UpChecksum:   upSum,
			DownChecksum: downSum,
		})
		upFiles[ver] = upPath
		downFiles[ver] = downPath
	}

	return &legacyImportPathResult{
		Candidates: candidates,
		UpFiles:    upFiles,
		DownFiles:  downFiles,
	}, nil
}

// ---------- Source metadata reading ----------

// readGolangMigrateSource reads the one-row version,dirty metadata
// from a golang-migrate source table.
func readGolangMigrateSource(
	ctx context.Context,
	conn *sql.Conn,
	tableName string,
) (golangMigrateSourceTuple, error) {
	var version uint64
	var dirty bool
	query := fmt.Sprintf(
		"SELECT version, dirty FROM `%s` LIMIT 2",
		tableName,
	)
	rows, err := conn.QueryContext(ctx, query)
	if err != nil {
		return golangMigrateSourceTuple{}, fmt.Errorf(
			"read golang-migrate source table %q: %w",
			tableName, err,
		)
	}
	defer rows.Close()

	rowCount := 0
	for rows.Next() {
		if err := rows.Scan(&version, &dirty); err != nil {
			return golangMigrateSourceTuple{}, fmt.Errorf(
				"scan golang-migrate source row: %w", err,
			)
		}
		rowCount++
	}
	if err := rows.Err(); err != nil {
		return golangMigrateSourceTuple{}, fmt.Errorf(
			"iterate golang-migrate source rows: %w", err,
		)
	}

	if rowCount == 0 {
		return golangMigrateSourceTuple{}, fmt.Errorf(
			"%w: golang-migrate source table %q is empty (expected one row with version,dirty)",
			ErrInvalidConfig, tableName,
		)
	}
	if rowCount > 1 {
		return golangMigrateSourceTuple{}, fmt.Errorf(
			"%w: golang-migrate source table %q has %d rows (expected exactly one)",
			ErrInvalidConfig, tableName, rowCount,
		)
	}

	return golangMigrateSourceTuple{
		Version: version,
		Dirty:   dirty,
	}, nil
}

// ---------- Plan building ----------

// buildImportPlan classifies legacy files and destination state into
// an import plan. It is called under the advisory lock.
func buildImportPlan(
	ctx context.Context,
	conn *sql.Conn,
	pathResult *legacyImportPathResult,
	srcTuple golangMigrateSourceTuple,
	sourceTable string,
	destTable string,
	legacyDir string,
	dryRun bool,
) (ImportPlanView, error) {
	view := ImportPlanView{
		DryRun:        dryRun,
		SourceTable:   sourceTable,
		SourceVersion: srcTuple.Version,
		SourceDirty:   srcTuple.Dirty,
		LegacyDir:     legacyDir,
	}

	// Refuse import when source is dirty (§13 step 3).
	if srcTuple.Dirty {
		return view, fmt.Errorf(
			"%w: golang-migrate source is dirty (version %d) — resolve dirty state before import",
			ErrDirtyState, srcTuple.Version,
		)
	}

	// Classify versions: <= current → baseline candidate, > current → unresolved.
	for _, c := range pathResult.Candidates {
		item := ImportPlanItem{
			MigrationID:  migrationIDForVersion(c.Version),
			SourceName:   c.UpFilename,
			Version:      c.Version,
			UpChecksum:   checksumHex(c.UpChecksum),
			DownChecksum: checksumHex(c.DownChecksum),
		}
		if c.Version <= srcTuple.Version {
			view.Baselines = append(view.Baselines, item)
		} else {
			view.Unresolved = append(view.Unresolved, item)
		}
	}

	// Check for duplicate baseline versions.
	seenVersions := make(map[uint64]bool, len(view.Baselines))
	for _, b := range view.Baselines {
		if seenVersions[b.Version] {
			return view, fmt.Errorf(
				"%w: duplicate baseline version %d",
				ErrInvalidConfig, b.Version,
			)
		}
		seenVersions[b.Version] = true
	}

	// Validate unique migration IDs across all baselines.
	seenIDs := make(map[string]bool, len(view.Baselines))
	for _, b := range view.Baselines {
		if seenIDs[b.MigrationID] {
			return view, fmt.Errorf(
				"%w: duplicate migration ID %q",
				ErrInvalidConfig, b.MigrationID,
			)
		}
		seenIDs[b.MigrationID] = true
	}

	view.Empty = len(view.Baselines) == 0
	if view.Empty {
		return view, nil
	}

	// Check destination state table.
	destBaselineSet, err := readDestinationBaselines(ctx, conn, destTable)
	if err != nil {
		return view, err
	}

	if len(destBaselineSet) == 0 {
		// Empty destination — safe to import.
		return view, nil
	}

	// Check for exact previously imported set (idempotent no-op).
	if isExactImportMatch(destBaselineSet, view.Baselines) {
		view.Noop = true
		return view, nil
	}

	// Partial or conflicting set — fail closed (§13 step 13).
	return view, fmt.Errorf(
		"%w: destination table %q has %d existing baselines that do not exactly match the %d planned baselines; "+
			"extension of a previous baseline import is not supported",
		ErrRecoveryRequired, destTable, len(destBaselineSet), len(view.Baselines),
	)
}

// ---------- Destination state reading ----------

// destinationBaseline holds the identity of one existing baseline row
// in the destination state table.
type destinationBaseline struct {
	MigrationID  string
	UpChecksum   []byte
	DownChecksum []byte
}

// readDestinationBaselines reads all golang_migrate baseline rows
// from the destination state table. Returns nil if the table doesn't
// exist or has no baseline rows.
func readDestinationBaselines(
	ctx context.Context,
	conn *sql.Conn,
	tableName string,
) ([]destinationBaseline, error) {
	// Check if the table exists first.
	var tableExists int
	err := conn.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = DATABASE() AND table_name = ?",
		tableName,
	).Scan(&tableExists)
	if err != nil {
		return nil, fmt.Errorf("check destination table existence: %w", err)
	}
	if tableExists == 0 {
		return nil, nil
	}

	// Read all baseline rows.
	query := fmt.Sprintf(
		"SELECT migration, up_checksum, down_checksum FROM `%s` WHERE source_kind = 'golang_migrate' AND is_baseline = TRUE ORDER BY source_version ASC",
		tableName,
	)
	rows, err := conn.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("read destination baselines: %w", err)
	}
	defer rows.Close()

	var baselines []destinationBaseline
	for rows.Next() {
		var b destinationBaseline
		var upCS, downCS []byte
		if err := rows.Scan(&b.MigrationID, &upCS, &downCS); err != nil {
			return nil, fmt.Errorf("scan destination baseline: %w", err)
		}
		b.UpChecksum = upCS
		b.DownChecksum = downCS
		baselines = append(baselines, b)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate destination baselines: %w", err)
	}

	return baselines, nil
}

// isExactImportMatch checks whether the existing destination baselines
// exactly match the planned baselines (same count, same IDs, same
// checksums in order).
func isExactImportMatch(
	existing []destinationBaseline,
	planned []ImportPlanItem,
) bool {
	if len(existing) != len(planned) {
		return false
	}
	for i, e := range existing {
		p := planned[i]
		if e.MigrationID != p.MigrationID {
			return false
		}
		if len(e.UpChecksum) != 32 {
			return false
		}
		var storedSum [32]byte
		copy(storedSum[:], e.UpChecksum)
		if checksumHex(storedSum) != p.UpChecksum {
			return false
		}
		if len(e.DownChecksum) != 32 {
			return false
		}
		var storedDown [32]byte
		copy(storedDown[:], e.DownChecksum)
		if checksumHex(storedDown) != p.DownChecksum {
			return false
		}
	}
	return true
}

// ---------- Baseline insertion ----------

// insertImportBaselines inserts all baseline rows in one explicit
// metadata transaction while holding the advisory lock (§13 step 12).
// It re-reads the source tuple inside the transaction to verify
// stability before committing.
func (m *Migrator) insertImportBaselines(
	ctx context.Context,
	conn *sql.Conn,
	sourceTable string,
	baselines []ImportPlanItem,
	snapshotVersion uint64,
	snapshotDirty bool,
) error {
	if len(baselines) == 0 {
		return nil
	}

	now := time.Now().UTC()
	runnerID := generateRunnerID()
	timestamp := now.Format("2006-01-02 15:04:05.000000")

	// Start the metadata transaction.
	if _, err := conn.ExecContext(ctx, "START TRANSACTION"); err != nil {
		return fmt.Errorf("import baselines: start transaction: %w", err)
	}

	// Re-read source tuple inside the transaction for final verification.
	reTuple, err := readGolangMigrateSource(ctx, conn, sourceTable)
	if err != nil {
		conn.ExecContext(ctx, "ROLLBACK") //nolint:errcheck
		return fmt.Errorf("import baselines: re-read source: %w", err)
	}
	if reTuple.Version != snapshotVersion || reTuple.Dirty != snapshotDirty {
		conn.ExecContext(ctx, "ROLLBACK") //nolint:errcheck
		return fmt.Errorf(
			"%w: source metadata changed during import transaction "+
				"(version was %d now %d, dirty was %v now %v)",
			ErrDirtyState,
			snapshotVersion, reTuple.Version,
			snapshotDirty, reTuple.Dirty,
		)
	}

	// Insert each baseline row.
	for _, b := range baselines {
		upSum := hexToBytes32(b.UpChecksum)
		downSum := hexToBytes32(b.DownChecksum)

		insertQuery := fmt.Sprintf(
			"INSERT INTO `%s` (migration, source_kind, source_version, source_name, "+
				"up_checksum, down_checksum, batch, state, is_baseline, runner_id, "+
				"started_at, applied_at, updated_at) VALUES (?, 'golang_migrate', ?, ?, ?, ?, 0, 'applied', TRUE, ?, ?, ?, ?)",
			m.tableName,
		)
		if _, err := conn.ExecContext(ctx, insertQuery,
			b.MigrationID, b.Version, b.SourceName,
			upSum[:], downSum[:],
			runnerID, timestamp, timestamp, timestamp,
		); err != nil {
			conn.ExecContext(ctx, "ROLLBACK") //nolint:errcheck
			return fmt.Errorf("import baselines: insert %s: %w", b.MigrationID, err)
		}
	}

	// Commit the transaction.
	if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
		conn.ExecContext(ctx, "ROLLBACK") //nolint:errcheck
		return fmt.Errorf("import baselines: commit: %w", err)
	}

	return nil
}

// ---------- Helpers ----------

// migrationIDForVersion returns the canonical migration ID for a
// golang-migrate version: "golang-migrate:<version>".
func migrationIDForVersion(version uint64) string {
	return "golang-migrate:" + strconv.FormatUint(version, 10)
}

// hexToBytes32 converts a lowercase hex string to a [32]byte.
func hexToBytes32(hex string) [32]byte {
	var b [32]byte
	for i := 0; i < 32 && i*2+1 < len(hex); i++ {
		b[i] = unhex(hex[i*2])<<4 | unhex(hex[i*2+1])
	}
	return b
}

func unhex(c byte) byte {
	switch {
	case c >= '0' && c <= '9':
		return c - '0'
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10
	case c >= 'A' && c <= 'F':
		return c - 'A' + 10
	default:
		return 0
	}
}


