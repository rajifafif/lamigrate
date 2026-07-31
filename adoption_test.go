package lamigrate

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// ---------- TestDetectPrototypeShape_Verifies4Columns ----------
//
// detectPrototypeShape queries the database directly and cannot be unit-tested
// without a live connection. Instead, we verify the prototype column definitions
// used by the detection logic by testing that validatePrototypeRows and the
// regex constants accept exactly the patterns the prototype uses.

func TestDetectPrototypeShape_Verifies4Columns(t *testing.T) {
	t.Parallel()

	// The prototype migration values that mapSourceFiles accepts follow
	// these two regexes — ensure they cover the prototype's expected formats.
	validTimestamps := []string{
		"20260730094235_create_users",
		"20000101000000_a",
		"20991231235959_very_long_migration_name_here",
	}
	for _, ts := range validTimestamps {
		if !timestampMigrationID.MatchString(ts) {
			t.Errorf("timestampMigrationID should match %q", ts)
		}
	}

	validNumerics := []string{
		"1",
		"42",
		"18446744073709551614",
	}
	for _, n := range validNumerics {
		if !numericMigrationID.MatchString(n) {
			t.Errorf("numericMigrationID should match %q", n)
		}
	}

	// The prototype columns are: id, migration, batch, applied_at.
	// Verify that a row with these fields passes validation.
	rows := []prototypeRow{
		{ID: 1, Migration: "20260730094235_create_users", Batch: 1, AppliedAt: time.Now()},
	}
	if err := validatePrototypeRows(rows); err != nil {
		t.Errorf("validatePrototypeRows with valid prototype row: %v", err)
	}
}

// ---------- validatePrototypeRows tests ----------

func TestValidatePrototypeRows_RequiresAtLeastOneRow(t *testing.T) {
	t.Parallel()
	err := validatePrototypeRows(nil)
	if err == nil {
		t.Fatal("expected error for nil rows, got nil")
	}
	if !errors.Is(err, ErrInvalidConfig) {
		t.Errorf("expected ErrInvalidConfig, got %v", err)
	}

	err = validatePrototypeRows([]prototypeRow{})
	if err == nil {
		t.Fatal("expected error for empty slice, got nil")
	}
	if !errors.Is(err, ErrInvalidConfig) {
		t.Errorf("expected ErrInvalidConfig, got %v", err)
	}
}

