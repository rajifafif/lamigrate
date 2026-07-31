package lamigrate

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// metadata.go — Core metadata operations for the v1 metadata model.
//
// These functions operate on validated metadata tables while holding
// the appropriate advisory lock. All mutations use explicit short
// transactions per architecture §11.

// AppliedMigration represents a row read from the migration-state table.
type AppliedMigration struct {
	ID            uint64
	Migration     string
	SourceKind    string
	SourceVersion *uint64 // NULL for timestamp migrations
	SourceName    string
	UpChecksum    []byte
	DownChecksum  []byte // NULL for irreversible
	Batch         uint64
	State         string
	IsBaseline    bool
	RunnerID      string
	StartedAt     time.Time
	AppliedAt     *time.Time // NULL when applying or apply_failed
	UpdatedAt     time.Time
}

// ControlRow represents a row from the lamigrate_control table.
type ControlRow struct {
	TrackingTable string
	SchemaVersion uint64
	NextBatch     uint64
	UpdatedAt     time.Time
}

// readAppliedMigrations reads all rows from the migration-state table
// with their checksums. This is used for preflight validation and
// status reporting.
func readAppliedMigrations(ctx context.Context, conn *sql.Conn, tableName string) ([]AppliedMigration, error) {
	query := fmt.Sprintf(
		"SELECT id, migration, source_kind, source_version, source_name, "+
			"up_checksum, down_checksum, batch, state, is_baseline, runner_id, "+
			"started_at, applied_at, updated_at FROM `%s` ORDER BY id ASC",
		tableName,
	)

	rows, err := conn.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("read applied migrations: %w", err)
	}
	defer rows.Close()

	var migrations []AppliedMigration
	for rows.Next() {
		var m AppliedMigration
		if err := rows.Scan(
			&m.ID, &m.Migration, &m.SourceKind, &m.SourceVersion,
			&m.SourceName, &m.UpChecksum, &m.DownChecksum,
			&m.Batch, &m.State, &m.IsBaseline, &m.RunnerID,
			&m.StartedAt, &m.AppliedAt, &m.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan migration row: %w", err)
		}

		// Validate cross-field invariants on every read (architecture §9).
		if err := validateStateRow(
			m.SourceKind, m.SourceVersion, m.IsBaseline,
			m.Batch, m.State, m.AppliedAt,
		); err != nil {
			return nil, fmt.Errorf("validate row id=%d: %w", m.ID, err)
		}

		migrations = append(migrations, m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate migration rows: %w", err)
	}

	return migrations, nil
}

// readControlRow reads the control row for the given tracking table.
// Returns the control row and the maximum positive batch number across
// all state-table rows.
func readControlRow(ctx context.Context, conn *sql.Conn, tableName string) (*ControlRow, uint64, error) {
	var cr ControlRow
	err := conn.QueryRowContext(ctx,
		"SELECT tracking_table, schema_version, next_batch, updated_at "+
			"FROM `"+controlTableName+"` WHERE tracking_table = ?",
		tableName,
	).Scan(&cr.TrackingTable, &cr.SchemaVersion, &cr.NextBatch, &cr.UpdatedAt)
	if err != nil {
		return nil, 0, fmt.Errorf("read control row: %w", err)
	}

	// Read max positive batch from state table.
	var maxBatch sql.NullInt64
	err = conn.QueryRowContext(ctx,
		fmt.Sprintf("SELECT MAX(batch) FROM `%s` WHERE batch > 0", tableName),
	).Scan(&maxBatch)
	if err != nil {
		return nil, 0, fmt.Errorf("read max batch: %w", err)
	}

	var maxPositiveBatch uint64
	if maxBatch.Valid {
		maxPositiveBatch = uint64(maxBatch.Int64)
	}

	return &cr, maxPositiveBatch, nil
}

// insertMigrationIntent inserts a migration row in the "applying"
// state. This is done before executing migration SQL per architecture
// §5.2 (intent before SQL). The row serves as a durable record of
// the intent.
//
// This function expects to be called within an explicit transaction
// that will be committed by the caller.
func insertMigrationIntent(ctx context.Context, conn *sql.Conn, tableName string, m AppliedMigration) error {
	_, err := conn.ExecContext(ctx,
		fmt.Sprintf(
			"INSERT INTO `%s` (migration, source_kind, source_version, source_name, "+
				"up_checksum, down_checksum, batch, state, is_baseline, runner_id, "+
				"started_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, 'applying', ?, ?, ?, ?)",
			tableName,
		),
		m.Migration, m.SourceKind, m.SourceVersion, m.SourceName,
		m.UpChecksum, m.DownChecksum, m.Batch,
		m.IsBaseline, m.RunnerID, m.StartedAt, m.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("insert migration intent: %w", err)
	}
	return nil
}

