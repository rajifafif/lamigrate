//go:build integration

package integration

import (
	"context"
	"errors"
	"fmt"
	"testing"

	_ "github.com/go-sql-driver/mysql"
	lamigrate "github.com/rajifafif/lamigrate"
)

// ---------------------------------------------------------------------------
// Integration tests for selective rollback (by-name, by-batch) and refresh.
// ---------------------------------------------------------------------------

// mustCreateMigrations creates multiple timestamped migrations in the given dir.
func mustCreateMigrations(t *testing.T, dir string, migrations []struct {
	Timestamp string
	Name      string
	UpSQL     string
	DownSQL   string
}) {
	t.Helper()
	for _, m := range migrations {
		mustCreateMigration(t, dir, m.Timestamp, m.Name, m.UpSQL, m.DownSQL)
	}
}

func TestSelectiveDownByName(t *testing.T) {
	t.Skip("requires integration DB")
	tb := setupTestDB(t)
	defer tb.Close()

	dir := t.TempDir()
	tableName := "migrations"

	// Create 3 migrations: A, B, C
	mustCreateMigrations(t, dir, []struct {
		Timestamp, Name, UpSQL, DownSQL string
	}{
		{"20260101000001", "create_alpha_table",
			"CREATE TABLE alpha (id INT PRIMARY KEY)",
			"DROP TABLE alpha"},
		{"20260101000002", "create_beta_table",
			"CREATE TABLE beta (id INT PRIMARY KEY)",
			"DROP TABLE beta"},
		{"20260101000003", "create_gamma_table",
			"CREATE TABLE gamma (id INT PRIMARY KEY)",
			"DROP TABLE gamma"},
	})

	ctx := context.Background()
	m := newTestMigratorWithDir(t, tb, tableName, dir)

	// Apply all 3.
	result, err := m.Up(ctx, lamigrate.All())
	if err != nil {
		t.Fatalf("Up: %v", err)
	}
	if len(result.Migrated) != 3 {
		t.Fatalf("expected 3 applied, got %d", len(result.Migrated))
	}

	// Roll back by name "B". Single-migration rollback: ONLY B is rolled back.
	target, err := lamigrate.DownToName("20260101000002_create_beta_table")
	if err != nil {
		t.Fatalf("DownToName: %v", err)
	}
	result, err = m.Down(ctx, target)
	if err != nil {
		t.Fatalf("Down by name: %v", err)
	}
	if len(result.Migrated) != 1 {
		t.Fatalf("expected 1 rolled back (only B), got %d", len(result.Migrated))
	}
	if result.Migrated[0].Name != "20260101000002_create_beta_table" {
		t.Fatalf("rollback should be beta only, got %s", result.Migrated[0].Name)
	}

	// Alpha should still exist.
	if !tableExists(t, tb, "alpha") {
		t.Fatal("alpha table should still exist after selective down")
	}
	// Gamma should ALSO still exist (only B was rolled back).
	if !tableExists(t, tb, "gamma") {
		t.Fatal("gamma table should still exist after single-migration down")
	}
}

func TestSelectiveDownByNameNotFound(t *testing.T) {
	t.Skip("requires integration DB")
	tb := setupTestDB(t)
	defer tb.Close()

	dir := t.TempDir()
	tableName := "migrations"

	mustCreateMigrations(t, dir, []struct {
		Timestamp, Name, UpSQL, DownSQL string
	}{
		{"20260101000001", "create_alpha_table",
			"CREATE TABLE alpha (id INT PRIMARY KEY)",
			"DROP TABLE alpha"},
	})

	ctx := context.Background()
	m := newTestMigratorWithDir(t, tb, tableName, dir)

	_, err := m.Up(ctx, lamigrate.All())
	if err != nil {
		t.Fatalf("Up: %v", err)
	}

	// Try to roll back a name that doesn't exist.
	target, err := lamigrate.DownToName("nonexistent_migration")
	if err != nil {
		t.Fatalf("DownToName: %v", err)
	}
	_, err = m.Down(ctx, target)
	if err == nil {
		t.Fatal("expected error for nonexistent migration name")
	}
	if !errors.Is(err, lamigrate.ErrMigrationNotFoundInLatestBatch) {
		t.Fatalf("expected ErrMigrationNotFoundInLatestBatch, got %v", err)
	}
}

