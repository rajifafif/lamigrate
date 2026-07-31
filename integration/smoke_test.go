//go:build integration

package integration

import (
	"strings"
	"testing"
)

func TestSmokeConnectAndVerify(t *testing.T) {
	tb := newTestDB(t)

	// Safety: ensure the harness won't operate on a real DB
	tb.requireTestDBName()

	// --- CREATE TABLE ---
	if _, err := tb.Exec("CREATE TABLE smoke_test (id INT UNSIGNED AUTO_INCREMENT PRIMARY KEY, label VARCHAR(64) NOT NULL)"); err != nil {
		t.Fatalf("CREATE TABLE failed: %v", err)
	}

	// --- INSERT + SELECT ---
	if _, err := tb.Exec("INSERT INTO smoke_test (label) VALUES (?)", "hello"); err != nil {
		t.Fatalf("INSERT failed: %v", err)
	}

	var label string
	if err := tb.QueryRow("SELECT label FROM smoke_test WHERE id = 1").Scan(&label); err != nil {
		t.Fatalf("SELECT failed: %v", err)
	}
	if label != "hello" {
		t.Fatalf("expected label=hello, got %q", label)
	}

	// --- SELECT DATABASE() ---
	var currentDB string
	if err := tb.QueryRow("SELECT DATABASE()").Scan(&currentDB); err != nil {
		t.Fatalf("SELECT DATABASE() failed: %v", err)
	}
	if !strings.HasPrefix(currentDB, "lamigrate_test_") {
		t.Fatalf("unexpected database name %q (missing lamigrate_test_ prefix)", currentDB)
	}
	t.Logf("connected to database: %s", currentDB)

	// --- SELECT VERSION() ---
	var version string
	if err := tb.QueryRow("SELECT VERSION()").Scan(&version); err != nil {
		t.Fatalf("SELECT VERSION() failed: %v", err)
	}
	if version == "" {
		t.Fatal("SELECT VERSION() returned empty string")
	}
	t.Logf("MySQL version: %s", version)

	// --- DROP TABLE ---
	if _, err := tb.Exec("DROP TABLE smoke_test"); err != nil {
		t.Fatalf("DROP TABLE failed: %v", err)
	}

	// Verify table is gone
	var count int
	err := tb.QueryRow("SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = ? AND table_name = ?", currentDB, "smoke_test").Scan(&count)
	if err != nil {
		t.Fatalf("information_schema query failed: %v", err)
	}
	if count != 0 {
		t.Fatalf("table still exists after DROP (information_schema count=%d)", count)
	}

	t.Log("smoke test passed: connect, create, insert, select, drop, cleanup")
}
