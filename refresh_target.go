package lamigrate

import "fmt"

// RefreshTarget controls what "refresh" does.
// Exactly one of Name or Limit must be set (mutually exclusive).
type RefreshTarget struct {
	// Name rolls back ALL migrations, then re-apply only up to and including
	// the named migration.
	Name string
	// Limit is the legacy step-count or all path. Zero value means "use Limit=All".
	Limit StepLimit
}

// RefreshAll creates a RefreshTarget that rolls back all + re-applies all.
func RefreshAll() RefreshTarget {
	return RefreshTarget{Limit: All()}
}

// RefreshSteps creates a RefreshTarget that rolls back the last N applied
// migrations (globally across all batches) and re-applies them.
func RefreshSteps(n int) (RefreshTarget, error) {
	l, err := Steps(n)
	if err != nil {
		return RefreshTarget{}, err
	}
	return RefreshTarget{Limit: l}, nil
}

// RefreshToName creates a RefreshTarget that rolls back ALL migrations and
// re-applies only up to and including the named migration.
func RefreshToName(name string) (RefreshTarget, error) {
	if name == "" {
		return RefreshTarget{}, fmt.Errorf("%w: RefreshToName requires a non-empty migration name", ErrInvalidConfig)
	}
	return RefreshTarget{Name: name}, nil
}

// isZero reports whether the RefreshTarget is uninitialized.
func (rt RefreshTarget) isZero() bool {
	return rt.Name == "" && rt.Limit.IsZero()
}

// kind returns which targeting mode is active.
func (rt RefreshTarget) kind() string {
	switch {
	case rt.Name != "":
		return "name"
	default:
		return "limit"
	}
}

// RefreshResult describes the outcome of a refresh operation.
type RefreshResult struct {
	// Rollback is the result of the rollback phase.
	Rollback Result
	// Apply is the result of the forward (re-apply) phase.
	Apply Result
}

// RefreshPlanView is a read-only preview of a refresh operation.
type RefreshPlanView struct {
	Command   string   // "refresh"
	Directory string
	TableName string
	Rollback  []string // migrations to roll back (reverse chronological)
	Apply     []string // migrations to re-apply (forward chronological)
	DryRun    bool
}

// toRefreshPlanView converts internal plans to a read-only RefreshPlanView.
func toRefreshPlanView(downPlan, upPlan *MigrationPlan, dir, tableName string, dryRun bool) RefreshPlanView {
	rbNames := make([]string, len(downPlan.migrations))
	for i, m := range downPlan.migrations {
		rbNames[i] = m.name
	}
	apNames := make([]string, len(upPlan.migrations))
	for i, m := range upPlan.migrations {
		apNames[i] = m.name
	}
	return RefreshPlanView{
		Command:   "refresh",
		Directory: dir,
		TableName: tableName,
		Rollback:  rbNames,
		Apply:     apNames,
		DryRun:    dryRun,
	}
}
