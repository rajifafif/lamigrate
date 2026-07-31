//go:build integration

package integration

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-sql-driver/mysql"
	lamigrate "github.com/rajifafif/lamigrate"
)

// ---------- TestPlanUpAfterBootstrap ----------

// TestPlanUpAfterBootstrap verifies that building an up plan against
// an empty (freshly bootstrapped) metadata table shows all discovered
// migrations as pending.
func TestPlanUpAfterBootstrap(t *testing.T) {
	tb := newTestDB(t)
	tb.requireTestDBName()

	// Create migration files in the test temp dir.
	dir := tb.t.TempDir()
	writeMigrationPair(t, dir, "20260730120000_create_users", "CREATE TABLE users (id INT);", "DROP TABLE users;")
	writeMigrationPair(t, dir, "20260730130000_create_posts", "CREATE TABLE posts (id INT);", "DROP TABLE posts;")

	m := newTestMigratorWithDir(t, tb, "migrations", dir)

	// Bootstrap metadata tables.
	if err := runBootstrap(t, m); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}

	// Preview should show both migrations as pending.
	plan, err := m.PreviewUp(context.Background(), lamigrate.All())
	if err != nil {
		t.Fatalf("PreviewUp: %v", err)
	}
	if !plan.DryRun {
		t.Error("DryRun should be true")
	}
	if len(plan.Migrations) != 2 {
		t.Fatalf("expected 2 pending migrations, got %d: %v", len(plan.Migrations), plan.Migrations)
	}
	t.Logf("up plan after bootstrap: %v", plan.Migrations)
}

// ---------- TestPlanUpAfterApply ----------

// TestPlanUpAfterApply verifies that after manually inserting an
// applied record, the up plan correctly shows only the remaining
// pending migrations.
func TestPlanUpAfterApply(t *testing.T) {
	tb := newTestDB(t)
	tb.requireTestDBName()

	dir := tb.t.TempDir()
	writeMigrationPair(t, dir, "20260730120000_create_users", "CREATE TABLE users (id INT);", "DROP TABLE users;")
	writeMigrationPair(t, dir, "20260730130000_create_posts", "CREATE TABLE posts (id INT);", "DROP TABLE posts;")

	m := newTestMigratorWithDir(t, tb, "migrations", dir)

	if err := runBootstrap(t, m); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}

	// Manually mark first migration as applied.
	upChecksum := computeFileChecksum(t, filepath.Join(dir, "20260730120000_create_users.up.sql"))
	downChecksum := computeFileChecksum(t, filepath.Join(dir, "20260730120000_create_users.down.sql"))
	runnerID := "00000000-0000-0000-0000-000000000001"
	now := time.Now().UTC().Format("2006-01-02 15:04:05.000000")

	_, err := tb.Exec(
		"INSERT INTO `migrations` (migration, source_kind, source_name, up_checksum, down_checksum, batch, state, is_baseline, runner_id, started_at, applied_at, updated_at) VALUES (?, 'timestamp', ?, ?, ?, 1, 'applied', FALSE, ?, ?, ?, ?)",
		"20260730120000_create_users",
		"20260730120000_create_users",
		upChecksum, downChecksum,
		runnerID, now, now, now,
	)
	if err != nil {
		t.Fatalf("insert applied migration: %v", err)
	}

	// Now PreviewUp should show only the second migration.
	plan, err := m.PreviewUp(context.Background(), lamigrate.All())
	if err != nil {
		t.Fatalf("PreviewUp: %v", err)
	}
	if len(plan.Migrations) != 1 {
		t.Fatalf("expected 1 pending migration, got %d: %v", len(plan.Migrations), plan.Migrations)
	}
	if plan.Migrations[0] != "20260730130000_create_posts" {
		t.Errorf("expected pending migration create_posts, got %s", plan.Migrations[0])
	}
	t.Logf("up plan after apply: %v", plan.Migrations)
}

// ---------- TestPlanDriftDetection ----------

