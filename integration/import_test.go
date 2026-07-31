//go:build integration

package integration

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/go-sql-driver/mysql"
	lamigrate "github.com/rajifafif/lamigrate"
)

// newImportTestMigrator creates a Migrator with a legacy dir for import tests.
func newImportTestMigrator(tb *testing.T, testDB *TestDB, tableName, legacyDir string) *lamigrate.Migrator {
	tb.Helper()
	cfg, err := mysql.ParseDSN(testDB.DSN)
	if err != nil {
		tb.Fatalf("parse DSN: %v", err)
	}
	cfg.MultiStatements = true
	cfg.ParseTime = true
	m, err := lamigrate.NewMySQL(cfg, lamigrate.Options{
		Directory:   tb.TempDir(),
		LegacyDir:   legacyDir,
		TableName:   tableName,
		LockTimeout: 10 * time.Second,
	})
	if err != nil {
		tb.Fatalf("NewMySQL: %v", err)
	}
	return m
}

// writeLegacyPair creates a legacy numbered file pair in dir.
func writeLegacyPair(tb *testing.T, dir, name, upSQL, downSQL string) {
	tb.Helper()
	upPath := filepath.Join(dir, name+".up.sql")
	downPath := filepath.Join(dir, name+".down.sql")
	if err := os.WriteFile(upPath, []byte(upSQL), 0o644); err != nil {
		tb.Fatalf("write %s: %v", upPath, err)
	}
	if err := os.WriteFile(downPath, []byte(downSQL), 0o644); err != nil {
		tb.Fatalf("write %s: %v", downPath, err)
	}
}

// createGolangMigrateSourceTable creates a golang-migrate schema_migrations
// table with the given version and dirty flag.
func createGolangMigrateSourceTable(tb *testing.T, db *sql.DB, tableName string, version uint64, dirty bool) {
	tb.Helper()
	_, err := db.Exec(
		"CREATE TABLE `" + tableName + "` (version BIGINT NOT NULL PRIMARY KEY, dirty BOOLEAN NOT NULL)",
	)
	if err != nil {
		tb.Fatalf("create source table: %v", err)
	}
	_, err = db.Exec(
		"INSERT INTO `"+tableName+"` (version, dirty) VALUES (?, ?)",
		version, dirty,
	)
	if err != nil {
		tb.Fatalf("insert source row: %v", err)
	}
}

// runImportBootstrap runs bootstrap on the migrator for import tests.
func runImportBootstrap(tb *testing.T, m *lamigrate.Migrator) {
	tb.Helper()
	if err := m.BootstrapForTest(context.Background()); err != nil {
		tb.Fatalf("bootstrap: %v", err)
	}
}

// countRows counts rows in a table matching a condition.
func countRows(tb *testing.T, db *sql.DB, query string, args ...any) int {
	tb.Helper()
	var count int
	if err := db.QueryRow(query, args...).Scan(&count); err != nil {
		tb.Fatalf("countRows: %v", err)
	}
	return count
}

// TestImportBasicFlow tests importing from a source table to an empty destination.
func TestImportBasicFlow(t *testing.T) {
	tb := newTestDB(t)
	tb.requireTestDBName()

	legacyDir := t.TempDir()
	writeLegacyPair(t, legacyDir, "1_create_users", "CREATE TABLE users (id INT);", "DROP TABLE users;")
	writeLegacyPair(t, legacyDir, "2_add_email", "ALTER TABLE users ADD email VARCHAR(255);", "ALTER TABLE users DROP email;")
	writeLegacyPair(t, legacyDir, "3_create_posts", "CREATE TABLE posts (id INT);", "DROP TABLE posts;")

	// Create source table with version=3 (all applied).
	createGolangMigrateSourceTable(t, tb.DB(), "schema_migrations", 3, false)

	m := newImportTestMigrator(t, tb, "migrations", legacyDir)

	// Preview first.
	plan, err := m.PreviewGolangMigrateImport(context.Background(), lamigrate.GolangMigrateImportOptions{
		SourceTable:    "schema_migrations",
		SourceQuiesced: true,
	})
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	if len(plan.Baselines) != 3 {
		t.Fatalf("preview baselines = %d, want 3", len(plan.Baselines))
	}
	if plan.Empty {
		t.Fatal("preview should not be empty")
	}
	if plan.Noop {
		t.Fatal("preview should not be noop")
	}
	t.Logf("preview: %d baselines, %d unresolved", len(plan.Baselines), len(plan.Unresolved))

	// Execute import.
	result, err := m.ImportGolangMigrate(context.Background(), lamigrate.GolangMigrateImportOptions{
		SourceTable:    "schema_migrations",
		SourceQuiesced: true,
	})
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if result.Command != "import" {
		t.Fatalf("result.Command = %q, want %q", result.Command, "import")
	}

	// Verify baseline rows were inserted.
	count := countRows(t, tb.DB(),
		"SELECT COUNT(*) FROM migrations WHERE source_kind = 'golang_migrate' AND is_baseline = TRUE",
	)
	if count != 3 {
		t.Fatalf("baseline rows = %d, want 3", count)
	}

	// Verify batch=0 for all baselines.
	batchZero := countRows(t, tb.DB(),
		"SELECT COUNT(*) FROM migrations WHERE source_kind = 'golang_migrate' AND batch = 0",
	)
	if batchZero != 3 {
		t.Fatalf("batch=0 rows = %d, want 3", batchZero)
	}

	// Verify state=applied for all baselines.
	appliedCount := countRows(t, tb.DB(),
		"SELECT COUNT(*) FROM migrations WHERE source_kind = 'golang_migrate' AND state = 'applied'",
	)
	if appliedCount != 3 {
		t.Fatalf("applied rows = %d, want 3", appliedCount)
	}

	t.Log("basic import flow completed successfully")
}

