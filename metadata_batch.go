package lamigrate

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// metadata_batch.go — Batch allocation for the v1 metadata model.
//
// Batch numbers are allocated by locking the relevant control row
// in a short metadata transaction, reading next_batch, and incrementing
// it before migration intent is inserted. Gaps after failures are
// acceptable; reuse is not (architecture §9.2).
//
// One successful `up` invocation allocates one batch number.
// Batch numbers are monotonic and never reused.

// allocateBatch allocates a new batch number by locking the control
// row, reading next_batch, incrementing it, and committing the new
// value. The batch number is monotonically increasing and never reused.
//
// This function expects to be called while holding the normal scope
// advisory lock on the given connection.
func allocateBatch(ctx context.Context, conn *sql.Conn, tableName string) (uint64, error) {
	var nextBatch uint64

	// Start a short metadata transaction for batch allocation.
	// Architecture §11: each metadata mutation is its own explicit transaction.
	if _, err := conn.ExecContext(ctx, "START TRANSACTION"); err != nil {
		return 0, fmt.Errorf("allocate batch: start transaction: %w", err)
	}

	// Lock the control row for this tracking table.
	// SELECT ... FOR UPDATE prevents concurrent batch allocation.
	err := conn.QueryRowContext(ctx,
		"SELECT next_batch FROM `"+controlTableName+"` WHERE tracking_table = ? FOR UPDATE",
		tableName,
	).Scan(&nextBatch)
	if err != nil {
		conn.ExecContext(ctx, "ROLLBACK") //nolint:errcheck
		return 0, fmt.Errorf("allocate batch: lock control row: %w", err)
	}

	// Compute the new batch number.
	newBatch := nextBatch

	// Update next_batch to increment.
	now := time.Now().UTC()
	_, err = conn.ExecContext(ctx,
		"UPDATE `"+controlTableName+"` SET next_batch = next_batch + 1, updated_at = ? WHERE tracking_table = ?",
		now.Format("2006-01-02 15:04:05.000000"), tableName,
	)
	if err != nil {
		conn.ExecContext(ctx, "ROLLBACK") //nolint:errcheck
		return 0, fmt.Errorf("allocate batch: update next_batch: %w", err)
	}

	// Commit the batch allocation.
	_, err = conn.ExecContext(ctx, "COMMIT")
	if err != nil {
		conn.ExecContext(ctx, "ROLLBACK") //nolint:errcheck
		return 0, fmt.Errorf("allocate batch: commit: %w", err)
	}

	// Verify the control row was updated.
	var verifyBatch uint64
	err = conn.QueryRowContext(ctx,
		"SELECT next_batch FROM `"+controlTableName+"` WHERE tracking_table = ?",
		tableName,
	).Scan(&verifyBatch)
	if err != nil {
		return 0, fmt.Errorf("allocate batch: verify: %w", err)
	}
	if verifyBatch != newBatch+1 {
		return 0, fmt.Errorf(
			"%w: batch allocation verification failed: expected next_batch=%d, got %d",
			ErrRecoveryRequired, newBatch+1, verifyBatch,
		)
	}

	return newBatch, nil
}