// markApplied transitions a migration row from "applying" to "applied"
// and sets the applied_at timestamp. This is done in an explicit
// metadata transaction per architecture §11.
func markApplied(ctx context.Context, conn *sql.Conn, tableName, migration string, appliedAt time.Time) error {
	if _, err := conn.ExecContext(ctx, "START TRANSACTION"); err != nil {
		return fmt.Errorf("mark applied: start transaction: %w", err)
	}

	now := time.Now().UTC()
	result, err := conn.ExecContext(ctx,
		fmt.Sprintf(
			"UPDATE `%s` SET state = 'applied', applied_at = ?, updated_at = ? "+
				"WHERE migration = ? AND state = 'applying'",
			tableName,
		),
		appliedAt.Format("2006-01-02 15:04:05.000000"),
		now.Format("2006-01-02 15:04:05.000000"),
		migration,
	)
	if err != nil {
		conn.ExecContext(ctx, "ROLLBACK") //nolint:errcheck
		return fmt.Errorf("mark applied: update: %w", err)
	}

	affected, err := result.RowsAffected()
	if err != nil {
		conn.ExecContext(ctx, "ROLLBACK") //nolint:errcheck
		return fmt.Errorf("mark applied: rows affected: %w", err)
	}
	if affected != 1 {
		conn.ExecContext(ctx, "ROLLBACK") //nolint:errcheck
		return fmt.Errorf(
			"%w: mark applied expected 1 affected row, got %d",
			ErrDirtyState, affected,
		)
	}

	if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
		conn.ExecContext(ctx, "ROLLBACK") //nolint:errcheck
		return fmt.Errorf("mark applied: commit: %w", err)
	}

	return nil
}

// markFailed transitions a migration row from "applying" to
// "apply_failed". This is done in an explicit metadata transaction
// per architecture §11.
func markFailed(ctx context.Context, conn *sql.Conn, tableName, migration string) error {
	if _, err := conn.ExecContext(ctx, "START TRANSACTION"); err != nil {
		return fmt.Errorf("mark failed: start transaction: %w", err)
	}

	now := time.Now().UTC()
	result, err := conn.ExecContext(ctx,
		fmt.Sprintf(
			"UPDATE `%s` SET state = 'apply_failed', updated_at = ? "+
				"WHERE migration = ? AND state = 'applying'",
			tableName,
		),
		now.Format("2006-01-02 15:04:05.000000"),
		migration,
	)
	if err != nil {
		conn.ExecContext(ctx, "ROLLBACK") //nolint:errcheck
		return fmt.Errorf("mark failed: update: %w", err)
	}

	affected, err := result.RowsAffected()
	if err != nil {
		conn.ExecContext(ctx, "ROLLBACK") //nolint:errcheck
		return fmt.Errorf("mark failed: rows affected: %w", err)
	}
	if affected != 1 {
		conn.ExecContext(ctx, "ROLLBACK") //nolint:errcheck
		return fmt.Errorf(
			"%w: mark failed expected 1 affected row, got %d",
			ErrDirtyState, affected,
		)
	}

	if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
		conn.ExecContext(ctx, "ROLLBACK") //nolint:errcheck
		return fmt.Errorf("mark failed: commit: %w", err)
	}

	return nil
}

// removeMigration deletes a migration row on successful rollback.
// The row is deleted from the migration-state table in an explicit
// metadata transaction per architecture §11.
func removeMigration(ctx context.Context, conn *sql.Conn, tableName, migration string) error {
	if _, err := conn.ExecContext(ctx, "START TRANSACTION"); err != nil {
		return fmt.Errorf("remove migration: start transaction: %w", err)
	}

	result, err := conn.ExecContext(ctx,
		fmt.Sprintf("DELETE FROM `%s` WHERE migration = ?", tableName),
		migration,
	)
	if err != nil {
		conn.ExecContext(ctx, "ROLLBACK") //nolint:errcheck
		return fmt.Errorf("remove migration: delete: %w", err)
	}

	affected, err := result.RowsAffected()
	if err != nil {
		conn.ExecContext(ctx, "ROLLBACK") //nolint:errcheck
		return fmt.Errorf("remove migration: rows affected: %w", err)
	}
	if affected != 1 {
		conn.ExecContext(ctx, "ROLLBACK") //nolint:errcheck
		return fmt.Errorf(
			"%w: remove migration expected 1 affected row, got %d",
			ErrDirtyState, affected,
		)
	}

	if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
		conn.ExecContext(ctx, "ROLLBACK") //nolint:errcheck
		return fmt.Errorf("remove migration: commit: %w", err)
	}

	return nil
}