func TestValidatePrototypeRows_AcceptsValidRows(t *testing.T) {
	t.Parallel()
	rows := []prototypeRow{
		{ID: 1, Migration: "20260730094235_create_users", Batch: 1, AppliedAt: time.Now()},
		{ID: 2, Migration: "20260730100000_add_email_to_users", Batch: 1, AppliedAt: time.Now()},
		{ID: 3, Migration: "0", Batch: 0, AppliedAt: time.Now()},
	}
	if err := validatePrototypeRows(rows); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidatePrototypeRows_RejectsMaxID(t *testing.T) {
	t.Parallel()
	rows := []prototypeRow{
		{ID: 18446744073709551615, Migration: "20260730094235_create_users", Batch: 1, AppliedAt: time.Now()},
	}
	err := validatePrototypeRows(rows)
	if err == nil {
		t.Fatal("expected error for MAX(id), got nil")
	}
	if !errors.Is(err, ErrInvalidConfig) {
		t.Errorf("expected ErrInvalidConfig, got %v", err)
	}
}

func TestValidatePrototypeRows_RejectsDuplicateMigration(t *testing.T) {
	t.Parallel()
	rows := []prototypeRow{
		{ID: 1, Migration: "20260730094235_create_users", Batch: 1, AppliedAt: time.Now()},
		{ID: 2, Migration: "20260730094235_create_users", Batch: 1, AppliedAt: time.Now()},
	}
	err := validatePrototypeRows(rows)
	if err == nil {
		t.Fatal("expected error for duplicate migration, got nil")
	}
	if !errors.Is(err, ErrInvalidConfig) {
		t.Errorf("expected ErrInvalidConfig, got %v", err)
	}
}

func TestValidatePrototypeRows_AcceptsNonSequentialIDs(t *testing.T) {
	t.Parallel()
	rows := []prototypeRow{
		{ID: 1, Migration: "20260730094235_create_users", Batch: 1, AppliedAt: time.Now()},
		{ID: 5, Migration: "20260730100000_add_email_to_users", Batch: 1, AppliedAt: time.Now()},
		{ID: 10, Migration: "20260730120000_create_posts", Batch: 2, AppliedAt: time.Now()},
	}
	if err := validatePrototypeRows(rows); err != nil {
		t.Errorf("unexpected error for non-sequential IDs: %v", err)
	}
}

// ---------- mapSourceFiles tests ----------

// writeMigrationFile creates a migration file pair in dir and returns
// the SHA-256 checksums of the contents.
func writeMigrationFile(t *testing.T, dir, base, upContent, downContent string) (upCS, downCS [32]byte) {
	t.Helper()
	upPath := filepath.Join(dir, base+".up.sql")
	downPath := filepath.Join(dir, base+".down.sql")
	if err := os.WriteFile(upPath, []byte(upContent), 0o644); err != nil {
		t.Fatalf("write up file: %v", err)
	}
	if err := os.WriteFile(downPath, []byte(downContent), 0o644); err != nil {
		t.Fatalf("write down file: %v", err)
	}
	var err error
	upCS, err = checksumFile(upPath)
	if err != nil {
		t.Fatalf("checksum up file: %v", err)
	}
	downCS, err = checksumFile(downPath)
	if err != nil {
		t.Fatalf("checksum down file: %v", err)
	}
	return upCS, downCS
}

func TestMapSourceFiles_PositiveBatch_TimestampFiles(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	appliedAt := time.Date(2026, 7, 30, 9, 42, 35, 0, time.UTC)

	upContent := "CREATE TABLE `users` (\n    `id` BIGINT UNSIGNED NOT NULL AUTO_INCREMENT\n);"
	downContent := "DROP TABLE IF EXISTS `users`;"

	upCS, downCS := writeMigrationFile(t, dir, "20260730094235_create_users", upContent, downContent)

	rows := []prototypeRow{
		{ID: 1, Migration: "20260730094235_create_users", Batch: 1, AppliedAt: appliedAt},
	}

	result, err := mapSourceFiles(rows, dir, "")
	if err != nil {
		t.Fatalf("mapSourceFiles: %v", err)
	}
	if len(result) != 1 {
		t.Fatalf("expected 1 mapping, got %d", len(result))
	}

	sm := result[0]
	if sm.PrototypeID != 1 {
		t.Errorf("PrototypeID = %d, want 1", sm.PrototypeID)
	}
	if sm.Migration != "20260730094235_create_users" {
		t.Errorf("Migration = %q, want %q", sm.Migration, "20260730094235_create_users")
	}
	if sm.Batch != 1 {
		t.Errorf("Batch = %d, want 1", sm.Batch)
	}
	if sm.SourceKind != "timestamp" {
		t.Errorf("SourceKind = %q, want %q", sm.SourceKind, "timestamp")
	}
	if sm.SourceName != "20260730094235_create_users" {
		t.Errorf("SourceName = %q, want %q", sm.SourceName, "20260730094235_create_users")
	}
	if sm.UpPath != filepath.Join(dir, "20260730094235_create_users.up.sql") {
		t.Errorf("UpPath = %q", sm.UpPath)
	}
	if sm.DownPath != filepath.Join(dir, "20260730094235_create_users.down.sql") {
		t.Errorf("DownPath = %q", sm.DownPath)
	}
	if sm.UpChecksum != upCS {
		t.Errorf("UpChecksum mismatch")
	}
	if sm.DownChecksum != downCS {
		t.Errorf("DownChecksum mismatch")
	}
}

func TestMapSourceFiles_BatchZero_LegacyFiles(t *testing.T) {
	t.Parallel()
	legacyDir := t.TempDir()
	appliedAt := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)

	upContent := "CREATE TABLE `users` (\n    `id` INT\n);"
	downContent := "DROP TABLE IF EXISTS `users`;"

	upCS, downCS := writeMigrationFile(t, legacyDir, "001_create_users", upContent, downContent)

	rows := []prototypeRow{
		{ID: 1, Migration: "1", Batch: 0, AppliedAt: appliedAt},
	}

	result, err := mapSourceFiles(rows, "", legacyDir)
	if err != nil {
		t.Fatalf("mapSourceFiles: %v", err)
	}
	if len(result) != 1 {
		t.Fatalf("expected 1 mapping, got %d", len(result))
	}

	sm := result[0]
	if sm.PrototypeID != 1 {
		t.Errorf("PrototypeID = %d, want 1", sm.PrototypeID)
	}
	if sm.Migration != "1" {
		t.Errorf("Migration = %q, want %q", sm.Migration, "1")
	}
	if sm.Batch != 0 {
		t.Errorf("Batch = %d, want 0", sm.Batch)
	}
	if sm.SourceKind != "golang_migrate" {
		t.Errorf("SourceKind = %q, want %q", sm.SourceKind, "golang_migrate")
	}
	if sm.SourceName != "001_create_users" {
		t.Errorf("SourceName = %q, want %q", sm.SourceName, "001_create_users")
	}
	if sm.UpPath != filepath.Join(legacyDir, "001_create_users.up.sql") {
		t.Errorf("UpPath = %q", sm.UpPath)
	}
	if sm.DownPath != filepath.Join(legacyDir, "001_create_users.down.sql") {
		t.Errorf("DownPath = %q", sm.DownPath)
	}
	if sm.UpChecksum != upCS {
		t.Errorf("UpChecksum mismatch")
	}
	if sm.DownChecksum != downCS {
		t.Errorf("DownChecksum mismatch")
	}
}