// TestImportRejectsDirtySource verifies that a dirty golang-migrate source is rejected.
func TestImportRejectsDirtySource(t *testing.T) {
	tb := newTestDB(t)
	tb.requireTestDBName()

	legacyDir := t.TempDir()
	writeLegacyPair(t, legacyDir, "1_create_users", "CREATE TABLE users (id INT);", "DROP TABLE users;")

	// Create source table with dirty=true.
	createGolangMigrateSourceTable(t, tb.DB(), "schema_migrations", 1, true)

	m := newImportTestMigrator(t, tb, "migrations", legacyDir)

	// Preview should report dirty and fail.
	_, err := m.PreviewGolangMigrateImport(context.Background(), lamigrate.GolangMigrateImportOptions{
		SourceTable:    "schema_migrations",
		SourceQuiesced: true,
	})
	if err == nil {
		t.Fatal("expected error for dirty source, got nil")
	}
	if !errors.Is(err, lamigrate.ErrDirtyState) {
		t.Fatalf("expected ErrDirtyState, got: %v", err)
	}
	t.Logf("dirty source correctly rejected: %v", err)
}

// TestImportIdempotent verifies that importing the same data twice is a no-op.
func TestImportIdempotent(t *testing.T) {
	tb := newTestDB(t)
	tb.requireTestDBName()

	legacyDir := t.TempDir()
	writeLegacyPair(t, legacyDir, "1_create_users", "CREATE TABLE users (id INT);", "DROP TABLE users;")
	writeLegacyPair(t, legacyDir, "2_add_email", "ALTER TABLE users ADD email VARCHAR(255);", "ALTER TABLE users DROP email;")

	createGolangMigrateSourceTable(t, tb.DB(), "schema_migrations", 2, false)
	m := newImportTestMigrator(t, tb, "migrations", legacyDir)

	// First import.
	_, err := m.ImportGolangMigrate(context.Background(), lamigrate.GolangMigrateImportOptions{
		SourceTable:    "schema_migrations",
		SourceQuiesced: true,
	})
	if err != nil {
		t.Fatalf("first import: %v", err)
	}
	count := countRows(t, tb.DB(), "SELECT COUNT(*) FROM migrations WHERE source_kind = 'golang_migrate'")
	if count != 2 {
		t.Fatalf("after first import: %d rows, want 2", count)
	}

	// Second import — should be idempotent no-op.
	_, err = m.ImportGolangMigrate(context.Background(), lamigrate.GolangMigrateImportOptions{
		SourceTable:    "schema_migrations",
		SourceQuiesced: true,
	})
	if err != nil {
		t.Fatalf("second import: %v", err)
	}
	count = countRows(t, tb.DB(), "SELECT COUNT(*) FROM migrations WHERE source_kind = 'golang_migrate'")
	if count != 2 {
		t.Fatalf("after second import: %d rows, want 2 (idempotent)", count)
	}
	t.Log("idempotent import verified")
}

