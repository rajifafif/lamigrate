package lamigrate

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// repair_metadata.go — Metadata transitions for the explicit repair
// workflow (architecture §12).
//
// Repair transitions are strictly conditional: they verify the current
// state before mutating, and require exactly one affected row. This
// prevents optimistic updates and ensures every repair is grounded in
// observable database state.

// requireDirtyState verifies that the named migration is in one of the
// dirty states and returns the current row details. If the migration is
// not found or is in a clean state, ErrRepairRejected is returned.
func requireDirtyState(
	ctx context.Context,
	conn *sql.Conn,
	tableName, migration string,
) (*AppliedMigration, error) {
	// Read the full migration row to determine its current state.
	row := conn.QueryRowContext(ctx,
		fmt.Sprintf(
			"SELECT id, migration, source_kind, source_version, source_name, "+
				"up_checksum, down_checksum, batch, state, is_baseline, runner_id, "+
				"started_at, applied_at, updated_at FROM `%s` WHERE migration = ?",
			tableName,
		),
		migration,
	)

	var am AppliedMigration
	err := row.Scan(
		&am.ID, &am.Migration, &am.SourceKind, &am.SourceVersion,
		&am.SourceName, &am.UpChecksum, &am.DownChecksum,
		&am.Batch, &am.State, &am.IsBaseline, &am.RunnerID,
		&am.StartedAt, &am.AppliedAt, &am.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf(
			"%w: migration %q not found in metadata table",
			ErrRepairRejected, migration,
		)
	}
	if err != nil {
		return nil, fmt.Errorf("requireDirtyState: read row: %w", err)
	}

	if !isDirtyState(am.State) {
		return nil, fmt.Errorf(
			"%w: migration %q is in state %q, not a dirty state",
			ErrRepairRejected, migration, am.State,
		)
	}

	return &am, nil
}

// requireCleanAppliedIrreversible verifies that the named migration is
// in "applied" state with a NULL down_checksum (irreversible). This is
// required for the mark-rolled-back operation on clean irreversible
// migrations (architecture §12).
func requireCleanAppliedIrreversible(
	ctx context.Context,
	conn *sql.Conn,
	tableName, migration string,
) (*AppliedMigration, error) {
	row := conn.QueryRowContext(ctx,
		fmt.Sprintf(
			"SELECT id, migration, source_kind, source_version, source_name, "+
				"up_checksum, down_checksum, batch, state, is_baseline, runner_id, "+
				"started_at, applied_at, updated_at FROM `%s` WHERE migration = ?",
			tableName,
		),
		migration,
	)

	var am AppliedMigration
	err := row.Scan(
		&am.ID, &am.Migration, &am.SourceKind, &am.SourceVersion,
		&am.SourceName, &am.UpChecksum, &am.DownChecksum,
		&am.Batch, &am.State, &am.IsBaseline, &am.RunnerID,
		&am.StartedAt, &am.AppliedAt, &am.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf(
			"%w: migration %q not found in metadata table",
			ErrRepairRejected, migration,
		)
	}
	if err != nil {
		return nil, fmt.Errorf("requireCleanAppliedIrreversible: read row: %w", err)
	}

	if am.State != "applied" {
		return nil, fmt.Errorf(
			"%w: migration %q is in state %q, not 'applied'",
			ErrRepairRejected, migration, am.State,
		)
	}

	if am.DownChecksum != nil {
		return nil, fmt.Errorf(
			"%w: migration %q has a down checksum (reversible), "+
				"mark-rolled-back is only for irreversible migrations or dirty states",
			ErrRepairRejected, migration,
		)
	}

	return &am, nil
}