func TestMapSourceFiles_RejectsMissingSource(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	// Reference a migration that does not exist on disk.
	rows := []prototypeRow{
		{ID: 1, Migration: "20260730094235_create_users", Batch: 1, AppliedAt: time.Now()},
	}

	_, err := mapSourceFiles(rows, dir, "")
	if err == nil {
		t.Fatal("expected error for missing source file, got nil")
	}
	if !errors.Is(err, ErrInvalidConfig) {
		t.Errorf("expected ErrInvalidConfig, got %v", err)
	}
}

func TestMapSourceFiles_RejectsAmbiguousLegacy(t *testing.T) {
	t.Parallel()
	legacyDir := t.TempDir()

	// Two different descriptions for the same version: ambiguous.
	writeMigrationFile(t, legacyDir, "001_create_users", "CREATE TABLE x;", "DROP TABLE x;")
	writeMigrationFile(t, legacyDir, "001_create_posts", "CREATE TABLE y;", "DROP TABLE y;")

	rows := []prototypeRow{
		{ID: 1, Migration: "1", Batch: 0, AppliedAt: time.Now()},
	}

	_, err := mapSourceFiles(rows, "", legacyDir)
	if err == nil {
		t.Fatal("expected error for ambiguous legacy, got nil")
	}
	if !errors.Is(err, ErrInvalidConfig) {
		t.Errorf("expected ErrInvalidConfig, got %v", err)
	}
}

func TestMapSourceFiles_SortsByPrototypeID(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	writeMigrationFile(t, dir, "20260730120000_create_posts", "CREATE TABLE posts;", "DROP TABLE posts;")
	writeMigrationFile(t, dir, "20260730094235_create_users", "CREATE TABLE users;", "DROP TABLE users;")
	writeMigrationFile(t, dir, "20260730100000_add_email", "ALTER TABLE users ADD email VARCHAR(255);", "ALTER TABLE users DROP email;")

	// Intentionally out of order.
	rows := []prototypeRow{
		{ID: 10, Migration: "20260730120000_create_posts", Batch: 2, AppliedAt: time.Now()},
		{ID: 1, Migration: "20260730094235_create_users", Batch: 1, AppliedAt: time.Now()},
		{ID: 5, Migration: "20260730100000_add_email", Batch: 1, AppliedAt: time.Now()},
	}

	result, err := mapSourceFiles(rows, dir, "")
	if err != nil {
		t.Fatalf("mapSourceFiles: %v", err)
	}
	if len(result) != 3 {
		t.Fatalf("expected 3 mappings, got %d", len(result))
	}

	// Must be sorted by ascending prototype ID.
	ids := []uint64{result[0].PrototypeID, result[1].PrototypeID, result[2].PrototypeID}
	if ids[0] != 1 || ids[1] != 5 || ids[2] != 10 {
		t.Errorf("results not sorted by PrototypeID: %v", ids)
	}
}

