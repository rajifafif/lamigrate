package lamigrate

import (
	"context"
	"database/sql"
)

// test_helpers.go — Exported test-only accessors for internal functions.
//
// These functions provide test access to internal metadata operations
// from external integration test packages. They are NOT part of the
// production API.

// BootstrapForTest exposes the internal bootstrap protocol for testing.
// It performs the two-phase bootstrap: create sessions, acquire locks,
// inventory tables, create/validate if needed, initialize control row.
func (m *Migrator) BootstrapForTest(ctx context.Context) error {
	return m.bootstrap(ctx)
}

// ValidateTableShapeForTest exposes the internal table-shape validation
// for testing. It checks columns, types, keys, constraints, engine,
// charset, and rejects extra columns, triggers, foreign keys, partitions.
func ValidateTableShapeForTest(ctx context.Context, conn *sql.Conn, database, tableName, tableType string) error {
	return validateTableShape(ctx, conn, database, tableName, tableType)
}

// AllocateBatchForTest exposes the internal batch allocation for testing.
// It locks the control row, reads next_batch, increments, and returns
// the allocated batch number.
func AllocateBatchForTest(ctx context.Context, conn *sql.Conn, tableName string) (uint64, error) {
	return allocateBatch(ctx, conn, tableName)
}

// ChecksumBytesForTest exposes the internal checksumBytes function for
// testing. It returns the SHA-256 digest of the given byte slice.
func ChecksumBytesForTest(data []byte) [32]byte {
	return checksumBytes(data)
}