// markAppliedByRepair transitions a dirty migration row to "applied"
// and sets applied_at. Legal transitions:
//   - applying -> applied
//   - apply_failed -> applied
//
// The operator must have verified the database state first.
func (m *Migrator) markAppliedByRepair(
	ctx context.Context,
	conn *sql.Conn,
	tableName, migration string,
) error {
	return m.metadataTransaction(ctx, conn, func(tx *sql.Tx) error {
		now := time.Now().UTC()
		result, err := tx.ExecContext(ctx,
			fmt.Sprintf(
				"UPDATE `%s` SET state = 'applied', applied_at = ?, updated_at = ? "+
					"WHERE migration = ? AND state IN ('applying', 'apply_failed')",
				tableName,
			),
			now.Format("2006-01-02 15:04:05.000000"),
			now.Format("2006-01-02 15:04:05.000000"),
			migration,
		)
		if err != nil {
			return fmt.Errorf("markAppliedByRepair: update: %w", err)
		}

		affected, err := result.RowsAffected()
		if err != nil {
			return fmt.Errorf("markAppliedByRepair: rows affected: %w", err)
		}
		if affected != 1 {
			return fmt.Errorf(
				"%w: markAppliedByRepair expected 1 affected row, got %d",
				ErrRepairRejected, affected,
			)
		}

		return nil
	})
}

// markRolledBackByRepair transitions a dirty migration row to "applied"
// and then deletes it (row absent), simulating a completed rollback.
// Legal transitions:
//   - rolling_back -> applied (then deleted)
//   - rollback_failed -> applied (then deleted)
//
// It also supports removing a clean irreversible applied migration
// after manual compensation. In that case, applied -> row absent.
func (m *Migrator) markRolledBackByRepair(
	ctx context.Context,
	conn *sql.Conn,
	tableName, migration string,
) error {
	// First, check the current state to determine the path.
	var state string
	err := conn.QueryRowContext(ctx,
		fmt.Sprintf("SELECT state FROM `%s` WHERE migration = ?", tableName),
		migration,
	).Scan(&state)
	if err != nil {
		return fmt.Errorf("markRolledBackByRepair: read state: %w", err)
	}

	// For dirty rollback states, transition to applied first, then delete.
	// For clean irreversible applied, just delete.
	switch state {
	case "rolling_back", "rollback_failed":
		// Transition to applied (which sets applied_at if null).
		if err := m.metadataTransaction(ctx, conn, func(tx *sql.Tx) error {
			now := time.Now().UTC()
			result, err := tx.ExecContext(ctx,
				fmt.Sprintf(
					"UPDATE `%s` SET state = 'applied', applied_at = COALESCE(applied_at, ?), updated_at = ? "+
						"WHERE migration = ? AND state IN ('rolling_back', 'rollback_failed')",
					tableName,
				),
				now.Format("2006-01-02 15:04:05.000000"),
				now.Format("2006-01-02 15:04:05.000000"),
				migration,
			)
			if err != nil {
				return fmt.Errorf("markRolledBackByRepair: transition: %w", err)
			}
			affected, err := result.RowsAffected()
			if err != nil {
				return fmt.Errorf("markRolledBackByRepair: rows affected: %w", err)
			}
			if affected != 1 {
				return fmt.Errorf(
					"%w: markRolledBackByRepair transition expected 1 affected row, got %d",
					ErrRepairRejected, affected,
				)
			}
			return nil
		}); err != nil {
			return err
		}
	case "applied":
		// Clean irreversible applied — proceed directly to delete.
	default:
		return fmt.Errorf(
			"%w: markRolledBackByRepair: unexpected state %q for %q",
			ErrRepairRejected, state, migration,
		)
	}

	// Delete the row.
	return m.removeRow(ctx, conn, tableName, migration)
}

// removeFailedByRepair deletes a dirty migration row, removing the
// failed intent record entirely. This is used when the operator has
// verified that the migration SQL had no effect.
func (m *Migrator) removeFailedByRepair(
	ctx context.Context,
	conn *sql.Conn,
	tableName, migration string,
) error {
	return m.removeRow(ctx, conn, tableName, migration)
}
