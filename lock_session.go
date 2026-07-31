package lamigrate

import (
	"context"
	"database/sql"
	"fmt"
)

// withLockSession provides the complete lock lifecycle for every
// database-dependent command (architecture §10.2). It:
//
//  1. Creates a private session (fresh connector, pool, connection)
//  2. Runs capability probes and captures database name + connection ID
//  3. Validates the database and tracking-table name domains
//  4. Derives the canonical lock key
//  5. Acquires the advisory lock with the configured timeout
//  6. Calls the operation function with the held connection
//  7. Releases the lock using a fresh bounded cleanup context
//  8. Closes the session (connection + pool), always, even on error
//
// The session is never reused across phases or commands.
// Physical session termination is mandatory (architecture §10).
// WithLockSessionForTest is an exported wrapper around withLockSession
// for use in integration tests from external packages. It provides
// access to the complete lock lifecycle: create session, probe,
// derive lock, acquire, execute operation, release, close session.
func (m *Migrator) WithLockSessionForTest(
	ctx context.Context,
	operation func(ctx context.Context) error,
) error {
	return m.withLockSession(ctx, func(ctx context.Context, conn *sql.Conn, caps *SessionCapabilities) error {
		return operation(ctx)
	})
}

func (m *Migrator) withLockSession(
	ctx context.Context,
	operation func(ctx context.Context, conn *sql.Conn, caps *SessionCapabilities) error,
) error {
	// Step 1: Create a private session.
	conn, pool, err := m.newPrivateSession(ctx)
	if err != nil {
		return err
	}

	// Step 2: Run capability probes.
	caps, err := m.runCapabilityProbes(ctx, conn)
	if err != nil {
		// Must close session even if probes fail.
		_ = closeSession(conn, pool)
		return err
	}

	// Step 3: Validate database name domain (architecture §9).
	if !validDatabaseName.MatchString(caps.DatabaseName) {
		_ = closeSession(conn, pool)
		return fmt.Errorf(
			"%w: database name %q from SELECT DATABASE() does not match [A-Za-z_][A-Za-z0-9_]*",
			ErrUnsupportedDriver, caps.DatabaseName,
		)
	}

	// Step 4: Derive the canonical lock key.
	lockKey, err := deriveLockKey(caps.DatabaseName, m.tableName)
	if err != nil {
		_ = closeSession(conn, pool)
		return fmt.Errorf("derive lock key: %w", err)
	}

	// Step 5: Acquire the advisory lock.
	if err := acquireLock(ctx, conn, lockKey, m.lockTime); err != nil {
		_ = closeSession(conn, pool)
		return err
	}

	// Step 6: Execute the operation under the lock.
	opErr := operation(ctx, conn, caps)

	// Step 7: Release the lock (fresh cleanup context, independent of
	// the possibly-canceled command context).
	relErr := releaseLock(ctx, conn, lockKey)

	// Step 8: Close the session — always, mandatory (architecture §10).
	sessErr := closeSession(conn, pool)

	// Report errors in priority order:
	// 1. Operation error (most important)
	// 2. Release error (cleanup uncertain)
	// 3. Session close error
	if opErr != nil {
		return opErr
	}
	if relErr != nil {
		return relErr
	}
	if sessErr != nil {
		return fmt.Errorf("%w: %v", ErrCleanupUncertain, sessErr)
	}

	return nil
}