// TestPlanDriftDetection verifies that modifying an applied migration
// file after it was recorded causes checksum drift detection.
func TestPlanDriftDetection(t *testing.T) {
	tb := newTestDB(t)
	tb.requireTestDBName()

	dir := tb.t.TempDir()
	writeMigrationPair(t, dir, "20260730120000_create_users", "CREATE TABLE users (id INT);", "DROP TABLE users;")
	writeMigrationPair(t, dir, "20260730130000_create_posts", "CREATE TABLE posts (id INT);", "DROP TABLE posts;")

	m := newTestMigratorWithDir(t, tb, "migrations", dir)

	if err := runBootstrap(t, m); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}

	// Mark both as applied with correct checksums.
	for _, name := range []string{"20260730120000_create_users", "20260730130000_create_posts"} {
		upChecksum := computeFileChecksum(t, filepath.Join(dir, name+".up.sql"))
		downChecksum := computeFileChecksum(t, filepath.Join(dir, name+".down.sql"))
		runnerID := "00000000-0000-0000-0000-000000000001"
		now := time.Now().UTC().Format("2006-01-02 15:04:05.000000")

		_, err := tb.Exec(
			"INSERT INTO `migrations` (migration, source_kind, source_name, up_checksum, down_checksum, batch, state, is_baseline, runner_id, started_at, applied_at, updated_at) VALUES (?, 'timestamp', ?, ?, ?, 1, 'applied', FALSE, ?, ?, ?, ?)",
			name, name, upChecksum, downChecksum, runnerID, now, now, now,
		)
		if err != nil {
			t.Fatalf("insert applied migration %s: %v", name, err)
		}
	}

	// Modify the up file to create drift.
	driftPath := filepath.Join(dir, "20260730120000_create_users.up.sql")
	if err := os.WriteFile(driftPath, []byte("CREATE TABLE users (id INT, name VARCHAR(255));"), 0o644); err != nil {
		t.Fatalf("write drifted file: %v", err)
	}

	// PreviewUp should detect drift.
	_, err := m.PreviewUp(context.Background(), lamigrate.All())
	if err == nil {
		t.Fatal("expected drift detection error")
	}
	if !errors.Is(err, lamigrate.ErrChecksumDrift) {
		t.Fatalf("expected ErrChecksumDrift, got: %v", err)
	}
	t.Logf("drift correctly detected: %v", err)
}

// ---------- TestPlanDirtyBlocksExecution ----------

// TestPlanDirtyBlocksExecution verifies that a dirty metadata row
// (applying/apply_failed/rolling_back/rollback_failed) blocks
// plan construction.
func TestPlanDirtyBlocksExecution(t *testing.T) {
	tb := newTestDB(t)
	tb.requireTestDBName()

	dir := tb.t.TempDir()
	writeMigrationPair(t, dir, "20260730120000_create_users", "CREATE TABLE users (id INT);", "DROP TABLE users;")

	m := newTestMigratorWithDir(t, tb, "migrations", dir)

	if err := runBootstrap(t, m); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}

	// Insert a dirty "applying" row.
	upChecksum := computeFileChecksum(t, filepath.Join(dir, "20260730120000_create_users.up.sql"))
	downChecksum := computeFileChecksum(t, filepath.Join(dir, "20260730120000_create_users.down.sql"))
	runnerID := "00000000-0000-0000-0000-000000000001"
	now := time.Now().UTC().Format("2006-01-02 15:04:05.000000")

	_, err := tb.Exec(
		"INSERT INTO `migrations` (migration, source_kind, source_name, up_checksum, down_checksum, batch, state, is_baseline, runner_id, started_at, updated_at) VALUES (?, 'timestamp', ?, ?, ?, 1, 'applying', FALSE, ?, ?, ?)",
		"20260730120000_create_users",
		"20260730120000_create_users",
		upChecksum, downChecksum, runnerID, now, now,
	)
	if err != nil {
		t.Fatalf("insert dirty migration: %v", err)
	}

	// PreviewUp should fail with dirty state error.
	_, err = m.PreviewUp(context.Background(), lamigrate.All())
	if err == nil {
		t.Fatal("expected dirty state error")
	}
	if !errors.Is(err, lamigrate.ErrDirtyState) {
		t.Fatalf("expected ErrDirtyState, got: %v", err)
	}
	t.Logf("dirty state correctly blocks: %v", err)
}

// ---------- TestPreviewDoesNotAllocateBatch ----------

