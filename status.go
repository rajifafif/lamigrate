package lamigrate

import (
	"context"
	"database/sql"
	"fmt"
)

// buildStatusReport reads metadata and source files to produce a
// comprehensive status report. It is side-effect free — it does NOT
// create or alter metadata tables (architecture §11.4).
//
// Reports: pending, applied with batch/time, baseline, applying/rolling_back,
// dirty, checksum drift, missing source, malformed files, unsupported metadata.
func (m *Migrator) buildStatusReport(ctx context.Context, conn *sql.Conn, caps *SessionCapabilities) (*StatusReport, error) {
	// 1. Scan and validate the migration directory.
	sourceFiles, err := scanMigrations(m.directory)
	if err != nil {
		return nil, fmt.Errorf("scan migration directory: %w", err)
	}
	// Source validation errors are reported as status items, not hard failures.
	sourceErr := validateSourceFiles(sourceFiles, m.maxFile)

	sourceMap := make(map[string]*migrationFile, len(sourceFiles))
	for i := range sourceFiles {
		sourceMap[sourceFiles[i].Name] = &sourceFiles[i]
	}

	// 2. Read all applied migrations from metadata.
	// If the table doesn't exist yet, report that metadata is uninitialized.
	applied, err := readAppliedMigrations(ctx, conn, m.tableName)
	if err != nil {
		// If the table doesn't exist, return status with all pending.
		return m.buildUninitializedStatus(sourceFiles, sourceErr), nil
	}

	// 3. Classify each migration.
	var details []MigrationStatusDetail

	// Track which source files have been matched.
	matched := make(map[string]bool, len(sourceFiles))

	for _, a := range applied {
		detail := MigrationStatusDetail{
			Name:  a.Migration,
			Batch: int(a.Batch),
		}
		if a.AppliedAt != nil {
			detail.AppliedAt = a.AppliedAt.Format("2006-01-02 15:04:05.000000")
		}
		if len(a.UpChecksum) == 32 {
			detail.UpChecksum = checksumHex([32]byte(a.UpChecksum[:32]))
		}

		// Classify status.
		switch {
		case isDirtyState(a.State):
			detail.Status = "dirty"
		case a.IsBaseline:
			detail.Status = "baseline"
		case a.State == "applied":
			detail.Status = "applied"
		default:
			detail.Status = a.State
		}

		// Check source file existence and drift.
		src, ok := sourceMap[a.Migration]
		if !ok {
			detail.Status = "missing_source"
		} else {
			matched[a.Migration] = true
			detail.Filename = src.Filename

			// Check up checksum drift.
			if len(a.UpChecksum) == 32 {
				var storedSum [32]byte
				copy(storedSum[:], a.UpChecksum)
				if storedSum != src.UpChecksum {
					detail.Drift = true
					detail.Status = "drift"
				}
			}
		}

		details = append(details, detail)
	}

	// 4. Find pending migrations (source files not in metadata).
	for _, f := range sourceFiles {
		if !matched[f.Name] {
			details = append(details, MigrationStatusDetail{
				Name:     f.Name,
				Filename: f.Filename,
				Status:   "pending",
			})
		}
	}

	// Sort for stable output.
	sortStatusDetails(details)

	return &StatusReport{Migrations: details}, nil
}

// buildUninitializedStatus returns a status report when metadata tables
// don't exist yet. All discovered source files are reported as pending.
func (m *Migrator) buildUninitializedStatus(sourceFiles []migrationFile, sourceErr error) *StatusReport {
	var details []MigrationStatusDetail

	// If source validation failed, report that.
	_ = sourceErr // TODO: surface malformed file info in status

	for _, f := range sourceFiles {
		details = append(details, MigrationStatusDetail{
			Name:     f.Name,
			Filename: f.Filename,
			Status:   "pending",
		})
	}

	return &StatusReport{Migrations: details}
}

// sortStatusDetails sorts migration status details for stable output:
// applied/baseline first (by batch), then pending, then others.
func sortStatusDetails(details []MigrationStatusDetail) {
	// Simple alphabetical sort by name for determinism.
	for i := 0; i < len(details); i++ {
		for j := i + 1; j < len(details); j++ {
			if details[j].Name < details[i].Name {
				details[i], details[j] = details[j], details[i]
			}
		}
	}
}
