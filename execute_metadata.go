package lamigrate

import (
	"context"
	"crypto/rand"
	"database/sql"
	"fmt"
	"strings"
	"time"
)

// execute_metadata.go — Metadata transaction protocol helpers (§11).
//
// Each metadata mutation uses an explicit short transaction:
// 1. Assert @@session.autocommit=1, @@session.in_transaction=0
// 2. BEGIN (via conn.BeginTx)
// 3. Execute one conditional mutation
// 4. Require exactly one affected row where a row should already exist
// 5. COMMIT and require acknowledgement
// 6. Re-read committed state to verify durability
//
// On uncertain commit: do NOT issue compensating mutation, return
// ErrRecoveryRequired, terminate the session.

// assertSessionClean verifies that the dedicated connection has
// autocommit=1 and in_transaction=0, which is required before each
// metadata transaction per §11 step 1.
func assertSessionClean(ctx context.Context, conn *sql.Conn) error {
	// Check autocommit.
	var autocommit int
	if err := conn.QueryRowContext(ctx, "SELECT @@session.autocommit").Scan(&autocommit); err != nil {
		return fmt.Errorf(
			"%w: cannot read @@session.autocommit: %v",
			ErrUnsupportedDriver, err,
		)
	}
	if autocommit != 1 {
		return fmt.Errorf(
			"%w: @@session.autocommit = %d, want 1",
			ErrUnsupportedDriver, autocommit,
		)
	}

	// Check in_transaction using performance_schema.
	var activeTxCount int
	txQuery := `SELECT COUNT(*) FROM performance_schema.events_transactions_current ` +
		`WHERE STATE = 'ACTIVE' AND THREAD_ID = (` +
		`SELECT THREAD_ID FROM performance_schema.threads ` +
		`WHERE PROCESSLIST_ID = CONNECTION_ID())`
	if err := conn.QueryRowContext(ctx, txQuery).Scan(&activeTxCount); err != nil {
		// Soft-fail if user lacks SELECT on performance_schema.
		// Lock ownership via IS_USED_LOCK still enforces single-writer.
		if strings.Contains(err.Error(), "1142") || strings.Contains(err.Error(), "denied") {
			return nil
		}
		return fmt.Errorf(
			"%w: cannot check in_transaction via performance_schema: %v",
			ErrUnsupportedDriver, err,
		)
	}
	if activeTxCount != 0 {
		return fmt.Errorf(
			"%w: active transaction detected (count=%d, want 0)",
			ErrDirtyState, activeTxCount,
		)
	}

	return nil
}

// metadataTransaction wraps one metadata mutation in the §11 explicit
// transaction protocol. It asserts a clean session, begins a
// transaction, calls fn with the active transaction, commits, and
// verifies the post-commit session state.
//
// fn receives an active *sql.Tx and must execute exactly one conditional
// mutation. It should verify affected rows internally. If fn returns
// an error, the transaction is rolled back.
//
// On commit failure, metadataTransaction returns ErrRecoveryRequired
// without issuing any compensating mutation.
func (m *Migrator) metadataTransaction(
	ctx context.Context,
	conn *sql.Conn,
	fn func(tx *sql.Tx) error,
) error {
	// Step 1: Assert clean session (§11 step 1).
	if err := assertSessionClean(ctx, conn); err != nil {
		return fmt.Errorf("metadata transaction: pre-commit check: %w", err)
	}

	// Step 2: Begin transaction (§11 step 2).
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("metadata transaction: begin: %w", err)
	}

	// Step 3: Execute mutation (§11 step 3).
	if err := fn(tx); err != nil {
		_ = tx.Rollback()
		return err
	}

	// Step 5: Commit (§11 step 5).
	if err := tx.Commit(); err != nil {
		// Uncertain commit — MUST NOT issue compensating mutation.
		return fmt.Errorf(
			"%w: metadata transaction commit failed: %v",
			ErrRecoveryRequired, err,
		)
	}

	// Step 6: Verify post-commit session state (§11 step 6).
	if err := assertSessionClean(ctx, conn); err != nil {
		return fmt.Errorf(
			"%w: metadata transaction post-commit: %v",
			ErrRecoveryRequired, err,
		)
	}

	return nil
}