// TestPreviewDoesNotAllocateBatch verifies that preview methods
// create no metadata — specifically, no batch is allocated.
func TestPreviewDoesNotAllocateBatch(t *testing.T) {
	tb := newTestDB(t)
	tb.requireTestDBName()

	dir := tb.t.TempDir()
	writeMigrationPair(t, dir, "20260730120000_create_users", "CREATE TABLE users (id INT);", "DROP TABLE users;")

	m := newTestMigratorWithDir(t, tb, "migrations", dir)

	if err := runBootstrap(t, m); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}

	// Read next_batch before preview.
	var batchBefore int
	err := tb.DB().QueryRow(
		"SELECT next_batch FROM lamigrate_control WHERE tracking_table = 'migrations'",
	).Scan(&batchBefore)
	if err != nil {
		t.Fatalf("query next_batch: %v", err)
	}

	// Run preview.
	plan, err := m.PreviewUp(context.Background(), lamigrate.All())
	if err != nil {
		t.Fatalf("PreviewUp: %v", err)
	}
	if !plan.DryRun {
		t.Error("DryRun should be true")
	}
	if plan.Batch != 0 {
		t.Errorf("preview Batch = %d, want 0", plan.Batch)
	}

	// Verify next_batch was NOT changed.
	var batchAfter int
	err = tb.DB().QueryRow(
		"SELECT next_batch FROM lamigrate_control WHERE tracking_table = 'migrations'",
	).Scan(&batchAfter)
	if err != nil {
		t.Fatalf("query next_batch after preview: %v", err)
	}
	if batchAfter != batchBefore {
		t.Errorf("next_batch changed during preview: before=%d, after=%d", batchBefore, batchAfter)
	}
	t.Logf("preview did not allocate batch (next_batch unchanged at %d)", batchAfter)
}

// ---------- TestStatusReportEmpty ----------

// TestStatusReportEmpty verifies status on an uninitialized database
// (no metadata tables) returns all pending migrations.
func TestStatusReportEmpty(t *testing.T) {
	tb := newTestDB(t)
	tb.requireTestDBName()

	dir := tb.t.TempDir()
	writeMigrationPair(t, dir, "20260730120000_create_users", "CREATE TABLE users (id INT);", "DROP TABLE users;")

	m := newTestMigratorWithDir(t, tb, "migrations", dir)

	// Do NOT bootstrap — test uninitialized behavior.
	report, err := m.Status(context.Background())
	if err != nil {
		t.Fatalf("Status on uninitialized DB: %v", err)
	}

	if len(report.Migrations) == 0 {
		t.Fatal("expected at least one migration in status")
	}

	for _, ms := range report.Migrations {
		if ms.Status != "pending" {
			t.Errorf("expected pending status for %s, got %q", ms.Name, ms.Status)
		}
	}
	t.Logf("status on empty DB: %d migrations, all pending", len(report.Migrations))
}

// ---------- TestStatusReportWithMigrations ----------

// TestStatusReportWithMigrations verifies that status correctly
// classifies both applied and pending migrations.
func TestStatusReportWithMigrations(t *testing.T) {
	tb := newTestDB(t)
	tb.requireTestDBName()

	dir := tb.t.TempDir()
	writeMigrationPair(t, dir, "20260730120000_create_users", "CREATE TABLE users (id INT);", "DROP TABLE users;")
	writeMigrationPair(t, dir, "20260730130000_create_posts", "CREATE TABLE posts (id INT);", "DROP TABLE posts;")

	m := newTestMigratorWithDir(t, tb, "migrations", dir)

	if err := runBootstrap(t, m); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}

	// Mark first as applied.
	upChecksum := computeFileChecksum(t, filepath.Join(dir, "20260730120000_create_users.up.sql"))
	downChecksum := computeFileChecksum(t, filepath.Join(dir, "20260730120000_create_users.down.sql"))
	runnerID := "00000000-0000-0000-0000-000000000001"
	now := time.Now().UTC().Format("2006-01-02 15:04:05.000000")

	_, err := tb.Exec(
		"INSERT INTO `migrations` (migration, source_kind, source_name, up_checksum, down_checksum, batch, state, is_baseline, runner_id, started_at, applied_at, updated_at) VALUES (?, 'timestamp', ?, ?, ?, 1, 'applied', FALSE, ?, ?, ?, ?)",
		"20260730120000_create_users", "20260730120000_create_users",
		upChecksum, downChecksum, runnerID, now, now, now,
	)
	if err != nil {
		t.Fatalf("insert applied: %v", err)
	}

	report, err := m.Status(context.Background())
	if err != nil {
		t.Fatalf("Status: %v", err)
	}

	var appliedCount, pendingCount int
	for _, ms := range report.Migrations {
		switch ms.Status {
		case "applied":
			appliedCount++
		case "pending":
			pendingCount++
		default:
			t.Errorf("unexpected status %q for %s", ms.Status, ms.Name)
		}
	}

	if appliedCount != 1 {
		t.Errorf("applied count = %d, want 1", appliedCount)
	}
	if pendingCount != 1 {
		t.Errorf("pending count = %d, want 1", pendingCount)
	}
	t.Logf("status: %d applied, %d pending", appliedCount, pendingCount)
}

