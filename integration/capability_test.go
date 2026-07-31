//go:build integration

package integration

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"
)

// TestCapabilityProbesAgainstRealMySQL creates an isolated test database
// and verifies all capability probe SQL patterns against MySQL 8.x.
// This proves the exact queries used by capability_probes.go work against
// a real server.
func TestCapabilityProbesAgainstRealMySQL(t *testing.T) {
	tb := newTestDB(t)
	tb.requireTestDBName()

	ctx := context.Background()

	// 1. Multi-statement execution: SELECT 1; SELECT 2;
	rows, err := tb.DB().QueryContext(ctx, "SELECT 1; SELECT 2")
	if err != nil {
		t.Fatalf("multi-statement probe failed: %v", err)
	}
	// Drain first result set.
	if !rows.Next() {
		t.Fatal("multi-statement: first result set empty")
	}
	var v1 int
	if err := rows.Scan(&v1); err != nil {
		t.Fatalf("multi-statement scan set 1: %v", err)
	}
	if v1 != 1 {
		t.Fatalf("multi-statement set 1: got %d, want 1", v1)
	}
	for rows.Next() {
		// drain
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("multi-statement rows error set 1: %v", err)
	}
	// Drain second result set.
	if !rows.NextResultSet() {
		t.Fatal("multi-statement: second result set not found")
	}
	if !rows.Next() {
		t.Fatal("multi-statement: second result set empty")
	}
	var v2 int
	if err := rows.Scan(&v2); err != nil {
		t.Fatalf("multi-statement scan set 2: %v", err)
	}
	if v2 != 2 {
		t.Fatalf("multi-statement set 2: got %d, want 2", v2)
	}
	for rows.Next() {
		// drain
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("multi-statement rows error set 2: %v", err)
	}
	_ = rows.Close()
	t.Log("multi-statement probe passed")

	// 2. CURRENT_TIMESTAMP(6) scan into time.Time.
	var ts time.Time
	if err := tb.DB().QueryRowContext(ctx, "SELECT CURRENT_TIMESTAMP(6)").Scan(&ts); err != nil {
		t.Fatalf("timestamp scan probe failed: %v", err)
	}
	if ts.IsZero() {
		t.Fatal("CURRENT_TIMESTAMP(6) returned zero time")
	}
	t.Logf("timestamp probe passed: %v", ts)

	// 3. Server version validation (must contain "8.").
	var version string
	if err := tb.DB().QueryRowContext(ctx, "SELECT VERSION()").Scan(&version); err != nil {
		t.Fatalf("version probe failed: %v", err)
	}
	if !strings.Contains(version, "8.") {
		t.Fatalf("unsupported MySQL version: %q (must contain \"8.\")", version)
	}
	t.Logf("server version probe passed: %s", version)

	// 4. SELECT DATABASE().
	var dbName sql.NullString
	if err := tb.DB().QueryRowContext(ctx, "SELECT DATABASE()").Scan(&dbName); err != nil {
		t.Fatalf("DATABASE() probe failed: %v", err)
	}
	if !dbName.Valid || dbName.String == "" {
		t.Fatal("SELECT DATABASE() returned NULL or empty")
	}
	if dbName.String != tb.Name {
		t.Fatalf("DATABASE() = %q, want %q", dbName.String, tb.Name)
	}
	t.Logf("database name probe passed: %q", dbName.String)

	// 5. Connection character set (must be utf8mb4).
	var charset string
	if err := tb.DB().QueryRowContext(ctx, "SHOW VARIABLES LIKE 'character_set_connection'").Scan(
		new(string), &charset,
	); err != nil {
		t.Fatalf("character set probe failed: %v", err)
	}
	if charset != "utf8mb4" {
		t.Fatalf("connection charset = %q, want \"utf8mb4\"", charset)
	}
	t.Logf("character set probe passed: %s", charset)

	// 6. @@lower_case_table_names (must be 0).
	var lct int
	if err := tb.DB().QueryRowContext(ctx, "SELECT @@lower_case_table_names").Scan(&lct); err != nil {
		t.Fatalf("lower_case_table_names probe failed: %v", err)
	}
	if lct != 0 {
		t.Fatalf("@@lower_case_table_names = %d, want 0", lct)
	}
	t.Logf("lower_case_table_names probe passed: %d", lct)

	// 7. @@session.autocommit (must be 1).
	var autocommit int
	if err := tb.DB().QueryRowContext(ctx, "SELECT @@session.autocommit").Scan(&autocommit); err != nil {
		t.Fatalf("autocommit probe failed: %v", err)
	}
	if autocommit != 1 {
		t.Fatalf("@@session.autocommit = %d, want 1", autocommit)
	}
	t.Logf("autocommit probe passed: %d", autocommit)

	// 8. In-transaction check (must be 0 ACTIVE transactions).
	// Uses performance_schema.events_transactions_current instead of
	// @@session.in_transaction which is not available in MySQL 8.4.
	// We check for STATE = 'ACTIVE' because the table retains COMMITTED
	// state records even after transactions complete.
	var inTx int
	inTxQuery := `SELECT COUNT(*) FROM performance_schema.events_transactions_current ` +
		`WHERE STATE = 'ACTIVE' AND THREAD_ID = (` +
		`SELECT THREAD_ID FROM performance_schema.threads ` +
		`WHERE PROCESSLIST_ID = CONNECTION_ID())`
	if err := tb.DB().QueryRowContext(ctx, inTxQuery).Scan(&inTx); err != nil {
		t.Fatalf("in_transaction probe failed: %v", err)
	}
	if inTx != 0 {
		t.Fatalf("active transaction count = %d, want 0", inTx)
	}
	t.Logf("in_transaction probe passed: %d active", inTx)

	// 9. CONNECTION_ID().
	var connID uint64
	if err := tb.DB().QueryRowContext(ctx, "SELECT CONNECTION_ID()").Scan(&connID); err != nil {
		t.Fatalf("CONNECTION_ID() probe failed: %v", err)
	}
	if connID == 0 {
		t.Fatal("CONNECTION_ID() returned 0")
	}
	t.Logf("CONNECTION_ID() probe passed: %d", connID)

	t.Log("all 9 capability probes passed against real MySQL")
}

