package lamigrate

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
)

// runCapabilityProbes executes all required pre-mutation probes on a
// private dedicated connection. Every probe must succeed or the function
// returns ErrUnsupportedDriver with a detailed message explaining which
// probe failed and why.
//
// Probes (architecture §7):
//   - Multi-statement execution: SELECT 1; SELECT 2; (drain both result sets)
//   - CURRENT_TIMESTAMP(6) scan into time.Time
//   - Server version validation (must contain "8.")
//   - SELECT DATABASE() — capture and validate
//   - Connection character set validation (must be utf8mb4)
//   - @@lower_case_table_names (must be 0)
//   - @@session.autocommit (must be 1)
//   - @@session.in_transaction (must be 0)
//   - CONNECTION_ID() — capture for lock verification
//
// No metadata changes are made. The function is side-effect-free.
func (m *Migrator) runCapabilityProbes(ctx context.Context, conn *sql.Conn) (*SessionCapabilities, error) {
	// 1. Multi-statement execution: SELECT 1; SELECT 2;
	// Both result sets must be drained to verify multi-statement support.
	if err := probeMultiStatement(ctx, conn); err != nil {
		return nil, err
	}

	// 2. CURRENT_TIMESTAMP(6) scan into time.Time
	if err := probeTimestampScan(ctx, conn); err != nil {
		return nil, err
	}

	// 3. Server version validation (must contain "8.")
	if err := probeServerVersion(ctx, conn); err != nil {
		return nil, err
	}

	// 4. SELECT DATABASE() — capture and validate
	dbName, err := probeDatabaseName(ctx, conn)
	if err != nil {
		return nil, err
	}

	// 5. Connection character set validation (must be utf8mb4)
	if err := probeCharacterSet(ctx, conn); err != nil {
		return nil, err
	}

	// 6. @@lower_case_table_names (must be 0)
	if err := probeLowerCaseTableNames(ctx, conn); err != nil {
		return nil, err
	}

	// 7. @@session.autocommit (must be 1)
	if err := probeAutocommit(ctx, conn); err != nil {
		return nil, err
	}

	// 8. @@session.in_transaction (must be 0)
	if err := probeInTransaction(ctx, conn); err != nil {
		return nil, err
	}

	// 9. CONNECTION_ID() — capture for lock verification
	connID, err := probeConnectionID(ctx, conn)
	if err != nil {
		return nil, err
	}

	return &SessionCapabilities{
		DatabaseName: dbName,
		ConnectionID: connID,
	}, nil
}