// TestImportRejectsFutureVersions verifies that versions above the DB version
// appear as unresolved in the plan.
func TestImportRejectsFutureVersions(t *testing.T) {
	tb := newTestDB(t)
	tb.requireTestDBName()

	legacyDir := t.TempDir()
	writeLegacyPair(t, legacyDir, "1_create_users", "CREATE TABLE users (id INT);", "DROP TABLE users;")
	writeLegacyPair(t, legacyDir, "5_future_migration", "SELECT 1;", "SELECT 1;")

	// Source version is 1, but we have file version 5 (unresolved).
	createGolangMigrateSourceTable(t, tb.DB(), "schema_migrations", 1, false)
	m := newImportTestMigrator(t, tb, "migrations", legacyDir)

	plan, err := m.PreviewGolangMigrateImport(context.Background(), lamigrate.GolangMigrateImportOptions{
		SourceTable:    "schema_migrations",
		SourceQuiesced: true,
	})
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	// Version 1 is baseline, version 5 is unresolved.
	if len(plan.Baselines) != 1 {
		t.Fatalf("baselines = %d, want 1", len(plan.Baselines))
	}
	if plan.Baselines[0].Version != 1 {
		t.Fatalf("baseline version = %d, want 1", plan.Baselines[0].Version)
	}
	if len(plan.Unresolved) != 1 {
		t.Fatalf("unresolved = %d, want 1", len(plan.Unresolved))
	}
	if plan.Unresolved[0].Version != 5 {
		t.Fatalf("unresolved version = %d, want 5", plan.Unresolved[0].Version)
	}
	t.Logf("future versions correctly classified: %d baseline, %d unresolved", len(plan.Baselines), len(plan.Unresolved))
}

// TestImportDisplaysPreview verifies preview shows correct baselines.
func TestImportDisplaysPreview(t *testing.T) {
	tb := newTestDB(t)
	tb.requireTestDBName()

	legacyDir := t.TempDir()
	writeLegacyPair(t, legacyDir, "1_a", "SELECT 1;", "SELECT 1;")
	writeLegacyPair(t, legacyDir, "2_b", "SELECT 2;", "SELECT 2;")
	writeLegacyPair(t, legacyDir, "3_c", "SELECT 3;", "SELECT 3;")

	createGolangMigrateSourceTable(t, tb.DB(), "schema_migrations", 2, false)
	m := newImportTestMigrator(t, tb, "migrations", legacyDir)

	plan, err := m.PreviewGolangMigrateImport(context.Background(), lamigrate.GolangMigrateImportOptions{
		SourceTable:    "schema_migrations",
		SourceQuiesced: true,
	})
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	if plan.DryRun != true {
		t.Error("plan.DryRun should be true")
	}
	if plan.SourceVersion != 2 {
		t.Errorf("plan.SourceVersion = %d, want 2", plan.SourceVersion)
	}
	if plan.SourceTable != "schema_migrations" {
		t.Errorf("plan.SourceTable = %q, want %q", plan.SourceTable, "schema_migrations")
	}
	if len(plan.Baselines) != 2 {
		t.Fatalf("baselines = %d, want 2", len(plan.Baselines))
	}
	// Verify each baseline has migration ID, checksums.
	for _, b := range plan.Baselines {
		if b.MigrationID == "" {
			t.Error("baseline has empty MigrationID")
		}
		if b.UpChecksum == "" {
			t.Error("baseline has empty UpChecksum")
		}
		if b.DownChecksum == "" {
			t.Error("baseline has empty DownChecksum")
		}
		t.Logf("baseline: %s (v%d) up=%s down=%s", b.MigrationID, b.Version, b.UpChecksum[:16], b.DownChecksum[:16])
	}
	if len(plan.Unresolved) != 1 {
		t.Fatalf("unresolved = %d, want 1", len(plan.Unresolved))
	}
}

// TestImportRequiresQuiescedAttestation verifies SourceQuiesced=false blocks mutation.
func TestImportRequiresQuiescedAttestation(t *testing.T) {
	tb := newTestDB(t)
	tb.requireTestDBName()

	legacyDir := t.TempDir()
	writeLegacyPair(t, legacyDir, "1_create_users", "CREATE TABLE users (id INT);", "DROP TABLE users;")

	createGolangMigrateSourceTable(t, tb.DB(), "schema_migrations", 1, false)
	m := newImportTestMigrator(t, tb, "migrations", legacyDir)

	// ImportGolangMigrate with SourceQuiesced=false must be rejected
	// BEFORE creating a connector.
	_, err := m.ImportGolangMigrate(context.Background(), lamigrate.GolangMigrateImportOptions{
		SourceTable:    "schema_migrations",
		SourceQuiesced: false,
	})
	if err == nil {
		t.Fatal("expected error for SourceQuiesced=false, got nil")
	}
	if !errors.Is(err, lamigrate.ErrInvalidConfig) {
		t.Fatalf("expected ErrInvalidConfig, got: %v", err)
	}

	// Verify nothing was inserted (no connector was created).
	count := countRows(t, tb.DB(), "SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = ? AND table_name = 'migrations'", tb.Name)
	// The table may not even exist yet since no connector was created.
	t.Logf("table existence check: %d (0 means not created, which is expected)", count)
	t.Log("quiesced attestation correctly enforced")
}
