package lamigrate

import (
	"context"
	"database/sql"
	"fmt"
)

// execute_reset.go — Reset execution logic.
//
// Reset rolls back all non-baseline applied migrations in reverse
// execution order. It uses the same rollback protocol as Down (§11.3)
// but selects all applied non-baseline rows instead of just the latest
// batch.

// executeReset rolls back all non-baseline applied migrations per §9.2
// and §11.3. It delegates to the same rollback protocol as Down.
func (m *Migrator) executeReset(
	ctx context.Context,
	conn *sql.Conn,
	caps *SessionCapabilities,
	plan *MigrationPlan,
) (Result, error) {
	result := Result{Command: "reset"}

	if len(plan.migrations) == 0 {
		return result, nil
	}

	lockKey, err := deriveLockKey(caps.DatabaseName, m.tableName)
	if err != nil {
		return Result{}, fmt.Errorf("execute reset: derive lock key: %w", err)
	}

	runnerID := generateRunnerID()

	// Execute each migration in the plan order (already reversed by planner).
	for _, mig := range plan.migrations {
		mr, err := m.executeRollbackOne(ctx, conn, caps, lockKey, runnerID, mig)
		result.Migrated = append(result.Migrated, mr)
		if err != nil {
			// Stop on first failure.
			result.Errors = append(result.Errors, MigrationError{
				Name:  mig.name,
				Error: err,
			})
			return result, err
		}
	}

	return result, nil
}
