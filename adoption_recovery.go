package lamigrate

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// ErrInterruptedPrototypeAdoption indicates that a v1 state table exists
// without its required control row, signaling a crash between rename
// and control-row creation during prototype adoption (architecture §9.3).
var ErrInterruptedPrototypeAdoption = errors.New("lamigrate: interrupted prototype adoption")

// detectInterruptedAdoption checks whether the given tracking table
// is in a v1 state without a control row, indicating an interrupted
// prototype adoption (architecture §9.3).
func detectInterruptedAdoption(ctx context.Context, conn *sql.Conn, database, tableName string) (bool, error) {
	// Check if the state table exists and is a valid v1 shape.
	if err := validateTableShape(ctx, conn, database, tableName, "state"); err != nil {
		return false, nil
	}

	// Check if the control row exists for this tracking table.
	var controlCount int
	err := conn.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM `"+controlTableName+"` WHERE tracking_table = ? AND schema_version = 1",
		tableName,
	).Scan(&controlCount)
	if err != nil {
		return false, fmt.Errorf("detect interrupted adoption: check control row: %w", err)
	}

	if controlCount > 0 {
		return false, nil
	}

	// v1 state table exists but no control row — interrupted adoption.
	return true, nil
}

// readV1StateRows reads id, migration, batch, applied_at from the v1
// state table ordered by id ASC, returning rows compatible with
// prototypeRow for source re-mapping during recovery.
func readV1StateRows(ctx context.Context, conn *sql.Conn, tableName string) ([]prototypeRow, error) {
	query := fmt.Sprintf(
		"SELECT id, migration, batch, applied_at FROM `%s` ORDER BY id ASC",
		tableName,
	)

	rows, err := conn.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("read v1 state rows: %w", err)
	}
	defer rows.Close()

	var result []prototypeRow
	for rows.Next() {
		var r prototypeRow
		if err := rows.Scan(&r.ID, &r.Migration, &r.Batch, &r.AppliedAt); err != nil {
			return nil, fmt.Errorf("scan v1 state row: %w", err)
		}
		result = append(result, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate v1 state rows: %w", err)
	}

	return result, nil
}

// recoverAdoption recovers from an interrupted prototype adoption.
// This is the entry point for standalone recovery calls.
func (m *Migrator) recoverAdoption(ctx context.Context, request AdoptionRequest) error {
	if err := validateBackupTableName(request.BackupTable, m.tableName); err != nil {
		return fmt.Errorf("recover adoption: %w", err)
	}

	err := m.withLockSession(ctx, func(ctx context.Context, conn *sql.Conn, caps *SessionCapabilities) error {
		return doRecoverAdoption(ctx, conn, caps, m, request)
	})
	return err
}

