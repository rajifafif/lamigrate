package lamigrate

import (
	"context"
	"database/sql"
	"fmt"
)

// execute_refresh.go — Refresh execution logic.
//
// Refresh rolls back a set of migrations, then re-applies them (or a subset).
// Both phases run within a single advisory-lock session.

// executeRefresh runs the refresh: rollback phase then forward phase.
// It is called within a single lock session.
func (m *Migrator) executeRefresh(
	ctx context.Context,
	conn *sql.Conn,
	caps *SessionCapabilities,
	plan *RefreshPlan,
) (RefreshResult, error) {
	var result RefreshResult

	// Phase 1: Rollback.
	rbResult, err := m.executeDown(ctx, conn, caps, plan.downPlan)
	result.Rollback = rbResult
	if err != nil {
		return result, err
	}

	// Phase 2: Forward (re-apply).
	if len(plan.upPlan.migrations) == 0 {
		return result, nil
	}

	apResult, err := m.executeUp(ctx, conn, caps, plan.upPlan)
	result.Apply = apResult
	if err != nil {
		// Forward phase failed — state is partially refreshed.
		// The caller can recover by running lamigrate up.
		return result, err
	}

	return result, nil
}

// Refresh executes a refresh operation: rollback + re-apply.
// Both phases run within a single advisory-lock session.
func (m *Migrator) Refresh(ctx context.Context, target RefreshTarget) (RefreshResult, error) {
	if target.isZero() {
		target = RefreshAll()
	}

	// Bootstrap metadata tables if needed.
	if err := m.bootstrap(ctx); err != nil {
		return RefreshResult{}, err
	}

	var result RefreshResult
	err := m.withLockSession(ctx, func(ctx context.Context, conn *sql.Conn, caps *SessionCapabilities) error {
		plan, err := m.buildRefreshPlan(ctx, conn, caps, target)
		if err != nil {
			return err
		}

		r, err := m.executeRefresh(ctx, conn, caps, plan)
		if err != nil {
			result = r
			return err
		}
		result = r
		return nil
	})
	if err != nil {
		return result, err
	}
	return result, nil
}

// PreviewRefresh returns a read-only plan of a refresh operation.
func (m *Migrator) PreviewRefresh(ctx context.Context, target RefreshTarget) (RefreshPlanView, error) {
	if target.isZero() {
		target = RefreshAll()
	}

	var view RefreshPlanView
	err := m.withLockSession(ctx, func(ctx context.Context, conn *sql.Conn, caps *SessionCapabilities) error {
		if err := m.bootstrap(ctx); err != nil {
			return err
		}

		plan, err := m.buildRefreshPlan(ctx, conn, caps, target)
		if err != nil {
			return err
		}
		view = plan.toRefreshPlanView(m.directory, m.tableName, true)
		return nil
	})
	if err != nil {
		return RefreshPlanView{}, err
	}
	return view, nil
}

// Compile-time interface check (ensures Refresh/PreviewRefresh are defined).
var _ interface {
	Refresh(ctx context.Context, target RefreshTarget) (RefreshResult, error)
	PreviewRefresh(ctx context.Context, target RefreshTarget) (RefreshPlanView, error)
} = (*Migrator)(nil)

// Ensure executeUp is referenced to avoid unused import in test builds.
var _ = fmt.Sprintf
