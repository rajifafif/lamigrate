package lamigrate

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// execute_down.go — Down-specific execution logic (§11.3).
//
// Before the first rollback SQL, the entire selected rollback set and
// all globally applied source checksums are preflighted.
//
// For each migration in reverse execution order:
// 1. Verify checksums match stored values
// 2. Mark applied → rolling_back via metadata transaction
// 3. Re-verify lock ownership
// 4. Execute exact down SQL bytes
// 5. Inspect session state
// 6. On success: delete the rolling_back row via metadata transaction
// 7. On failure: mark rolling_back → rollback_failed
// 8. Stop on first failure

// executeDown rolls back migrations from the last batch per §11.3.
func (m *Migrator) executeDown(
	ctx context.Context,
	conn *sql.Conn,
	caps *SessionCapabilities,
	plan *MigrationPlan,
) (Result, error) {
	result := Result{Command: "down"}

	if len(plan.migrations) == 0 {
		return result, nil
	}

	lockKey, err := deriveLockKey(caps.DatabaseName, m.tableName)
	if err != nil {
		return Result{}, fmt.Errorf("execute down: derive lock key: %w", err)
	}

	runnerID := generateRunnerID()

	// Execute each migration in the plan order (already reversed by planner).
	for _, mig := range plan.migrations {
		mr, err := m.executeRollbackOne(ctx, conn, caps, lockKey, runnerID, mig)
		result.Migrated = append(result.Migrated, mr)
		if err != nil {
			// Stop on first failure (§11.3).
			result.Errors = append(result.Errors, MigrationError{
				Name:  mig.name,
				Error: err,
			})
			return result, err
		}
	}

	return result, nil
}

// executeRollbackOne rolls back a single migration per §11.3.
// This function is shared by both Down and Reset since the rollback
// protocol is identical.
func (m *Migrator) executeRollbackOne(
	ctx context.Context,
	conn *sql.Conn,
	caps *SessionCapabilities,
	lockKey string,
	runnerID string,
	mig plannedMigration,
) (MigrationResult, error) {
	startedAt := time.Now().UTC()
	mr := MigrationResult{
		Name:      mig.name,
		Direction: "down",
	}

	// Step 1: Verify checksums match (§11.3 step 1).
	if err := verifyDownChecksums(ctx, conn, m.tableName, mig); err != nil {
		return mr, err
	}

	// Step 2: Mark applied → rolling_back (§11.3 step 2).
	if err := m.markRollingBack(ctx, conn, m.tableName, mig.name); err != nil {
		return mr, fmt.Errorf("mark rolling_back for %s: %w", mig.name, err)
	}

	// Verify rolling_back state was committed.
	var state string
	if err := conn.QueryRowContext(ctx,
		fmt.Sprintf("SELECT state FROM `%s` WHERE migration = ?", m.tableName),
		mig.name,
	).Scan(&state); err != nil {
		return mr, fmt.Errorf("verify rolling_back state for %s: %w", mig.name, err)
	}
	if state != "rolling_back" {
		return mr, fmt.Errorf(
			"%w: migration %s state = %q after mark rolling_back, want 'rolling_back'",
			ErrRecoveryRequired, mig.name, state,
		)
	}

	// Step 3: Re-verify lock ownership (§11.3 step 3).
	if err := verifyLockOwnership(ctx, conn, lockKey); err != nil {
		return mr, fmt.Errorf("pre-execution lock check for %s: %w", mig.name, err)
	}

	// Step 4: Execute exact down SQL bytes (§11.3 step 3).
	_, sqlErr := conn.ExecContext(ctx, string(mig.downSQL))

	// Step 5: Inspect session state (§11.3 step 4).
	postErr := inspectAndVerifyPostExecution(ctx, conn, caps.DatabaseName, lockKey)

	if sqlErr != nil || postErr != nil {
		// Step 5: Cleanup and mark rollback_failed (§11.3 step 5).
		if cleanupErr := cleanupSessionState(ctx, conn, caps.DatabaseName, lockKey); cleanupErr != nil {
			return mr, fmt.Errorf(
				"%w: migration %s rollback SQL failed and session cleanup failed: %v (original error: %v)",
				ErrRecoveryRequired, mig.name, cleanupErr, sqlErr,
			)
		}

		if err := m.markRollbackFailed(ctx, conn, m.tableName, mig.name); err != nil {
			return mr, fmt.Errorf(
				"%w: migration %s rollback failed but could not mark rollback_failed: %v (original error: %v)",
				ErrRecoveryRequired, mig.name, err, sqlErr,
			)
		}

		mr.Duration = time.Since(startedAt)
		if sqlErr != nil {
			return mr, fmt.Errorf("%w: %s: %v", ErrSQLExecution, mig.name, sqlErr)
		}
		return mr, fmt.Errorf("%w: %s: post-execution session check failed: %v", ErrSQLExecution, mig.name, postErr)
	}

	// Step 6: Delete the rolling_back row (§11.3 step 6).
	if err := m.removeRow(ctx, conn, m.tableName, mig.name); err != nil {
		return mr, fmt.Errorf(
			"%w: migration %s rollback SQL succeeded but row delete failed: %v",
			ErrRecoveryRequired, mig.name, err,
		)
	}

	// Verify row was durably removed.
	if err := verifyRowDeleted(ctx, conn, m.tableName, mig.name); err != nil {
		return mr, err
	}

	mr.Applied = true // meaning rollback succeeded (row removed)
	mr.Duration = time.Since(startedAt)
	return mr, nil
}

// verifyDownChecksums checks that the stored checksums for a migration
// row match the source file checksums in the plan (§11.3 step 1).
func verifyDownChecksums(
	ctx context.Context,
	conn *sql.Conn,
	tableName string,
	mig plannedMigration,
) error {
	var storedUp []byte
	var storedDown []byte

	err := conn.QueryRowContext(ctx,
		fmt.Sprintf(
			"SELECT up_checksum, down_checksum FROM `%s` WHERE migration = ?",
			tableName,
		),
		mig.name,
	).Scan(&storedUp, &storedDown)
	if err != nil {
		return fmt.Errorf("verify checksums for %s: %w", mig.name, err)
	}

	// Verify up checksum.
	if len(storedUp) == 32 {
		var storedSum [32]byte
		copy(storedSum[:], storedUp)
		if storedSum != mig.upSum {
			return fmt.Errorf(
				"%w: up checksum mismatch for %s",
				ErrChecksumDrift, mig.name,
			)
		}
	}

	// Verify down checksum (may be NULL for irreversible).
	if len(storedDown) == 32 {
		var storedDownSum [32]byte
		copy(storedDownSum[:], storedDown)
		if storedDownSum != mig.downSum {
			return fmt.Errorf(
				"%w: down checksum mismatch for %s",
				ErrChecksumDrift, mig.name,
			)
		}
	}

	return nil
}
