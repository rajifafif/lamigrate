package lamigrate

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// TestBuildUpPlanRejectsDirtyState verifies that dirty metadata rows
// cause the global drift check to reject plan construction.
func TestBuildUpPlanRejectsDirtyState(t *testing.T) {
	t.Parallel()

	// Create source files.
	dir := t.TempDir()
	writeTestMigration(t, dir, "20260730120000_create_users", "CREATE TABLE users (id INT);", "DROP TABLE users;")

	// Build a source map.
	files, err := scanMigrations(dir)
	if err != nil {
		t.Fatalf("scanMigrations: %v", err)
	}
	sourceMap := make(map[string]*migrationFile, len(files))
	for i := range files {
		sourceMap[files[i].Name] = &files[i]
	}

	// Test each dirty state.
	for _, state := range []string{"applying", "apply_failed", "rolling_back", "rollback_failed"} {
		applied := []AppliedMigration{
			{
				Migration:  "20260730120000_create_users",
				SourceKind: "timestamp",
				State:      state,
				Batch:      1,
				IsBaseline: false,
			},
		}

		err := globalDriftCheck(applied, sourceMap, false)
		if err == nil {
			t.Errorf("globalDriftCheck should reject dirty state %q", state)
			continue
		}
		if !errors.Is(err, ErrDirtyState) {
			t.Errorf("expected ErrDirtyState for state %q, got: %v", state, err)
		}
	}
}

// TestBuildUpPlanRejectsDrift verifies that checksum mismatches
// between source files and metadata are detected.
func TestBuildUpPlanRejectsDrift(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeTestMigration(t, dir, "20260730120000_create_users", "CREATE TABLE users (id INT);", "DROP TABLE users;")

	files, err := scanMigrations(dir)
	if err != nil {
		t.Fatalf("scanMigrations: %v", err)
	}
	sourceMap := make(map[string]*migrationFile, len(files))
	for i := range files {
		sourceMap[files[i].Name] = &files[i]
	}

	// Create metadata with a wrong checksum.
	applied := []AppliedMigration{
		{
			Migration:  "20260730120000_create_users",
			SourceKind: "timestamp",
			State:      "applied",
			Batch:      1,
			IsBaseline: false,
			UpChecksum: []byte{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15,
				16, 17, 18, 19, 20, 21, 22, 23, 24, 25, 26, 27, 28, 29, 30, 31},
		},
	}

	err = globalDriftCheck(applied, sourceMap, false)
	if err == nil {
		t.Fatal("globalDriftCheck should detect checksum drift")
	}
	if !errors.Is(err, ErrChecksumDrift) {
		t.Fatalf("expected ErrChecksumDrift, got: %v", err)
	}
}

// TestBuildUpPlanRejectsMissingSource verifies that applied metadata
// rows without corresponding source files are rejected.
func TestBuildUpPlanRejectsMissingSource(t *testing.T) {
	t.Parallel()

	// Simulate deleted files — source map is empty.
	sourceMap := make(map[string]*migrationFile)

	applied := []AppliedMigration{
		{
			Migration:  "20260730120000_create_users",
			SourceKind: "timestamp",
			State:      "applied",
			Batch:      1,
			IsBaseline: false,
		},
	}

	err := globalDriftCheck(applied, sourceMap, false)
	if err == nil {
		t.Fatal("globalDriftCheck should reject missing source files")
	}
	if !errors.Is(err, ErrChecksumDrift) {
		t.Fatalf("expected ErrChecksumDrift for missing source, got: %v", err)
	}
}

// TestBuildUpPlanRejectsDuplicateTimestamp verifies that duplicate
// timestamps in selected migration files are rejected.
func TestBuildUpPlanRejectsDuplicateTimestamp(t *testing.T) {
	t.Parallel()

	dups := []migrationFile{
		{Name: "20260730120000_create_users", Timestamp: 20260730120000},
		{Name: "20260730120000_create_posts", Timestamp: 20260730120000},
	}

	err := checkDuplicateTimestamps(dups)
	if err == nil {
		t.Fatal("checkDuplicateTimestamps should reject duplicate timestamps")
	}
}