func TestSelectiveDownByBatch(t *testing.T) {
	t.Skip("requires integration DB")
	tb := setupTestDB(t)
	defer tb.Close()

	dir := t.TempDir()
	tableName := "migrations"

	mustCreateMigrations(t, dir, []struct {
		Timestamp, Name, UpSQL, DownSQL string
	}{
		{"20260101000001", "create_alpha_table",
			"CREATE TABLE alpha (id INT PRIMARY KEY)",
			"DROP TABLE alpha"},
		{"20260101000002", "create_beta_table",
			"CREATE TABLE beta (id INT PRIMARY KEY)",
			"DROP TABLE beta"},
	})

	ctx := context.Background()
	m := newTestMigratorWithDir(t, tb, tableName, dir)

	// Apply all.
	_, err := m.Up(ctx, lamigrate.All())
	if err != nil {
		t.Fatalf("Up: %v", err)
	}

	// Roll back batch 1 (which should be the latest).
	target, err := lamigrate.DownToBatch(1)
	if err != nil {
		t.Fatalf("DownToBatch: %v", err)
	}
	result, err := m.Down(ctx, target)
	if err != nil {
		t.Fatalf("Down by batch: %v", err)
	}
	if len(result.Migrated) != 2 {
		t.Fatalf("expected 2 rolled back, got %d", len(result.Migrated))
	}
}

func TestSelectiveDownByBatchNotLatest(t *testing.T) {
	t.Skip("requires integration DB")
	tb := setupTestDB(t)
	defer tb.Close()

	dir := t.TempDir()
	tableName := "migrations"

	mustCreateMigrations(t, dir, []struct {
		Timestamp, Name, UpSQL, DownSQL string
	}{
		{"20260101000001", "create_alpha_table",
			"CREATE TABLE alpha (id INT PRIMARY KEY)",
			"DROP TABLE alpha"},
	})

	ctx := context.Background()
	m := newTestMigratorWithDir(t, tb, tableName, dir)

	_, err := m.Up(ctx, lamigrate.All())
	if err != nil {
		t.Fatalf("Up: %v", err)
	}

	// Try batch 5 (doesn't exist, latest is 1).
	target, err := lamigrate.DownToBatch(5)
	if err != nil {
		t.Fatalf("DownToBatch: %v", err)
	}
	_, err = m.Down(ctx, target)
	if err == nil {
		t.Fatal("expected error for non-latest batch")
	}
	if !errors.Is(err, lamigrate.ErrBatchNotLatest) {
		t.Fatalf("expected ErrBatchNotLatest, got %v", err)
	}
}

func TestRefreshBare(t *testing.T) {
	t.Skip("requires integration DB")
	tb := setupTestDB(t)
	defer tb.Close()

	dir := t.TempDir()
	tableName := "migrations"

	mustCreateMigrations(t, dir, []struct {
		Timestamp, Name, UpSQL, DownSQL string
	}{
		{"20260101000001", "create_alpha_table",
			"CREATE TABLE alpha (id INT PRIMARY KEY)",
			"DROP TABLE alpha"},
		{"20260101000002", "create_beta_table",
			"CREATE TABLE beta (id INT PRIMARY KEY)",
			"DROP TABLE beta"},
	})

	ctx := context.Background()
	m := newTestMigratorWithDir(t, tb, tableName, dir)

	// Apply both.
	_, err := m.Up(ctx, lamigrate.All())
	if err != nil {
		t.Fatalf("Up: %v", err)
	}

	// Refresh: rollback all + re-apply all.
	result, err := m.Refresh(ctx, lamigrate.RefreshAll())
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}

	total := len(result.Rollback.Migrated) + len(result.Apply.Migrated)
	if total != 4 { // 2 rolled back + 2 re-applied
		t.Fatalf("expected 4 total migrations in refresh, got %d", total)
	}

	// Both tables should exist after refresh.
	if !tableExists(t, tb, "alpha") {
		t.Fatal("alpha should exist after refresh")
	}
	if !tableExists(t, tb, "beta") {
		t.Fatal("beta should exist after refresh")
	}
}