// doRecoverAdoption performs the actual recovery work on an existing
// connection. It is called both from recoverAdoption (standalone) and
// from AdoptPrototype (when interrupted adoption is detected inside
// an existing lock session).
func doRecoverAdoption(ctx context.Context, conn *sql.Conn, caps *SessionCapabilities, m *Migrator, request AdoptionRequest) error {
	// a. Detect interrupted adoption.
	interrupted, err := detectInterruptedAdoption(ctx, conn, caps.DatabaseName, m.tableName)
	if err != nil {
		return fmt.Errorf("recover adoption: detect interrupted: %w", err)
	}
	if !interrupted {
		return fmt.Errorf(
			"%w: no interrupted adoption detected for table %q",
			ErrInterruptedPrototypeAdoption, m.tableName,
		)
	}

	// c. Verify backup table exists.
	var backupExists int
	err = conn.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = ? AND table_name = ?",
		caps.DatabaseName, request.BackupTable,
	).Scan(&backupExists)
	if err != nil {
		return fmt.Errorf("recover adoption: check backup table: %w", err)
	}
	if backupExists == 0 {
		return fmt.Errorf(
			"%w: backup table %q does not exist",
			ErrInterruptedPrototypeAdoption, request.BackupTable,
		)
	}

	// d. Read ALL rows from the v1 state table.
	applied, err := readAppliedMigrations(ctx, conn, m.tableName)
	if err != nil {
		return fmt.Errorf("recover adoption: read applied: %w", err)
	}
	if len(applied) == 0 {
		return fmt.Errorf(
			"%w: v1 state table %q is empty",
			ErrInterruptedPrototypeAdoption, m.tableName,
		)
	}

	// e. Verify every row.
	for _, a := range applied {
		if a.State != "applied" {
			return fmt.Errorf(
				"%w: row %d has state %q, want 'applied'",
				ErrInterruptedPrototypeAdoption, a.ID, a.State,
			)
		}
		if a.SourceKind != "timestamp" && a.SourceKind != "golang_migrate" {
			return fmt.Errorf(
				"%w: row %d has invalid source_kind %q",
				ErrInterruptedPrototypeAdoption, a.ID, a.SourceKind,
			)
		}
	}

	// f. Re-map source files to verify checksums.
	protoRows, err := readV1StateRows(ctx, conn, m.tableName)
	if err != nil {
		return fmt.Errorf("recover adoption: read v1 rows: %w", err)
	}
	mappings, err := mapSourceFiles(protoRows, m.directory, m.legacyDir)
	if err != nil {
		return fmt.Errorf("recover adoption: re-map sources: %w", err)
	}

	// Verify checksums.
	appliedByMigration := make(map[string]*AppliedMigration, len(applied))
	for i := range applied {
		appliedByMigration[applied[i].Migration] = &applied[i]
	}
	for _, sm := range mappings {
		stored, ok := appliedByMigration[sm.Migration]
		if !ok {
			return fmt.Errorf(
				"%w: migration %q not in v1 table",
				ErrInterruptedPrototypeAdoption, sm.Migration,
			)
		}
		if len(stored.UpChecksum) == 32 {
			var storedSum [32]byte
			copy(storedSum[:], stored.UpChecksum)
			if storedSum != sm.UpChecksum {
				return fmt.Errorf(
					"%w: up checksum mismatch for %q",
					ErrInterruptedPrototypeAdoption, sm.Migration,
				)
			}
		}
	}

	// g. Compute expected next_batch.
	var maxBatch sql.NullInt64
	err = conn.QueryRowContext(ctx,
		fmt.Sprintf("SELECT MAX(batch) FROM `%s` WHERE batch > 0", m.tableName),
	).Scan(&maxBatch)
	if err != nil {
		return fmt.Errorf("recover adoption: read max batch: %w", err)
	}
	var maxPositiveBatch uint64
	if maxBatch.Valid && maxBatch.Int64 > 0 {
		maxPositiveBatch = uint64(maxBatch.Int64)
	}
	nextBatch := maxPositiveBatch + 1
	if nextBatch < 1 {
		nextBatch = 1
	}

	// h. Create control row.
	now := time.Now().UTC()
	_, err = conn.ExecContext(ctx,
		"INSERT INTO `"+controlTableName+"` (tracking_table, schema_version, next_batch, updated_at)"+
			" VALUES (?, 1, ?, ?)",
		m.tableName, nextBatch, now.Format("2006-01-02 15:04:05.000000"),
	)
	if err != nil {
		return fmt.Errorf("recover adoption: insert control row: %w", err)
	}

	// i. Re-read and verify control row.
	cr, verifyMaxPB, err := readControlRow(ctx, conn, m.tableName)
	if err != nil {
		return fmt.Errorf("recover adoption: verify control row: %w", err)
	}
	if err := validateControlRow(cr.SchemaVersion, cr.NextBatch, verifyMaxPB); err != nil {
		return fmt.Errorf("recover adoption: validate control row: %w", err)
	}
	if cr.NextBatch != nextBatch {
		return fmt.Errorf(
			"%w: next_batch=%d != expected %d",
			ErrInterruptedPrototypeAdoption, cr.NextBatch, nextBatch,
		)
	}

	return nil
}