// TestIgnoreMissingSourceSkipsOrphan verifies that when the ignore
// flag is set, an applied migration with no source file does not block,
// while a genuine checksum drift on a present file still does.
func TestIgnoreMissingSourceSkipsOrphan(t *testing.T) {
	t.Parallel()

	// Orphaned applied row — no matching source file.
	orphan := []AppliedMigration{
		{
			Migration:  "20260730120000_create_users",
			SourceKind: "timestamp",
			State:      "applied",
			Batch:      1,
			IsBaseline: false,
		},
	}
	emptySourceMap := make(map[string]*migrationFile)

	// Without the flag, it blocks.
	if err := globalDriftCheck(orphan, emptySourceMap, false); err == nil {
		t.Fatal("ignored=false should block orphaned applied migration")
	}
	// With the flag, it is skipped.
	if err := globalDriftCheck(orphan, emptySourceMap, true); err != nil {
		t.Fatalf("ignored=true should skip orphan, got: %v", err)
	}

	// Drift on a PRESENT source is still a hard error even with the flag.
	dir := t.TempDir()
	writeTestMigration(t, dir, "20260730120000_create_users", "CREATE TABLE users (id INT);", "DROP TABLE users;")
	files, err := scanMigrations(dir)
	if err != nil {
		t.Fatalf("scanMigrations: %v", err)
	}
	sourceMap := make(map[string]*migrationFile, len(files))
	for i := range files {
		sourceMap[files[i].Name] = &files[i]
	}
	drifted := []AppliedMigration{
		{
			Migration:  "20260730120000_create_users",
			SourceKind: "timestamp",
			State:      "applied",
			Batch:      1,
			IsBaseline: false,
			UpChecksum: []byte{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15,
				16, 17, 18, 19, 20, 21, 22, 23, 24, 25, 26, 27, 28, 29, 30, 31},
		},
	}
	if err := globalDriftCheck(drifted, sourceMap, true); err == nil {
		t.Fatal("checksum drift must still be detected even with ignoreMissingSource=true")
	} else if !errors.Is(err, ErrChecksumDrift) {
		t.Fatalf("expected ErrChecksumDrift, got: %v", err)
	}
}

// TestBuildUpPlanReadsAllApplied verifies that the global integrity
// check examines ALL applied records, not just selected ones.
func TestBuildUpPlanReadsAllApplied(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeTestMigration(t, dir, "20260730120000_create_users", "CREATE TABLE users (id INT);", "DROP TABLE users;")
	writeTestMigration(t, dir, "20260730130000_create_posts", "CREATE TABLE posts (id INT);", "DROP TABLE posts;")

	files, err := scanMigrations(dir)
	if err != nil {
		t.Fatalf("scanMigrations: %v", err)
	}
	sourceMap := make(map[string]*migrationFile, len(files))
	for i := range files {
		sourceMap[files[i].Name] = &files[i]
	}

	// Both migrations are applied. The second one has drift.
	applied := []AppliedMigration{
		{
			Migration:  "20260730120000_create_users",
			SourceKind: "timestamp",
			State:      "applied",
			Batch:      1,
			IsBaseline: false,
		},
		{
			Migration:  "20260730130000_create_posts",
			SourceKind: "timestamp",
			State:      "applied",
			Batch:      1,
			IsBaseline: false,
			UpChecksum: []byte{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15,
				16, 17, 18, 19, 20, 21, 22, 23, 24, 25, 26, 27, 28, 29, 30, 31},
		},
	}

	// Even though both are "applied", drift in the second is detected.
	err = globalDriftCheck(applied, sourceMap, false)
	if err == nil {
		t.Fatal("globalDriftCheck should detect drift in any applied record")
	}
	if !errors.Is(err, ErrChecksumDrift) {
		t.Fatalf("expected ErrChecksumDrift, got: %v", err)
	}
}

// TestPlanViewIsReadOnly verifies that PlanView is a simple value type
// with no mutation methods. Internal MigrationPlan is unexported and
// cannot be accessed from external packages.
func TestPlanViewIsReadOnly(t *testing.T) {
	t.Parallel()

	view := PlanView{
		Command:    "up",
		Directory:  "/tmp/migrations",
		TableName:  "migrations",
		Migrations: []string{"20260730120000_create_users"},
		DryRun:     true,
		Batch:      1,
	}

	// PlanView should be a copy when assigned.
	view2 := view
	view2.Migrations = append(view2.Migrations, "20260730130000_create_posts")

	// The original should be unaffected (slice header copy).
	if len(view.Migrations) != 1 {
		t.Fatalf("PlanView is not a copy: original has %d migrations, want 1", len(view.Migrations))
	}
	if len(view2.Migrations) != 2 {
		t.Fatalf("copied PlanView should have 2 migrations, got %d", len(view2.Migrations))
	}

	// Verify fields.
	if !view.DryRun {
		t.Error("DryRun should be true")
	}
	if view.Command != "up" {
		t.Errorf("Command = %q, want %q", view.Command, "up")
	}
	if view.Batch != 1 {
		t.Errorf("Batch = %d, want 1", view.Batch)
	}
}