func TestRefreshByStep(t *testing.T) {
	t.Skip("requires integration DB")
	tb := setupTestDB(t)
	defer tb.Close()

	dir := t.TempDir()
	tableName := "migrations"

	mustCreateMigrations(t, dir, []struct {
		Timestamp, Name, UpSQL, DownSQL string
	}{
		{"20260101000001", "create_alpha_table",
			"CREATE TABLE alpha (id INT PRIMARY KEY)",
			"DROP TABLE alpha"},
		{"20260101000002", "create_beta_table",
			"CREATE TABLE beta (id INT PRIMARY KEY)",
			"DROP TABLE beta"},
		{"20260101000003", "create_gamma_table",
			"CREATE TABLE gamma (id INT PRIMARY KEY)",
			"DROP TABLE gamma"},
	})

	ctx := context.Background()
	m := newTestMigratorWithDir(t, tb, tableName, dir)

	_, err := m.Up(ctx, lamigrate.All())
	if err != nil {
		t.Fatalf("Up: %v", err)
	}

	// Refresh --step 2: rollback 2, re-apply 2.
	target, err := lamigrate.RefreshSteps(2)
	if err != nil {
		t.Fatalf("RefreshSteps: %v", err)
	}
	result, err := m.Refresh(ctx, target)
	if err != nil {
		t.Fatalf("Refresh --step 2: %v", err)
	}

	// Rollback should have 2 migrations.
	if len(result.Rollback.Migrated) != 2 {
		t.Fatalf("expected 2 rolled back, got %d", len(result.Rollback.Migrated))
	}
	// Re-apply should have 2.
	if len(result.Apply.Migrated) != 2 {
		t.Fatalf("expected 2 re-applied, got %d", len(result.Apply.Migrated))
	}

	// Alpha should still exist (wasn't rolled back).
	if !tableExists(t, tb, "alpha") {
		t.Fatal("alpha should still exist after refresh --step 2")
	}
}

func TestRefreshNothingToRollback(t *testing.T) {
	t.Skip("requires integration DB")
	tb := setupTestDB(t)
	defer tb.Close()

	dir := t.TempDir()
	tableName := "migrations"

	// No migrations created.
	ctx := context.Background()
	m := newTestMigratorWithDir(t, tb, tableName, dir)

	_, err := m.Refresh(ctx, lamigrate.RefreshAll())
	if err == nil {
		t.Fatal("expected error when nothing to refresh")
	}
	if !errors.Is(err, lamigrate.ErrRefreshNothingToRollback) {
		t.Fatalf("expected ErrRefreshNothingToRollback, got %v", err)
	}
}

func TestDownTargetConstructors(t *testing.T) {
	t.Parallel()

	// DownAll — no error
	_ = lamigrate.DownAll()

	// DownSteps
	s, err := lamigrate.DownSteps(3)
	if err != nil {
		t.Fatalf("DownSteps: %v", err)
	}
	_ = s // valid

	// DownToName
	n, err := lamigrate.DownToName("20260101000001_create_alpha")
	if err != nil {
		t.Fatalf("DownToName: %v", err)
	}
	if n.Name != "20260101000001_create_alpha" {
		t.Fatalf("DownToName name = %q", n.Name)
	}

	// DownToName empty
	_, err = lamigrate.DownToName("")
	if err == nil {
		t.Fatal("DownToName('') should error")
	}

	// DownToBatch
	b, err := lamigrate.DownToBatch(5)
	if err != nil {
		t.Fatalf("DownToBatch: %v", err)
	}
	if b.Batch != 5 {
		t.Fatalf("DownToBatch(5).Batch = %d", b.Batch)
	}

	// DownToBatch invalid
	_, err = lamigrate.DownToBatch(0)
	if err == nil {
		t.Fatal("DownToBatch(0) should error")
	}

	// DownSteps invalid
	_, err = lamigrate.DownSteps(0)
	if err == nil {
		t.Fatal("DownSteps(0) should error")
	}
}

func TestRefreshTargetConstructors(t *testing.T) {
	t.Parallel()

	// RefreshAll — no error
	_ = lamigrate.RefreshAll()

	// RefreshSteps
	s, err := lamigrate.RefreshSteps(3)
	if err != nil {
		t.Fatalf("RefreshSteps: %v", err)
	}
	_ = s // valid

	// RefreshToName
	n, err := lamigrate.RefreshToName("20260101000001_create_alpha")
	if err != nil {
		t.Fatalf("RefreshToName: %v", err)
	}
	if n.Name != "20260101000001_create_alpha" {
		t.Fatalf("RefreshToName name = %q", n.Name)
	}

	// RefreshToName empty
	_, err = lamigrate.RefreshToName("")
	if err == nil {
		t.Fatal("RefreshToName('') should error")
	}
}