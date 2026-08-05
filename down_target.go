package lamigrate

import "fmt"

// DownTarget controls what "down" selects for rollback.
// Exactly one of Name, Batch, or Limit must be set (mutually exclusive).
type DownTarget struct {
	// Name selects rollback to-and-including this migration in the latest batch.
	Name string
	// Batch selects rollback of all migrations in this batch (must == latestBatch).
	Batch int
	// Limit is the legacy step-count or all path. Zero value means "use Limit=All".
	Limit StepLimit
}

// DownToName creates a DownTarget that rolls back the named migration and
// everything newer in the latest batch.
func DownToName(name string) (DownTarget, error) {
	if name == "" {
		return DownTarget{}, fmt.Errorf("%w: DownToName requires a non-empty migration name", ErrInvalidConfig)
	}
	return DownTarget{Name: name}, nil
}

// DownToBatch creates a DownTarget that rolls back all migrations in the
// given batch number. The batch must be the latest applied batch.
func DownToBatch(batch int) (DownTarget, error) {
	if batch <= 0 {
		return DownTarget{}, fmt.Errorf("%w: DownToBatch requires a positive batch number, got %d", ErrInvalidConfig, batch)
	}
	return DownTarget{Batch: batch}, nil
}

// DownAll creates a DownTarget that rolls back all migrations in the latest batch.
func DownAll() DownTarget {
	return DownTarget{Limit: All()}
}

// DownSteps creates a DownTarget that rolls back at most N migrations from the
// latest batch.
func DownSteps(n int) (DownTarget, error) {
	l, err := Steps(n)
	if err != nil {
		return DownTarget{}, err
	}
	return DownTarget{Limit: l}, nil
}

// isZero reports whether the DownTarget is uninitialized.
func (dt DownTarget) isZero() bool {
	return dt.Name == "" && dt.Batch == 0 && dt.Limit.IsZero()
}

// kind returns which targeting mode is active.
func (dt DownTarget) kind() string {
	switch {
	case dt.Name != "":
		return "name"
	case dt.Batch != 0:
		return "batch"
	default:
		return "limit"
	}
}
