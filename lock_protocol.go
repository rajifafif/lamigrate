package lamigrate

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"time"
)

// acquireLock acquires the MySQL advisory lock on the given dedicated
// connection using GET_LOCK(lockKey, timeoutSeconds).
//
// Returns:
//   - nil on successful acquisition (GET_LOCK returns 1)
//   - ErrLockTimeout when GET_LOCK returns 0 (timeout expired)
//   - ErrLockUncertain on NULL, query error, cancellation, or
//     unreceived result
//
// The timeout is rounded up to whole seconds for GET_LOCK per
// architecture §10.2. A zero timeout means do not wait. The effective
// wait is bounded by the earlier of the timeout and the caller context
// deadline.
func acquireLock(ctx context.Context, conn *sql.Conn, lockKey string, timeout time.Duration) error {
	// Clamp to max 24 hours per architecture §10.2.
	if timeout < 0 {
		return fmt.Errorf(
			"%w: lock timeout must not be negative",
			ErrInvalidConfig,
		)
	}
	if timeout > maxLockTimeout {
		return fmt.Errorf(
			"%w: lock timeout %v exceeds maximum %v",
			ErrInvalidConfig, timeout, maxLockTimeout,
		)
	}

	// Round up to whole seconds for GET_LOCK.
	timeoutSeconds := int64(math.Ceil(timeout.Seconds()))
	if timeoutSeconds > 24*3600 {
		timeoutSeconds = 24 * 3600
	}

	var result sql.NullInt64
	err := conn.QueryRowContext(ctx, "SELECT GET_LOCK(?, ?)", lockKey, timeoutSeconds).Scan(&result)
	if err != nil {
		return fmt.Errorf(
			"%w: GET_LOCK(%q) query failed: %v",
			ErrLockUncertain, lockKey, err,
		)
	}

	if !result.Valid {
		// NULL returned — lock acquisition failed for an internal reason.
		return fmt.Errorf(
			"%w: GET_LOCK(%q) returned NULL",
			ErrLockUncertain, lockKey,
		)
	}

	switch result.Int64 {
	case 1:
		// Successfully acquired.
		return nil
	case 0:
		// Timeout — lock was not acquired.
		return fmt.Errorf(
			"%w: GET_LOCK(%q) timed out after %v",
			ErrLockTimeout, lockKey, timeout,
		)
	default:
		return fmt.Errorf(
			"%w: GET_LOCK(%q) returned unexpected value %d",
			ErrLockUncertain, lockKey, result.Int64,
		)
	}
}

// verifyLockOwnership checks that the advisory lock identified by
// lockKey is held by this connection by verifying
// IS_USED_LOCK(lockKey) = CONNECTION_ID().
//
// Returns nil if ownership is confirmed, or ErrLockUncertain if the
// lock is not held, is held by a different connection, or the query
// fails.
func verifyLockOwnership(ctx context.Context, conn *sql.Conn, lockKey string) error {
	// Get the connection ID for this session.
	var connID uint64
	if err := conn.QueryRowContext(ctx, "SELECT CONNECTION_ID()").Scan(&connID); err != nil {
		return fmt.Errorf(
			"%w: cannot read CONNECTION_ID() for ownership check: %v",
			ErrLockUncertain, err,
		)
	}

	// Check IS_USED_LOCK — returns the connection ID holding the lock,
	// or NULL if not held.
	var holder sql.NullInt64
	if err := conn.QueryRowContext(ctx, "SELECT IS_USED_LOCK(?)", lockKey).Scan(&holder); err != nil {
		return fmt.Errorf(
			"%w: IS_USED_LOCK(%q) query failed: %v",
			ErrLockUncertain, lockKey, err,
		)
	}

	if !holder.Valid {
		return fmt.Errorf(
			"%w: IS_USED_LOCK(%q) returned NULL — lock not held",
			ErrLockUncertain, lockKey,
		)
	}

	if uint64(holder.Int64) != connID {
		return fmt.Errorf(
			"%w: IS_USED_LOCK(%q) = %d, expected CONNECTION_ID() = %d",
			ErrLockUncertain, lockKey, holder.Int64, connID,
		)
	}

	return nil
}

// releaseLock releases the MySQL advisory lock using a fresh bounded
// cleanup context independent of the possibly-canceled command context.
//
// Returns nil only when RELEASE_LOCK returns exactly 1 (successful
// release). Any other result (0 = lock not held by this connection,
// NULL = lock did not exist, or query error) returns an error.
//
// The cleanup context is bounded to 10 seconds per architecture §10.2.
func releaseLock(ctx context.Context, conn *sql.Conn, lockKey string) error {
	// Fresh bounded cleanup context — independent of the command context
	// which may be canceled (architecture §10.2, §11).
	cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var result sql.NullInt64
	err := conn.QueryRowContext(cleanupCtx, "SELECT RELEASE_LOCK(?)", lockKey).Scan(&result)
	if err != nil {
		return fmt.Errorf(
			"%w: RELEASE_LOCK(%q) query failed: %v",
			ErrCleanupUncertain, lockKey, err,
		)
	}

	if !result.Valid {
		return fmt.Errorf(
			"%w: RELEASE_LOCK(%q) returned NULL",
			ErrCleanupUncertain, lockKey,
		)
	}

	if result.Int64 != 1 {
		return fmt.Errorf(
			"%w: RELEASE_LOCK(%q) returned %d (expected 1)",
			ErrCleanupUncertain, lockKey, result.Int64,
		)
	}

	return nil
}
