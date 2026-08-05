package lamigrate

import "errors"

// Sentinel errors for typed error handling via errors.Is / errors.As.
//
// These cover the error categories defined in architecture §16.
// Callers should use errors.Is to check for specific conditions.
var (
	// ErrInvalidConfig indicates that the supplied configuration or
	// options failed validation.
	ErrInvalidConfig = errors.New("lamigrate: invalid configuration")

	// ErrLockTimeout indicates that the advisory-lock acquisition
	// timed out.
	ErrLockTimeout = errors.New("lamigrate: lock timeout")

	// ErrDirtyState indicates that a previous migration did not
	// complete cleanly and manual intervention is required.
	ErrDirtyState = errors.New("lamigrate: dirty state")

	// ErrChecksumDrift indicates that an applied migration file has
	// been modified since it was applied.
	ErrChecksumDrift = errors.New("lamigrate: checksum drift")

	// ErrUnsupportedMetadata indicates that the tracking table uses
	// an unrecognised schema version.
	ErrUnsupportedMetadata = errors.New("lamigrate: unsupported metadata schema")

	// ErrSQLExecution indicates that a migration SQL statement failed
	// during execution.
	ErrSQLExecution = errors.New("lamigrate: SQL execution failure")

	// ErrRecoveryRequired indicates that recovery requires explicit
	// operator decision (architecture §5.7).
	ErrRecoveryRequired = errors.New("lamigrate: recovery required")

	// ErrOutcomeUnknown indicates that a metadata commit succeeded but
	// the overall migration outcome cannot be determined.
	ErrOutcomeUnknown = errors.New("lamigrate: outcome unknown")

	// ErrCleanupUncertain indicates that physical-session cleanup
	// could not be completed reliably.
	ErrCleanupUncertain = errors.New("lamigrate: cleanup uncertain")

	// ErrUnsupportedDriver indicates that the supplied driver
	// configuration is not supported by the production runtime.
	ErrUnsupportedDriver = errors.New("lamigrate: unsupported driver/configuration")

	// ErrLockUncertain indicates that the advisory lock acquisition
	// or release outcome cannot be determined (NULL, error, or
	// unexpected result from GET_LOCK / RELEASE_LOCK).
	ErrLockUncertain = errors.New("lamigrate: lock outcome uncertain")

	// ErrNotImplemented indicates that the command has been planned
	// but execution is not yet implemented (LM-024).
	ErrNotImplemented = errors.New("lamigrate: not implemented")

	// ErrConfirmationRequired indicates that the operation requires
	// explicit operator confirmation (--yes) but none was provided.
	ErrConfirmationRequired = errors.New("lamigrate: confirmation required")

	// ErrRepairRejected indicates that the repair operation was
	// rejected because the migration is not in the expected dirty state.
	ErrRepairRejected = errors.New("lamigrate: repair rejected")

	// ErrMigrationNotFoundInLatestBatch indicates that the named migration
	// is not in the latest batch (for selective down by-name).
	ErrMigrationNotFoundInLatestBatch = errors.New("lamigrate: migration not found in latest batch")

	// ErrBatchNotLatest indicates that the requested batch is not the latest
	// applied batch (for selective down by-batch).
	ErrBatchNotLatest = errors.New("lamigrate: batch is not the latest")

	// ErrRefreshNothingToRollback indicates that there are no applied
	// migrations to refresh.
	ErrRefreshNothingToRollback = errors.New("lamigrate: nothing to refresh")
)