// probeMultiStatement verifies multi-statement support by executing
// two SELECT statements in one call and draining both result sets.
func probeMultiStatement(ctx context.Context, conn *sql.Conn) error {
	rows, err := conn.QueryContext(ctx, "SELECT 1; SELECT 2")
	if err != nil {
		return fmt.Errorf(
			"%w: multi-statement probe failed (SELECT 1; SELECT 2): %v",
			ErrUnsupportedDriver, err,
		)
	}
	// NOTE: rows must NOT be closed between result sets.
	// NextResultSet requires the *sql.Rows to remain open.
	defer rows.Close()

	// --- Drain first result set (expect value 1) ---
	for rows.Next() {
		var v int
		if err := rows.Scan(&v); err != nil {
			return fmt.Errorf(
				"%w: multi-statement probe scan failed (result set 1): %v",
				ErrUnsupportedDriver, err,
			)
		}
		if v != 1 {
			return fmt.Errorf(
				"%w: multi-statement probe unexpected value (result set 1): got %d, want 1",
				ErrUnsupportedDriver, v,
			)
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf(
			"%w: multi-statement probe rows error (result set 1): %v",
			ErrUnsupportedDriver, err,
		)
	}

	// --- Drain second result set (expect value 2) ---
	if !rows.NextResultSet() {
		return fmt.Errorf(
			"%w: multi-statement probe: second result set not found",
			ErrUnsupportedDriver,
		)
	}
	for rows.Next() {
		var v int
		if err := rows.Scan(&v); err != nil {
			return fmt.Errorf(
				"%w: multi-statement probe scan failed (result set 2): %v",
				ErrUnsupportedDriver, err,
			)
		}
		if v != 2 {
			return fmt.Errorf(
				"%w: multi-statement probe unexpected value (result set 2): got %d, want 2",
				ErrUnsupportedDriver, v,
			)
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf(
			"%w: multi-statement probe rows error (result set 2): %v",
			ErrUnsupportedDriver, err,
		)
	}
	return nil
}

// probeTimestampScan verifies CURRENT_TIMESTAMP(6) can be scanned into
// a Go time.Time. This confirms ParseTime is working correctly.
func probeTimestampScan(ctx context.Context, conn *sql.Conn) error {
	var ts time.Time
	if err := conn.QueryRowContext(ctx, "SELECT CURRENT_TIMESTAMP(6)").Scan(&ts); err != nil {
		return fmt.Errorf(
			"%w: CURRENT_TIMESTAMP(6) scan probe failed: %v",
			ErrUnsupportedDriver, err,
		)
	}
	if ts.IsZero() {
		return fmt.Errorf(
			"%w: CURRENT_TIMESTAMP(6) returned zero time",
			ErrUnsupportedDriver,
		)
	}
	return nil
}

// probeServerVersion validates the MySQL server version contains "8."
// to ensure we are running against MySQL 8.x.
func probeServerVersion(ctx context.Context, conn *sql.Conn) error {
	var version string
	if err := conn.QueryRowContext(ctx, "SELECT VERSION()").Scan(&version); err != nil {
		return fmt.Errorf(
			"%w: server version probe failed: %v",
			ErrUnsupportedDriver, err,
		)
	}
	if !strings.Contains(version, "8.") {
		return fmt.Errorf(
			"%w: unsupported MySQL version %q (must contain \"8.\")",
			ErrUnsupportedDriver, version,
		)
	}
	return nil
}

// probeDatabaseName captures the selected database via SELECT DATABASE()
// and validates it is non-empty and ASCII.
func probeDatabaseName(ctx context.Context, conn *sql.Conn) (string, error) {
	var dbName sql.NullString
	if err := conn.QueryRowContext(ctx, "SELECT DATABASE()").Scan(&dbName); err != nil {
		return "", fmt.Errorf(
			"%w: SELECT DATABASE() probe failed: %v",
			ErrUnsupportedDriver, err,
		)
	}
	if !dbName.Valid || dbName.String == "" {
		return "", fmt.Errorf(
			"%w: no database selected (SELECT DATABASE() returned NULL or empty)",
			ErrUnsupportedDriver,
		)
	}
	// Validate ASCII per architecture §9.
	for _, c := range dbName.String {
		if c > 127 {
			return "", fmt.Errorf(
				"%w: database name %q contains non-ASCII characters",
				ErrUnsupportedDriver, dbName.String,
			)
		}
	}
	return dbName.String, nil
}

// probeCharacterSet validates the connection character set is utf8mb4.
func probeCharacterSet(ctx context.Context, conn *sql.Conn) error {
	var charset string
	if err := conn.QueryRowContext(ctx, "SHOW VARIABLES LIKE 'character_set_connection'").Scan(
		new(string), &charset,
	); err != nil {
		return fmt.Errorf(
			"%w: connection character set probe failed: %v",
			ErrUnsupportedDriver, err,
		)
	}
	if charset != "utf8mb4" {
		return fmt.Errorf(
			"%w: connection character set is %q, want \"utf8mb4\"",
			ErrUnsupportedDriver, charset,
		)
	}
	return nil
}

// probeLowerCaseTableNames validates @@lower_case_table_names is 0.
// Only the case-sensitive mode (0) is supported by lock protocol v1
// because it preserves case-sensitive physical database identity.
func probeLowerCaseTableNames(ctx context.Context, conn *sql.Conn) error {
	var val int
	if err := conn.QueryRowContext(ctx, "SELECT @@lower_case_table_names").Scan(&val); err != nil {
		return fmt.Errorf(
			"%w: @@lower_case_table_names probe failed: %v",
			ErrUnsupportedDriver, err,
		)
	}
	if val != 0 {
		return fmt.Errorf(
			"%w: @@lower_case_table_names = %d, want 0 (case-sensitive mode required)",
			ErrUnsupportedDriver, val,
		)
	}
	return nil
}

// probeAutocommit validates @@session.autocommit is 1 (enabled).
func probeAutocommit(ctx context.Context, conn *sql.Conn) error {
	var val int
	if err := conn.QueryRowContext(ctx, "SELECT @@session.autocommit").Scan(&val); err != nil {
		return fmt.Errorf(
			"%w: @@session.autocommit probe failed: %v",
			ErrUnsupportedDriver, err,
		)
	}
	if val != 1 {
		return fmt.Errorf(
			"%w: @@session.autocommit = %d, want 1",
			ErrUnsupportedDriver, val,
		)
	}
	return nil
}

// probeInTransaction validates that no active transaction exists on the
// new session. It queries performance_schema.events_transactions_current
// which returns a row with STATE='ACTIVE' only when a transaction is in
// progress. A fresh session should have zero matching rows.
func probeInTransaction(ctx context.Context, conn *sql.Conn) error {
	var activeCount int
	query := `SELECT COUNT(*) FROM performance_schema.events_transactions_current ` +
		`WHERE STATE = 'ACTIVE' AND THREAD_ID = (` +
		`SELECT THREAD_ID FROM performance_schema.threads ` +
		`WHERE PROCESSLIST_ID = CONNECTION_ID())`
	if err := conn.QueryRowContext(ctx, query).Scan(&activeCount); err != nil {
		return fmt.Errorf(
			"%w: in_transaction probe failed (performance_schema): %v",
			ErrUnsupportedDriver, err,
		)
	}
	if activeCount != 0 {
		return fmt.Errorf(
			"%w: active transaction detected on new session (count=%d, want 0)",
			ErrUnsupportedDriver, activeCount,
		)
	}
	return nil
}

// probeConnectionID captures the MySQL CONNECTION_ID() for lock
// verification and session tracking.
func probeConnectionID(ctx context.Context, conn *sql.Conn) (uint64, error) {
	var connID uint64
	if err := conn.QueryRowContext(ctx, "SELECT CONNECTION_ID()").Scan(&connID); err != nil {
		return 0, fmt.Errorf(
			"%w: CONNECTION_ID() probe failed: %v",
			ErrUnsupportedDriver, err,
		)
	}
	if connID == 0 {
		return 0, fmt.Errorf(
			"%w: CONNECTION_ID() returned 0",
			ErrUnsupportedDriver,
		)
	}
	return connID, nil
}