// TestCapabilityProbesSessionIsolation creates two connections to the same
// database and verifies they get different connection IDs.
func TestCapabilityProbesSessionIsolation(t *testing.T) {
	tb := newTestDB(t)
	tb.requireTestDBName()

	ctx := context.Background()

	// Open two separate connections.
	db1, err := sql.Open("mysql", tb.DSN)
	if err != nil {
		t.Fatalf("open db1: %v", err)
	}
	defer db1.Close()
	db1.SetMaxOpenConns(1)

	db2, err := sql.Open("mysql", tb.DSN)
	if err != nil {
		t.Fatalf("open db2: %v", err)
	}
	defer db2.Close()
	db2.SetMaxOpenConns(1)

	// Get connection IDs from separate sessions.
	var id1, id2 uint64
	if err := db1.QueryRowContext(ctx, "SELECT CONNECTION_ID()").Scan(&id1); err != nil {
		t.Fatalf("CONNECTION_ID 1: %v", err)
	}
	if err := db2.QueryRowContext(ctx, "SELECT CONNECTION_ID()").Scan(&id2); err != nil {
		t.Fatalf("CONNECTION_ID 2: %v", err)
	}

	if id1 == id2 {
		t.Fatalf("expected different connection IDs, both got %d", id1)
	}
	t.Logf("session isolation verified: id1=%d id2=%d", id1, id2)
}

// TestCapabilityProbeFailureMessages verifies that probe SQL failures
// produce descriptive error messages.
func TestCapabilityProbeFailureMessages(t *testing.T) {
	// Connect to a non-existent server to test error handling.
	badDSN := "nonexistent:nonexistent@tcp(192.0.2.1:12345)/nodb?timeout=2s"
	db, err := sql.Open("mysql", badDSN)
	if err != nil {
		t.Fatalf("sql.Open should not fail (lazy): %v", err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	// Any query should fail with a connection error.
	err = db.QueryRowContext(ctx, "SELECT 1").Scan(new(int))
	if err == nil {
		t.Fatal("expected error querying non-existent server, got nil")
	}
	t.Logf("bad server correctly produces error: %v", err)

	// Try capability probe SQL — should also fail.
	err = db.QueryRowContext(ctx, "SELECT 1; SELECT 2").Scan(new(int))
	if err == nil {
		t.Fatal("expected error from multi-statement on non-existent server")
	}
	t.Logf("multi-statement probe correctly fails on bad server: %v", err)
}
