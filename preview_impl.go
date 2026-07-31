package lamigrate

import (
	"context"
	"database/sql"
)

// PreviewUp returns a read-only plan of pending migrations.
// It acquires the advisory lock for read consistency (§5.5, §11.5)
// but performs no metadata DDL/DML and no migration SQL.
func (m *Migrator) PreviewUp(ctx context.Context, limit StepLimit) (PlanView, error) {
	if err := validateStepLimit(limit); err != nil {
		return PlanView{}, err
	}

	var view PlanView
	err := m.withLockSession(ctx, func(ctx context.Context, conn *sql.Conn, caps *SessionCapabilities) error {
		// Ensure metadata is initialized so we can read applied rows.
		if err := m.bootstrap(ctx); err != nil {
			return err
		}

		plan, err := m.buildUpPlan(ctx, conn, caps, limit)
		if err != nil {
			return err
		}
		view = plan.toPlanView(m.directory, m.tableName, true)
		return nil
	})
	if err != nil {
		return PlanView{}, err
	}
	return view, nil
}

// PreviewDown returns a read-only plan of migrations to roll back.
// It acquires the advisory lock for read consistency (§5.5, §11.5)
// but performs no metadata DDL/DML and no migration SQL.
func (m *Migrator) PreviewDown(ctx context.Context, limit StepLimit) (PlanView, error) {
	if err := validateStepLimit(limit); err != nil {
		return PlanView{}, err
	}

	var view PlanView
	err := m.withLockSession(ctx, func(ctx context.Context, conn *sql.Conn, caps *SessionCapabilities) error {
		if err := m.bootstrap(ctx); err != nil {
			return err
		}

		plan, err := m.buildDownPlan(ctx, conn, caps, limit)
		if err != nil {
			return err
		}
		view = plan.toPlanView(m.directory, m.tableName, true)
		return nil
	})
	if err != nil {
		return PlanView{}, err
	}
	return view, nil
}

// PreviewReset returns a read-only plan of all applied migrations.
// It acquires the advisory lock for read consistency (§5.5, §11.5)
// but performs no metadata DDL/DML and no migration SQL.
func (m *Migrator) PreviewReset(ctx context.Context) (PlanView, error) {
	var view PlanView
	err := m.withLockSession(ctx, func(ctx context.Context, conn *sql.Conn, caps *SessionCapabilities) error {
		if err := m.bootstrap(ctx); err != nil {
			return err
		}

		plan, err := m.buildResetPlan(ctx, conn, caps)
		if err != nil {
			return err
		}
		view = plan.toPlanView(m.directory, m.tableName, true)
		return nil
	})
	if err != nil {
		return PlanView{}, err
	}
	return view, nil
}