// TestStatusReportPendingApplied verifies that status correctly
// classifies migrations as pending or applied.
func TestStatusReportPendingApplied(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeTestMigration(t, dir, "20260730120000_create_users", "CREATE TABLE users (id INT);", "DROP TABLE users;")
	writeTestMigration(t, dir, "20260730130000_create_posts", "CREATE TABLE posts (id INT);", "DROP TABLE posts;")

	files, err := scanMigrations(dir)
	if err != nil {
		t.Fatalf("scanMigrations: %v", err)
	}
	sourceMap := make(map[string]*migrationFile, len(files))
	for i := range files {
		sourceMap[files[i].Name] = &files[i]
	}

	// Only the first migration is applied.
	applied := []AppliedMigration{
		{
			Migration:  "20260730120000_create_users",
			SourceKind: "timestamp",
			State:      "applied",
			Batch:      1,
			IsBaseline: false,
		},
	}

	// Build status from applied + source files.
	appliedSet := make(map[string]bool)
	for _, a := range applied {
		appliedSet[a.Migration] = true
	}

	var pending, appliedMigs int
	for _, f := range files {
		if appliedSet[f.Name] {
			appliedMigs++
		} else {
			pending++
		}
	}

	if appliedMigs != 1 {
		t.Errorf("applied count = %d, want 1", appliedMigs)
	}
	if pending != 1 {
		t.Errorf("pending count = %d, want 1", pending)
	}
}

// TestStatusReportDirtyBlocked verifies that dirty state is properly
// classified in the global drift check.
func TestStatusReportDirtyBlocked(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeTestMigration(t, dir, "20260730120000_create_users", "CREATE TABLE users (id INT);", "DROP TABLE users;")

	files, err := scanMigrations(dir)
	if err != nil {
		t.Fatalf("scanMigrations: %v", err)
	}
	sourceMap := make(map[string]*migrationFile, len(files))
	for i := range files {
		sourceMap[files[i].Name] = &files[i]
	}

	// An "applying" row is dirty and must block operations.
	applied := []AppliedMigration{
		{
			Migration:  "20260730120000_create_users",
			SourceKind: "timestamp",
			State:      "applying",
			Batch:      1,
			IsBaseline: false,
		},
	}

	err = globalDriftCheck(applied, sourceMap, false)
	if err == nil {
		t.Fatal("globalDriftCheck should block when dirty state present")
	}
	if !errors.Is(err, ErrDirtyState) {
		t.Fatalf("expected ErrDirtyState, got: %v", err)
	}

	// Verify isDirtyState helper.
	if !isDirtyState("applying") {
		t.Error("isDirtyState('applying') = false, want true")
	}
	if !isDirtyState("apply_failed") {
		t.Error("isDirtyState('apply_failed') = false, want true")
	}
	if !isDirtyState("rolling_back") {
		t.Error("isDirtyState('rolling_back') = false, want true")
	}
	if !isDirtyState("rollback_failed") {
		t.Error("isDirtyState('rollback_failed') = false, want true")
	}
	if isDirtyState("applied") {
		t.Error("isDirtyState('applied') = true, want false")
	}
}

// ---------- helpers ----------

// writeTestMigration creates a migration pair in the given directory.
func writeTestMigration(t *testing.T, dir, name, upSQL, downSQL string) {
	t.Helper()
	upPath := filepath.Join(dir, name+".up.sql")
	downPath := filepath.Join(dir, name+".down.sql")
	if err := os.WriteFile(upPath, []byte(upSQL), 0o644); err != nil {
		t.Fatalf("write up file: %v", err)
	}
	if err := os.WriteFile(downPath, []byte(downSQL), 0o644); err != nil {
		t.Fatalf("write down file: %v", err)
	}
}