func TestMapSourceFiles_RejectsInvalidTimestampFormat(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	// batch > 0 but migration is not a valid timestamp ID.
	rows := []prototypeRow{
		{ID: 1, Migration: "not_a_timestamp_migration", Batch: 1, AppliedAt: time.Now()},
	}

	_, err := mapSourceFiles(rows, dir, "")
	if err == nil {
		t.Fatal("expected error for invalid timestamp format, got nil")
	}
	if !errors.Is(err, ErrInvalidConfig) {
		t.Errorf("expected ErrInvalidConfig, got %v", err)
	}
}

func TestMapSourceFiles_RejectsInvalidNumericFormat(t *testing.T) {
	t.Parallel()
	legacyDir := t.TempDir()

	// batch = 0 but migration is not a valid numeric version.
	rows := []prototypeRow{
		{ID: 1, Migration: "abc", Batch: 0, AppliedAt: time.Now()},
	}

	_, err := mapSourceFiles(rows, "", legacyDir)
	if err == nil {
		t.Fatal("expected error for invalid numeric format, got nil")
	}
	if !errors.Is(err, ErrInvalidConfig) {
		t.Errorf("expected ErrInvalidConfig, got %v", err)
	}
}

// ---------- validateBackupTableName tests ----------

func TestValidateBackupTableName_AcceptsValid(t *testing.T) {
	t.Parallel()
	valid := []string{
		"backup_20260730",
		"migrations_backup",
		"a",
		"my_backup_table_123",
		"_backup",
	}
	for _, name := range valid {
		name := name
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if err := validateBackupTableName(name, "migrations"); err != nil {
				t.Errorf("validateBackupTableName(%q, %q) unexpected error: %v", name, "migrations", err)
			}
		})
	}
}

func TestValidateBackupTableName_RejectsReserved(t *testing.T) {
	t.Parallel()
	err := validateBackupTableName("lamigrate_control", "migrations")
	if err == nil {
		t.Fatal("expected error for reserved name, got nil")
	}
	if !errors.Is(err, ErrInvalidConfig) {
		t.Errorf("expected ErrInvalidConfig, got %v", err)
	}
}

func TestValidateBackupTableName_RejectsSameAsTracking(t *testing.T) {
	t.Parallel()
	err := validateBackupTableName("migrations", "migrations")
	if err == nil {
		t.Fatal("expected error when backup name equals tracking table, got nil")
	}
	if !errors.Is(err, ErrInvalidConfig) {
		t.Errorf("expected ErrInvalidConfig, got %v", err)
	}
}

func TestValidateBackupTableName_RejectsEmpty(t *testing.T) {
	t.Parallel()
	err := validateBackupTableName("", "migrations")
	if err == nil {
		t.Fatal("expected error for empty name, got nil")
	}
	if !errors.Is(err, ErrInvalidConfig) {
		t.Errorf("expected ErrInvalidConfig, got %v", err)
	}
}

func TestValidateBackupTableName_RejectsUppercase(t *testing.T) {
	t.Parallel()
	names := []string{"Backup", "BACKUP", "my_Backup"}
	for _, name := range names {
		name := name
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			err := validateBackupTableName(name, "migrations")
			if err == nil {
				t.Errorf("expected error for uppercase name %q, got nil", name)
			}
			if !errors.Is(err, ErrInvalidConfig) {
				t.Errorf("expected ErrInvalidConfig for %q, got %v", name, err)
			}
		})
	}
}
