// Package lamigrate provides Laravel-style database migrations for Go + MySQL.
//
// Deprecated: Use [NewMySQL] or [OpenMySQL] to construct a [Migrator] instead.
// The legacy [New] and [Migrate] type are retained for backward compatibility
// and will be removed in a future release.
package lamigrate

import (
	"fmt"
	"time"

	"github.com/go-sql-driver/mysql"
)

// ---------- Configuration ----------

// Options configures a [Migrator]. All fields are validated at construction
// time by [NewMySQL] and [OpenMySQL].
type Options struct {
	// Directory is the path to the migration files directory. Required.
	Directory string

	// LegacyDir is the path for golang-migrate numbered import files.
	// Optional.
	LegacyDir string

	// TableName is the name of the tracking table.
	// Default "migrations". Lowercase ASCII only: [a-z][a-z0-9_]*.
	TableName string

	// LockTimeout is the advisory-lock timeout.
	// Default 30s. Must not exceed 24h.
	LockTimeout time.Duration

	// MaxFileSize is the maximum migration file size in bytes.
	// Default 1 MB.
	MaxFileSize int64

	// IgnoreMissingSource relaxes the global integrity check so that an
	// applied migration whose source file no longer exists in the
	// directory ("orphaned" migration, reported as MISSING_SOURCE) does not
	// block up/down/reset. Useful in a shared-database workflow where each
	// developer applies their feature-branch migrations to a common DB and
	// those source files later disappear from the trunk. When set, the
	// missing-source check is skipped but ALL other safety checks remain:
	// dirty states still block, and any source that IS present and has a
	// different checksum is still a drift error. Orphaned metadata rows are
	// left in place (not deleted). Default false.
	IgnoreMissingSource bool
}

// ---------- Step limiting ----------

const (
	stepKindNone  = 0 // zero value — must be rejected
	stepKindAll   = 1 // every eligible migration
	stepKindSteps = 2 // at most n migrations
)

// StepLimit controls how many migrations a command processes.
// A zero [StepLimit] is invalid and must be rejected before any I/O.
type StepLimit struct {
	kind int
	n    int
}

// All returns a StepLimit that selects every eligible migration.
func All() StepLimit {
	return StepLimit{kind: stepKindAll}
}

// Steps returns a StepLimit that selects at most n migrations.
// It rejects n <= 0.
func Steps(n int) (StepLimit, error) {
	if n <= 0 {
		return StepLimit{}, fmt.Errorf("lamigrate: Steps(%d): n must be positive", n)
	}
	return StepLimit{kind: stepKindSteps, n: n}, nil
}

// IsZero reports whether sl is the zero (uninitialized) value.
// Every public method that accepts a StepLimit rejects zero values.
func (sl StepLimit) IsZero() bool {
	return sl.kind == stepKindNone
}

// isAll reports whether this limit means "all".
func (sl StepLimit) isAll() bool {
	return sl.kind == stepKindAll
}

// count returns the step count, or -1 for "all".
func (sl StepLimit) count() int {
	if sl.kind == stepKindAll {
		return -1
	}
	return sl.n
}

// ---------- Result types ----------

// Result describes the outcome of a migration operation.
type Result struct {
	// Command is the operation name: "up", "down", "reset", etc.
	Command string

	// Migrated lists each migration that was processed.
	Migrated []MigrationResult

	// Errors lists any migrations that failed.
	Errors []MigrationError
}

// MigrationResult describes one migration that was processed.
type MigrationResult struct {
	Name      string
	Direction string // "up" or "down"
	Batch     int
	Applied   bool
	Duration  time.Duration
}

// MigrationError describes a migration that failed.
type MigrationError struct {
	Name    string
	Error   error
	Partial bool // true if some statements succeeded before failure
}

// ---------- Status types ----------

// StatusReport is the full status output.
type StatusReport struct {
	Migrations []MigrationStatusDetail
}

// MigrationStatusDetail describes one migration's status.
type MigrationStatusDetail struct {
	Name       string
	Filename   string
	Status     string // "pending", "applied", "baseline", "dirty"
	Batch      int
	AppliedAt  string
	UpChecksum string // hex
	Drift      bool
}

// ---------- Plan view types ----------

// PlanView is a read-only view of a migration plan.
// Internal executable plans are unexported and cannot be mutated.
type PlanView struct {
	Command   string
	Directory string
	TableName string
	// Migrations is the list of migration names in execution order.
	Migrations []string
	// DryRun indicates this is a preview, not an execution.
	DryRun bool
	// Batch is the allocated batch number for up plans.
	// 0 means no batch was allocated (preview or empty plan).
	Batch int
}

// ---------- Migrator ----------

// Migrator is the main entry point for production migration operations.
// Construct one with [NewMySQL] or [OpenMySQL]. The zero value is not usable.
//
// Migrator methods do not write to stdout or stderr; results are returned
// as structured types.
type Migrator struct {
	dsn       string        // raw DSN for diagnostics/connection creation
	config    *mysql.Config // validated, cloned driver config
	directory string        // resolved Directory from Options
	legacyDir string        // resolved LegacyDir from Options
	tableName string        // validated table name
	lockTime  time.Duration // validated lock timeout
	maxFile   int64         // validated max file size
	// ignoreMissingSource mirrors Options.IgnoreMissingSource.
	ignoreMissingSource bool
}

// SessionCapabilities holds validated session information captured by
// pre-mutation capability probes on a private dedicated connection.
type SessionCapabilities struct {
	DatabaseName string // from SELECT DATABASE()
	ConnectionID uint64 // from CONNECTION_ID()
}

// Directory returns the configured migration directory.
func (m *Migrator) Directory() string { return m.directory }

// TableName returns the validated tracking-table name.
func (m *Migrator) TableName() string { return m.tableName }

// LockTimeout returns the configured lock timeout.
func (m *Migrator) LockTimeout() time.Duration { return m.lockTime }

// MaxFileSize returns the configured maximum file size.
func (m *Migrator) MaxFileSize() int64 { return m.maxFile }

// LegacyDir returns the configured legacy directory, or empty string.
func (m *Migrator) LegacyDir() string { return m.legacyDir }

// ---------- Internal validation ----------

// validateStepLimit rejects zero StepLimit values. This must be called
// before any I/O to satisfy architecture §7.
func validateStepLimit(sl StepLimit) error {
	if sl.IsZero() {
		return fmt.Errorf("%w: zero StepLimit is not valid", ErrInvalidConfig)
	}
	return nil
}
