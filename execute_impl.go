package lamigrate

import (
	"context"
	"database/sql"
)

// execute_impl.go — Execution method implementations.
//
// Up, Down, Reset, and Status are defined on *Migrator in types.go.
// This file contains the production implementations that delegate to
// the execution modules (execute_up.go, execute_down.go,
// execute_reset.go) after building a plan via the planner.
//
// Bootstrap is called BEFORE withLockSession because bootstrap creates
// its own private sessions and acquires the normal scope lock
// internally (§9 two-phase protocol). If bootstrap ran inside
// withLockSession, it would deadlock on the same advisory lock.

// Up applies pending migrations.
// It bootstraps metadata, then acquires the lock, builds the plan
// using the same planner as preview (§5.5), and executes each
// migration using the §11.2 protocol.
func (m *Migrator) Up(ctx context.Context, limit StepLimit) (Result, error) {
	if err := validateStepLimit(limit); err != nil {
		return Result{}, err
	}

	// Bootstrap metadata tables if needed (§9).
	// Must happen before withLockSession to avoid lock deadlock.
	if err := m.bootstrap(ctx); err != nil {
		return Result{}, err
	}

	var result Result
	err := m.withLockSession(ctx, func(ctx context.Context, conn *sql.Conn, caps *SessionCapabilities) error {
		plan, err := m.buildUpPlan(ctx, conn, caps, limit)
		if err != nil {
			return err
		}

		r, err := m.executeUp(ctx, conn, caps, plan)
		if err != nil {
			return err
		}
		result = r
		return nil
	})
	if err != nil {
		return Result{}, err
	}
	return result, nil
}

// Down rolls back migrations. The DownTarget controls selection:
//   - DownAll() or DownSteps(n): rollback from the latest batch.
//   - DownToName(name): rollback named migration + everything newer in latest batch.
//   - DownToBatch(n): rollback all migrations in batch n (must be latest).
//
// It bootstraps metadata, then acquires the lock, builds the plan,
// and executes each rollback using the §11.3 protocol.
func (m *Migrator) Down(ctx context.Context, target DownTarget) (Result, error) {
	if target.isZero() {
		target = DownAll()
	}

	// Bootstrap metadata tables if needed (§9).
	if err := m.bootstrap(ctx); err != nil {
		return Result{}, err
	}

	var result Result
	err := m.withLockSession(ctx, func(ctx context.Context, conn *sql.Conn, caps *SessionCapabilities) error {
		plan, err := m.buildDownPlan(ctx, conn, caps, target)
		if err != nil {
			return err
		}

		r, err := m.executeDown(ctx, conn, caps, plan)
		if err != nil {
			return err
		}
		result = r
		return nil
	})
	if err != nil {
		return Result{}, err
	}
	return result, nil
}

// Reset rolls back all applied migrations.
// It bootstraps metadata, then acquires the lock, builds the reset
// plan, and executes each rollback using the §11.3 protocol.
func (m *Migrator) Reset(ctx context.Context) (Result, error) {
	// Bootstrap metadata tables if needed (§9).
	if err := m.bootstrap(ctx); err != nil {
		return Result{}, err
	}

	var result Result
	err := m.withLockSession(ctx, func(ctx context.Context, conn *sql.Conn, caps *SessionCapabilities) error {
		plan, err := m.buildResetPlan(ctx, conn, caps)
		if err != nil {
			return err
		}

		r, err := m.executeReset(ctx, conn, caps, plan)
		if err != nil {
			return err
		}
		result = r
		return nil
	})
	if err != nil {
		return Result{}, err
	}
	return result, nil
}

// Status returns the full status report.
// It acquires the lock for read consistency and builds the status
// report without creating or modifying metadata.
func (m *Migrator) Status(ctx context.Context) (StatusReport, error) {
	var report StatusReport
	err := m.withLockSession(ctx, func(ctx context.Context, conn *sql.Conn, caps *SessionCapabilities) error {
		// Ensure metadata is initialized so we can read applied rows.
		// If bootstrap fails, we still want to report what we can.
		_ = m.bootstrap(ctx)

		r, err := m.buildStatusReport(ctx, conn, caps)
		if err != nil {
			return err
		}
		report = *r
		return nil
	})
	if err != nil {
		return StatusReport{}, err
	}
	return report, nil
}