// ---------- TestResetPlanShowsAllApplied ----------

// TestResetPlanShowsAllApplied verifies that a reset plan includes
// all non-baseline applied migrations in reverse execution order.
func TestResetPlanShowsAllApplied(t *testing.T) {
	tb := newTestDB(t)
	tb.requireTestDBName()

	dir := tb.t.TempDir()
	writeMigrationPair(t, dir, "20260730120000_create_users", "CREATE TABLE users (id INT);", "DROP TABLE users;")
	writeMigrationPair(t, dir, "20260730130000_create_posts", "CREATE TABLE posts (id INT);", "DROP TABLE posts;")
	writeMigrationPair(t, dir, "20260730140000_add_email", "ALTER TABLE users ADD COLUMN email VARCHAR(255);", "ALTER TABLE users DROP COLUMN email;")

	m := newTestMigratorWithDir(t, tb, "migrations", dir)

	if err := runBootstrap(t, m); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}

	// Mark all three as applied.
	for i, name := range []string{
		"20260730120000_create_users",
		"20260730130000_create_posts",
		"20260730140000_add_email",
	} {
		upChecksum := computeFileChecksum(t, filepath.Join(dir, name+".up.sql"))
		downChecksum := computeFileChecksum(t, filepath.Join(dir, name+".down.sql"))
		runnerID := "00000000-0000-0000-0000-000000000001"
		now := time.Now().UTC().Format("2006-01-02 15:04:05.000000")

		_, err := tb.Exec(
			"INSERT INTO `migrations` (migration, source_kind, source_name, up_checksum, down_checksum, batch, state, is_baseline, runner_id, started_at, applied_at, updated_at) VALUES (?, 'timestamp', ?, ?, ?, 1, 'applied', FALSE, ?, ?, ?, ?)",
			name, name, upChecksum, downChecksum, runnerID, now, now, now,
		)
		if err != nil {
			t.Fatalf("insert applied %s (i=%d): %v", name, i, err)
		}
	}

	// Reset plan should show all three in reverse order.
	plan, err := m.PreviewReset(context.Background())
	if err != nil {
		t.Fatalf("PreviewReset: %v", err)
	}
	if len(plan.Migrations) != 3 {
		t.Fatalf("expected 3 migrations in reset plan, got %d: %v", len(plan.Migrations), plan.Migrations)
	}

	// Should be in reverse execution order.
	expected := []string{
		"20260730140000_add_email",
		"20260730130000_create_posts",
		"20260730120000_create_users",
	}
	for i, name := range expected {
		if plan.Migrations[i] != name {
			t.Errorf("reset plan[%d] = %q, want %q", i, plan.Migrations[i], name)
		}
	}
	t.Logf("reset plan (reverse order): %v", plan.Migrations)
}

// ---------- helpers ----------

// newTestMigratorWithDir creates a Migrator connected to the test DB
// with a specific migration directory.
func newTestMigratorWithDir(tb *testing.T, testDB *TestDB, tableName, dir string) *lamigrate.Migrator {
	tb.Helper()

	cfg, err := mysql.ParseDSN(testDB.DSN)
	if err != nil {
		tb.Fatalf("parse DSN: %v", err)
	}
	cfg.MultiStatements = true
	cfg.ParseTime = true

	m, err := lamigrate.NewMySQL(cfg, lamigrate.Options{
		Directory:   dir,
		TableName:   tableName,
		LockTimeout: 10 * time.Second,
	})
	if err != nil {
		tb.Fatalf("NewMySQL: %v", err)
	}
	return m
}

// writeMigrationPair creates a migration pair in the given directory.
func writeMigrationPair(tb *testing.T, dir, name, upSQL, downSQL string) {
	tb.Helper()
	upPath := filepath.Join(dir, name+".up.sql")
	downPath := filepath.Join(dir, name+".down.sql")
	if err := os.WriteFile(upPath, []byte(upSQL), 0o644); err != nil {
		tb.Fatalf("write up file: %v", err)
	}
	if err := os.WriteFile(downPath, []byte(downSQL), 0o644); err != nil {
		tb.Fatalf("write down file: %v", err)
	}
}

// computeFileChecksum reads a file and returns its checksum as a hex string.
func computeFileChecksum(tb *testing.T, path string) []byte {
	tb.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		tb.Fatalf("read file for checksum: %v", err)
	}
	sum := lamigrate.ChecksumBytesForTest(data)
	return sum[:]
}

// Ensure fmt is used.
var _ = strings.TrimSpace