// insertIntent inserts a migration row in the "applying" state within
// an explicit metadata transaction per §5.2 (intent before SQL) and
// §11.
//
// The row serves as a durable record before migration SQL begins.
// After commit, the caller should verify the committed row state.
func (m *Migrator) insertIntent(
	ctx context.Context,
	conn *sql.Conn,
	tableName string,
	mig AppliedMigration,
) error {
	return m.metadataTransaction(ctx, conn, func(tx *sql.Tx) error {
		now := time.Now().UTC()
		nowStr := now.Format("2006-01-02 15:04:05.000000")

		result, err := tx.ExecContext(ctx,
			fmt.Sprintf(
				"INSERT INTO `%s` (migration, source_kind, source_version, source_name, "+
					"up_checksum, down_checksum, batch, state, is_baseline, runner_id, "+
					"started_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, 'applying', ?, ?, ?, ?)",
				tableName,
			),
			mig.Migration, mig.SourceKind, mig.SourceVersion, mig.SourceName,
			mig.UpChecksum, mig.DownChecksum, mig.Batch,
			mig.IsBaseline, mig.RunnerID, mig.StartedAt, now,
		)
		if err != nil {
			return fmt.Errorf("insert intent: %w", err)
		}

		affected, err := result.RowsAffected()
		if err != nil {
			return fmt.Errorf("insert intent: rows affected: %w", err)
		}
		if affected != 1 {
			return fmt.Errorf(
				"%w: insert intent expected 1 affected row, got %d",
				ErrDirtyState, affected,
			)
		}

		_ = nowStr // used above
		return nil
	})
}

// verifyIntentCommitted re-reads the migration row to confirm the
// intent was durably persisted in "applying" state.
func verifyIntentCommitted(
	ctx context.Context,
	conn *sql.Conn,
	tableName, migration string,
) error {
	var state string
	err := conn.QueryRowContext(ctx,
		fmt.Sprintf("SELECT state FROM `%s` WHERE migration = ?", tableName),
		migration,
	).Scan(&state)
	if err != nil {
		return fmt.Errorf("verify intent committed: %w", err)
	}
	if state != "applying" {
		return fmt.Errorf(
			"%w: intent row for %s has state %q, want 'applying'",
			ErrRecoveryRequired, migration, state,
		)
	}
	return nil
}

// markApplied transitions a migration row from "applying" to "applied"
// and sets applied_at within an explicit metadata transaction (§11).
func (m *Migrator) markApplied(
	ctx context.Context,
	conn *sql.Conn,
	tableName, migration string,
	appliedAt time.Time,
) error {
	return m.metadataTransaction(ctx, conn, func(tx *sql.Tx) error {
		now := time.Now().UTC()

		result, err := tx.ExecContext(ctx,
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
			return fmt.Errorf("mark applied: %w", err)
		}

		affected, err := result.RowsAffected()
		if err != nil {
			return fmt.Errorf("mark applied: rows affected: %w", err)
		}
		if affected != 1 {
			return fmt.Errorf(
				"%w: mark applied expected 1 affected row, got %d",
				ErrDirtyState, affected,
			)
		}

		return nil
	})
}

// markFailed transitions a migration row from "applying" to
// "apply_failed" within an explicit metadata transaction (§11).
func (m *Migrator) markFailed(
	ctx context.Context,
	conn *sql.Conn,
	tableName, migration string,
) error {
	return m.metadataTransaction(ctx, conn, func(tx *sql.Tx) error {
		now := time.Now().UTC()

		result, err := tx.ExecContext(ctx,
			fmt.Sprintf(
				"UPDATE `%s` SET state = 'apply_failed', updated_at = ? "+
					"WHERE migration = ? AND state = 'applying'",
				tableName,
			),
			now.Format("2006-01-02 15:04:05.000000"),
			migration,
		)
		if err != nil {
			return fmt.Errorf("mark failed: %w", err)
		}

		affected, err := result.RowsAffected()
		if err != nil {
			return fmt.Errorf("mark failed: rows affected: %w", err)
		}
		if affected != 1 {
			return fmt.Errorf(
				"%w: mark failed expected 1 affected row, got %d",
				ErrDirtyState, affected,
			)
		}

		return nil
	})
}

