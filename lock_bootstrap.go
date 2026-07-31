package lamigrate

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// bootstrapTrackingComponent is the reserved tracking-table component
// used for the database-wide bootstrap lock. It cannot be a valid
// public tracking-table name because it contains '!' and '-' characters
// outside the [a-z_][a-z0-9_]* domain (architecture §9).
const bootstrapTrackingComponent = "!lamigrate-control-bootstrap-v1!"

// bootstrapLockKey returns the database-wide advisory lock key used
// for control table initialization. It uses the reserved bootstrap
// tracking component, which is not a valid public tracking-table name
// (it contains '!' and '-' characters outside [a-z_][a-z0-9_]*).
//
// Because the bootstrap component cannot be a valid tracking-table
// name, a bootstrap lock key can never collide with a normal scope
// lock key for any valid tracking table (architecture §9).
func bootstrapLockKey(database string) (string, error) {
	if database == "" {
		return "", fmt.Errorf(
			"%w: database name must not be empty",
			ErrInvalidConfig,
		)
	}
	if len(database) > maxIdentifierLen {
		return "", fmt.Errorf(
			"%w: database name %q exceeds %d bytes",
			ErrInvalidConfig, database, maxIdentifierLen,
		)
	}
	if !validDatabaseName.MatchString(database) {
		return "", fmt.Errorf(
			"%w: database name %q must match [A-Za-z_][A-Za-z0-9_]*",
			ErrInvalidConfig, database,
		)
	}
	return computeLockKey(database, bootstrapTrackingComponent), nil
}

// acquireBootstrapLock acquires the database-wide bootstrap advisory
// lock for control table initialization.
func acquireBootstrapLock(ctx context.Context, conn *sql.Conn, database string, timeout time.Duration) error {
	key, err := bootstrapLockKey(database)
	if err != nil {
		return fmt.Errorf("derive bootstrap lock key: %w", err)
	}
	return acquireLock(ctx, conn, key, timeout)
}

// releaseBootstrapLock releases the database-wide bootstrap advisory
// lock using a fresh bounded cleanup context.
func releaseBootstrapLock(ctx context.Context, conn *sql.Conn, database string) error {
	key, err := bootstrapLockKey(database)
	if err != nil {
		return fmt.Errorf("derive bootstrap lock key: %w", err)
	}
	return releaseLock(ctx, conn, key)
}
