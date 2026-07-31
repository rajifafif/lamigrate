package lamigrate

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// execute_up.go — Up-specific execution logic (§11.2).
//
// For each planned migration:
// 1. Allocate batch (once, before any migration)
// 2. Insert "applying" intent row via metadata transaction
// 3. Re-verify lock ownership
// 4. Execute exact up SQL bytes
// 5. Inspect session state (autocommit, in_transaction, database, lock)
// 6. On success: commit applying → applied, set applied_at
// 7. On failure: commit applying → apply_failed
// 8. On uncertain commit: leave dirty, return recovery-required
//
// No migration executes after one fails. Batch is allocated only once
// per Up invocation.

// executeUp applies pending migrations according to §11.2.
func (m *Migrator) executeUp(
	ctx context.Context,
	conn *sql.Conn,
	caps *SessionCapabilities,
	plan *MigrationPlan,
) (Result, error) {
	result := Result{Command: "up"}

	// Empty plan: nothing to do.
	if len(plan.migrations) == 0 {
		return result, nil
	}

	// Derive lock key for re-verification (§10.2 step 9).
	lockKey, err := deriveLockKey(caps.DatabaseName, m.tableName)
	if err != nil {
		return Result{}, fmt.Errorf("execute up: derive lock key: %w", err)
	}

	// Allocate batch before any migration (§9.2, §11.2).
	batch, err := allocateBatch(ctx, conn, m.tableName)
	if err != nil {
		return Result{}, fmt.Errorf("execute up: allocate batch: %w", err)
	}

	runnerID := generateRunnerID()

	for _, mig := range plan.migrations {
		mr, err := m.executeUpOne(ctx, conn, caps, lockKey, batch, runnerID, mig)
		result.Migrated = append(result.Migrated, mr)
		if err != nil {
			// Stop on first failure (§11.2).
			result.Errors = append(result.Errors, MigrationError{
				Name:  mig.name,
				Error: err,
			})
			return result, err
		}
	}

	return result, nil
}

// executeUpOne applies a single migration per §11.2.
func (m *Migrator) executeUpOne(
	ctx context.Context,
	conn *sql.Conn,
	caps *SessionCapabilities,
	lockKey string,
	batch uint64,
	runnerID string,
	mig plannedMigration,
) (MigrationResult, error) {
	startedAt := time.Now().UTC()
	mr := MigrationResult{
		Name:      mig.name,
		Direction: "up",
		Batch:     int(batch),
	}

	// Step 1: Insert intent row (§11.2 step 1).
	intent := AppliedMigration{
		Migration:     mig.name,
		SourceKind:    "timestamp",
		SourceVersion: nil,
		SourceName:    mig.name,
		UpChecksum:    mig.upSum[:],
		DownChecksum:  mig.downSum[:],
		Batch:         batch,
		State:         "applying",
		IsBaseline:    false,
		RunnerID:      runnerID,
		StartedAt:     startedAt,
		UpdatedAt:     startedAt,
	}

	if err := m.insertIntent(ctx, conn, m.tableName, intent); err != nil {
		return mr, fmt.Errorf("insert intent for %s: %w", mig.name, err)
	}

	// Verify intent was durably committed (§11 step 6).
	if err := verifyIntentCommitted(ctx, conn, m.tableName, mig.name); err != nil {
		return mr, err
	}

	// Step 2: Re-verify lock ownership (§11.2 step 2).
	if err := verifyLockOwnership(ctx, conn, lockKey); err != nil {
		return mr, fmt.Errorf("pre-execution lock check for %s: %w", mig.name, err)
	}

	// Step 3: Execute exact up SQL bytes (§11.2 step 2).
	_, sqlErr := conn.ExecContext(ctx, string(mig.upSQL))

	// Step 4: Inspect session state (§11.2 step 3).
	postErr := inspectAndVerifyPostExecution(ctx, conn, caps.DatabaseName, lockKey)

	if sqlErr != nil || postErr != nil {
		// Step 4: On failure, cleanup session and mark failed (§11.2 step 4).
		if cleanupErr := cleanupSessionState(ctx, conn, caps.DatabaseName, lockKey); cleanupErr != nil {
			// Cannot cleanup — leave dirty, return recovery-required.
			return mr, fmt.Errorf(
				"%w: migration %s executed but session cleanup failed: %v (original SQL error: %v)",
				ErrRecoveryRequired, mig.name, cleanupErr, sqlErr,
			)
		}

		// Mark as apply_failed (§11.2 step 4).
		if err := m.markFailed(ctx, conn, m.tableName, mig.name); err != nil {
			// Commit of failure mark is uncertain — leave dirty.
			return mr, fmt.Errorf(
				"%w: migration %s failed but could not mark apply_failed: %v (original SQL error: %v)",
				ErrRecoveryRequired, mig.name, err, sqlErr,
			)
		}

		mr.Duration = time.Since(startedAt)
		if sqlErr != nil {
			return mr, fmt.Errorf("%w: %s: %v", ErrSQLExecution, mig.name, sqlErr)
		}
		return mr, fmt.Errorf("%w: %s: post-execution session check failed: %v", ErrSQLExecution, mig.name, postErr)
	}

	// Step 5: Execution succeeded with clean session (§11.2 step 5).
	appliedAt := time.Now().UTC()
	if err := m.markApplied(ctx, conn, m.tableName, mig.name, appliedAt); err != nil {
		// Commit of applied mark is uncertain — the SQL succeeded but
		// the metadata transition may not have durably committed.
		return mr, fmt.Errorf(
			"%w: migration %s SQL succeeded but mark applied failed: %v",
			ErrRecoveryRequired, mig.name, err,
		)
	}

	// Verify applied state was committed.
	var state string
	if err := conn.QueryRowContext(ctx,
		fmt.Sprintf("SELECT state FROM `%s` WHERE migration = ?", m.tableName),
		mig.name,
	).Scan(&state); err != nil {
		return mr, fmt.Errorf(
			"%w: verify applied state for %s: %v",
			ErrRecoveryRequired, mig.name, err,
		)
	}
	if state != "applied" {
		return mr, fmt.Errorf(
			"%w: migration %s state = %q after mark applied, want 'applied'",
			ErrRecoveryRequired, mig.name, state,
		)
	}

	mr.Applied = true
	mr.Duration = time.Since(startedAt)
	return mr, nil
}