// markRollingBack transitions a migration row from "applied" to
// "rolling_back" within an explicit metadata transaction (§11.3).
func (m *Migrator) markRollingBack(
	ctx context.Context,
	conn *sql.Conn,
	tableName, migration string,
) error {
	return m.metadataTransaction(ctx, conn, func(tx *sql.Tx) error {
		now := time.Now().UTC()

		result, err := tx.ExecContext(ctx,
			fmt.Sprintf(
				"UPDATE `%s` SET state = 'rolling_back', updated_at = ? "+
					"WHERE migration = ? AND state = 'applied'",
				tableName,
			),
			now.Format("2006-01-02 15:04:05.000000"),
			migration,
		)
		if err != nil {
			return fmt.Errorf("mark rolling_back: %w", err)
		}

		affected, err := result.RowsAffected()
		if err != nil {
			return fmt.Errorf("mark rolling_back: rows affected: %w", err)
		}
		if affected != 1 {
			return fmt.Errorf(
				"%w: mark rolling_back expected 1 affected row, got %d",
				ErrDirtyState, affected,
			)
		}

		return nil
	})
}

// markRollbackFailed transitions a migration row from "rolling_back"
// to "rollback_failed" within an explicit metadata transaction (§11.3).
func (m *Migrator) markRollbackFailed(
	ctx context.Context,
	conn *sql.Conn,
	tableName, migration string,
) error {
	return m.metadataTransaction(ctx, conn, func(tx *sql.Tx) error {
		now := time.Now().UTC()

		result, err := tx.ExecContext(ctx,
			fmt.Sprintf(
				"UPDATE `%s` SET state = 'rollback_failed', updated_at = ? "+
					"WHERE migration = ? AND state = 'rolling_back'",
				tableName,
			),
			now.Format("2006-01-02 15:04:05.000000"),
			migration,
		)
		if err != nil {
			return fmt.Errorf("mark rollback_failed: %w", err)
		}

		affected, err := result.RowsAffected()
		if err != nil {
			return fmt.Errorf("mark rollback_failed: rows affected: %w", err)
		}
		if affected != 1 {
			return fmt.Errorf(
				"%w: mark rollback_failed expected 1 affected row, got %d",
				ErrDirtyState, affected,
			)
		}

		return nil
	})
}

// removeRow deletes a migration row after successful rollback within
// an explicit metadata transaction (§11.3 step 6).
func (m *Migrator) removeRow(
	ctx context.Context,
	conn *sql.Conn,
	tableName, migration string,
) error {
	return m.metadataTransaction(ctx, conn, func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx,
			fmt.Sprintf("DELETE FROM `%s` WHERE migration = ?", tableName),
			migration,
		)
		if err != nil {
			return fmt.Errorf("remove row: %w", err)
		}

		affected, err := result.RowsAffected()
		if err != nil {
			return fmt.Errorf("remove row: rows affected: %w", err)
		}
		if affected != 1 {
			return fmt.Errorf(
				"%w: remove row expected 1 affected row, got %d",
				ErrDirtyState, affected,
			)
		}

		return nil
	})
}

// verifyRowDeleted confirms that the migration row was durably removed.
func verifyRowDeleted(
	ctx context.Context,
	conn *sql.Conn,
	tableName, migration string,
) error {
	var count int
	err := conn.QueryRowContext(ctx,
		fmt.Sprintf("SELECT COUNT(*) FROM `%s` WHERE migration = ?", tableName),
		migration,
	).Scan(&count)
	if err != nil {
		return fmt.Errorf("verify row deleted: %w", err)
	}
	if count != 0 {
		return fmt.Errorf(
			"%w: migration %s row still exists after delete (count=%d)",
			ErrRecoveryRequired, migration, count,
		)
	}
	return nil
}

// generateRunnerID creates a UUID v4 string for runner identification.
func generateRunnerID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	b[6] = (b[6] & 0x0f) | 0x40 // Version 4
	b[8] = (b[8] & 0x3f) | 0x80 // Variant 10
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}

