//go:build integration

package integration

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rajifafif/lamigrate"
)

// TestCreateAndExecuteTemplate creates a migration via CreateMigration,
// executes the up SQL in MySQL via the harness, then executes the down SQL,
// verifying the table exists after up and doesn't after down.
func TestCreateAndExecuteTemplate(t *testing.T) {
	tb := newTestDB(t)
	tb.requireTestDBName()

	ctx := context.Background()

	// Create a migration in a temporary directory.
	dir := filepath.Join(t.TempDir(), "migrations")
	created, err := lamigrate.CreateMigration(dir, "create_template_test_table")
	if err != nil {
		t.Fatalf("CreateMigration() error = %v", err)
	}

	t.Logf("created up:   %s", created.UpPath)
	t.Logf("created down: %s", created.DownPath)
	t.Logf("template:     %s", created.Template)

	// Verify template is create_table (immediately runnable, no SIGNAL guard).
	if created.Template != "create_table" {
		t.Fatalf("expected create_table template, got %q", created.Template)
	}

	// The regex strips the _table suffix, so the actual table name is "template_test".
	const tableName = "template_test"

	// Read the up SQL.
	upSQL, err := os.ReadFile(created.UpPath)
	if err != nil {
		t.Fatalf("read up migration: %v", err)
	}
	upSQLStr := strings.TrimSpace(string(upSQL))

	// Read the down SQL.
	downSQL, err := os.ReadFile(created.DownPath)
	if err != nil {
		t.Fatalf("read down migration: %v", err)
	}
	downSQLStr := strings.TrimSpace(string(downSQL))

	// --- Execute UP ---
	if _, err := tb.Exec(upSQLStr); err != nil {
		t.Fatalf("execute up migration: %v", err)
	}

	// Verify the table exists.
	var count int
	if err := tb.QueryRow(
		"SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = ? AND table_name = ?",
		tb.Name, tableName,
	).Scan(&count); err != nil {
		t.Fatalf("information_schema query failed: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected %s to exist after up (count=%d)", tableName, count)
	}
	t.Log("table exists after up migration — verified")

	// --- Execute DOWN ---
	if _, err := tb.Exec(downSQLStr); err != nil {
		t.Fatalf("execute down migration: %v", err)
	}

	// Verify the table no longer exists.
	if err := tb.QueryRow(
		"SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = ? AND table_name = ?",
		tb.Name, tableName,
	).Scan(&count); err != nil {
		t.Fatalf("information_schema query failed: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected %s to be gone after down (count=%d)", tableName, count)
	}
	t.Log("table dropped after down migration — verified")

	// --- Verify via Migrate.Up + Migrate.Down round-trip ---
	// Re-create the table using the Migrate struct to exercise the library API.
	m, err := lamigrate.New(dir, tb.DSN)
	if err != nil {
		t.Fatalf("lamigrate.New() error = %v", err)
	}
	defer m.Close()

	if err := m.Up(ctx); err != nil {
		t.Fatalf("m.Up() error = %v", err)
	}

	// Verify table exists.
	if err := tb.QueryRow(
		"SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = ? AND table_name = ?",
		tb.Name, tableName,
	).Scan(&count); err != nil {
		t.Fatalf("information_schema query failed: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected %s after m.Up (count=%d)", tableName, count)
	}
	fmt.Println("m.Up() applied migration successfully")

	// Rollback with m.Down().
	if err := m.Down(ctx); err != nil {
		t.Fatalf("m.Down() error = %v", err)
	}

	// Verify table is gone.
	if err := tb.QueryRow(
		"SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = ? AND table_name = ?",
		tb.Name, tableName,
	).Scan(&count); err != nil {
		t.Fatalf("information_schema query failed: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected %s gone after m.Down (count=%d)", tableName, count)
	}
	fmt.Println("m.Down() rolled back migration successfully")

	t.Log("full up/down round-trip via library API verified")
}