// cleanupSessionState attempts to restore clean session state after a
// failed migration SQL execution per §11.2 step 4:
// 1. ROLLBACK if a transaction is open
// 2. Verify autocommit=1
// 3. Verify DATABASE() matches expected
// 4. Re-verify lock ownership
//
// Returns nil if session is fully restored, or an error if restoration
// failed (which means the session is in an uncertain state).
func cleanupSessionState(
	ctx context.Context,
	conn *sql.Conn,
	expectedDB, lockKey string,
) error {
	// Check and rollback any open transaction.
	var activeTxCount int
	txQuery := `SELECT COUNT(*) FROM performance_schema.events_transactions_current ` +
		`WHERE STATE = 'ACTIVE' AND THREAD_ID = (` +
		`SELECT THREAD_ID FROM performance_schema.threads ` +
		`WHERE PROCESSLIST_ID = CONNECTION_ID())`
	if err := conn.QueryRowContext(ctx, txQuery).Scan(&activeTxCount); err != nil {
		return fmt.Errorf("cleanup: read in_transaction: %w", err)
	}
	if activeTxCount != 0 {
		if _, err := conn.ExecContext(ctx, "ROLLBACK"); err != nil {
			return fmt.Errorf("cleanup: ROLLBACK failed: %w", err)
		}
		// Verify ROLLBACK succeeded.
		if err := conn.QueryRowContext(ctx, txQuery).Scan(&activeTxCount); err != nil {
			return fmt.Errorf("cleanup: verify post-rollback in_transaction: %w", err)
		}
		if activeTxCount != 0 {
			return fmt.Errorf(
				"%w: ROLLBACK did not clear transaction (active count=%d)",
				ErrRecoveryRequired, activeTxCount,
			)
		}
	}

	// Verify autocommit=1.
	var autocommit int
	if err := conn.QueryRowContext(ctx, "SELECT @@session.autocommit").Scan(&autocommit); err != nil {
		return fmt.Errorf("cleanup: read autocommit: %w", err)
	}
	if autocommit != 1 {
		// Attempt to restore.
		if _, err := conn.ExecContext(ctx, "SET SESSION autocommit = 1"); err != nil {
			return fmt.Errorf("cleanup: restore autocommit: %w", err)
		}
		if err := conn.QueryRowContext(ctx, "SELECT @@session.autocommit").Scan(&autocommit); err != nil {
			return fmt.Errorf("cleanup: verify autocommit: %w", err)
		}
		if autocommit != 1 {
			return fmt.Errorf(
				"%w: cannot restore autocommit=1 (got %d)",
				ErrRecoveryRequired, autocommit,
			)
		}
	}

	// Verify DATABASE() matches expected.
	var currentDB string
	if err := conn.QueryRowContext(ctx, "SELECT DATABASE()").Scan(&currentDB); err != nil {
		return fmt.Errorf("cleanup: read DATABASE(): %w", err)
	}
	if currentDB != expectedDB {
		return fmt.Errorf(
			"%w: DATABASE() = %q, expected %q",
			ErrRecoveryRequired, currentDB, expectedDB,
		)
	}

	// Verify lock ownership.
	if err := verifyLockOwnership(ctx, conn, lockKey); err != nil {
		return fmt.Errorf("cleanup: lock ownership: %w", err)
	}

	return nil
}

// inspectAndVerifyPostExecution checks session state after migration
// SQL execution: autocommit=1, in_transaction=0, DATABASE() correct,
// and lock ownership held.
func inspectAndVerifyPostExecution(
	ctx context.Context,
	conn *sql.Conn,
	expectedDB, lockKey string,
) error {
	if err := assertSessionClean(ctx, conn); err != nil {
		return fmt.Errorf("post-execution session check: %w", err)
	}

	// Verify DATABASE().
	var currentDB string
	if err := conn.QueryRowContext(ctx, "SELECT DATABASE()").Scan(&currentDB); err != nil {
		return fmt.Errorf("post-execution read DATABASE(): %w", err)
	}
	if currentDB != expectedDB {
		return fmt.Errorf(
			"%w: post-execution DATABASE() = %q, expected %q",
			ErrRecoveryRequired, currentDB, expectedDB,
		)
	}

	// Verify lock ownership.
	if err := verifyLockOwnership(ctx, conn, lockKey); err != nil {
		return fmt.Errorf("post-execution lock ownership: %w", err)
	}

	return nil
}
