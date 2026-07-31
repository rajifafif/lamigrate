package lamigrate

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestNormalizeMigrationName(t *testing.T) {
	t.Parallel()

	tests := []struct {
		input string
		want  string
	}{
		{"create users table", "create_users_table"},
		{"Create-Users-Table", "create_users_table"},
		{"create__users___table.up.sql", "create_users_table"},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.input, func(t *testing.T) {
			t.Parallel()
			got, err := normalizeMigrationName(tt.input)
			if err != nil {
				t.Fatalf("normalizeMigrationName() error = %v", err)
			}
			if got != tt.want {
				t.Fatalf("normalizeMigrationName() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestNormalizeMigrationNameRejectsUnsafeNames(t *testing.T) {
	t.Parallel()

	for _, input := range []string{"", "../escape", "create/users", "_private", "create;drop"} {
		if _, err := normalizeMigrationName(input); err == nil {
			t.Errorf("normalizeMigrationName(%q) unexpectedly succeeded", input)
		}
	}
}

func TestCreateMigrationCreateTableTemplate(t *testing.T) {
	t.Parallel()

	dir := filepath.Join(t.TempDir(), "nested", "migrations")
	now := time.Date(2026, time.July, 30, 12, 30, 45, 0, time.FixedZone("test", 7*60*60))
	created, err := createMigrationAt(dir, "Create Users Table", now)
	if err != nil {
		t.Fatalf("createMigrationAt() error = %v", err)
	}

	if created.Base != "20260730053045_create_users_table" {
		t.Fatalf("Base = %q", created.Base)
	}
	if created.Template != "create_table" {
		t.Fatalf("Template = %q", created.Template)
	}

	up := readTestFile(t, created.UpPath)
	down := readTestFile(t, created.DownPath)
	for _, want := range []string{
		"CREATE TABLE `users`",
		"`id` BIGINT UNSIGNED NOT NULL AUTO_INCREMENT",
		"`created_at` TIMESTAMP NULL DEFAULT NULL",
		"ENGINE=InnoDB DEFAULT CHARSET=utf8mb4",
	} {
		if !strings.Contains(up, want) {
			t.Errorf("up migration missing %q:\n%s", want, up)
		}
	}
	if !strings.Contains(down, "DROP TABLE IF EXISTS `users`;") {
		t.Fatalf("down migration is not inferred correctly:\n%s", down)
	}
	if strings.Contains(up, "SIGNAL SQLSTATE") || strings.Contains(down, "SIGNAL SQLSTATE") {
		t.Fatal("create-table template must be immediately runnable")
	}
}

func TestCreateMigrationGuardedAddColumnTemplate(t *testing.T) {
	t.Parallel()

	created, err := createMigrationAt(t.TempDir(), "add_email_to_users_table", fixedTestTime())
	if err != nil {
		t.Fatalf("createMigrationAt() error = %v", err)
	}
	if created.Template != "add_column" {
		t.Fatalf("Template = %q", created.Template)
	}

	up := readTestFile(t, created.UpPath)
	down := readTestFile(t, created.DownPath)
	if !strings.Contains(up, "SIGNAL SQLSTATE '45000'") || !strings.Contains(down, "SIGNAL SQLSTATE '45000'") {
		t.Fatal("inferred alter templates must fail closed until reviewed")
	}
	if !strings.Contains(up, "ALTER TABLE `users` ADD COLUMN `email` VARCHAR(255) NULL;") {
		t.Fatalf("up suggestion missing:\n%s", up)
	}
	if !strings.Contains(down, "ALTER TABLE `users` DROP COLUMN `email`;") {
		t.Fatalf("down suggestion missing:\n%s", down)
	}
}

func TestDropColumnTemplateFailsClosed(t *testing.T) {
	t.Parallel()

	created, err := createMigrationAt(t.TempDir(), "drop_legacy_code_from_users_table", fixedTestTime())
	if err != nil {
		t.Fatal(err)
	}
	up := readTestFile(t, created.UpPath)
	down := readTestFile(t, created.DownPath)
	if !strings.Contains(up, "ALTER TABLE `users` DROP COLUMN `legacy_code`;") ||
		!strings.Contains(down, "ALTER TABLE `users` ADD COLUMN `legacy_code` VARCHAR(255) NULL;") {
		t.Fatalf("drop-column templates incorrect:\nUP:\n%s\nDOWN:\n%s", up, down)
	}
	if !strings.Contains(up, "SIGNAL SQLSTATE '45000'") || !strings.Contains(down, "SIGNAL SQLSTATE '45000'") {
		t.Fatal("drop-column template must fail closed")
	}
}

func TestCreateMigrationGenericTemplateFailsClosedBothWays(t *testing.T) {
	t.Parallel()

	created, err := createMigrationAt(t.TempDir(), "backfill_user_slugs", fixedTestTime())
	if err != nil {
		t.Fatalf("createMigrationAt() error = %v", err)
	}
	if created.Template != "generic" {
		t.Fatalf("Template = %q", created.Template)
	}
	for _, path := range []string{created.UpPath, created.DownPath} {
		if sql := readTestFile(t, path); !strings.Contains(sql, "SIGNAL SQLSTATE '45000'") {
			t.Fatalf("generic migration does not fail closed in %s:\n%s", path, sql)
		}
	}
}

func TestCreateMigrationRejectsSameTimestampWithoutOverwrite(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	first, err := createMigrationAt(dir, "create_users_table", fixedTestTime())
	if err != nil {
		t.Fatalf("first createMigrationAt() error = %v", err)
	}
	original := readTestFile(t, first.UpPath)

	if _, err := createMigrationAt(dir, "create_posts_table", fixedTestTime()); err == nil {
		t.Fatal("second migration with same timestamp unexpectedly succeeded")
	}
	if got := readTestFile(t, first.UpPath); got != original {
		t.Fatal("existing migration was modified after collision")
	}
}

func TestCreateMigrationConcurrentSameTimestamp(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for _, name := range []string{"create_users_table", "create_posts_table"} {
		name := name
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := createMigrationAt(dir, name, fixedTestTime())
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)

	succeeded, failed := 0, 0
	for err := range errs {
		if err == nil {
			succeeded++
		} else {
			failed++
		}
	}
	if succeeded != 1 || failed != 1 {
		t.Fatalf("concurrent results: succeeded=%d failed=%d", succeeded, failed)
	}
	files, err := filepath.Glob(filepath.Join(dir, "*.sql"))
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 2 {
		t.Fatalf("published SQL files = %d, want 2: %v", len(files), files)
	}
}

func TestScanMigrationsRejectsMissingDownFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	up := filepath.Join(dir, "20260730010203_create_users_table.up.sql")
	if err := os.WriteFile(up, []byte("SELECT 1;"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := scanMigrations(dir); err == nil {
		t.Fatal("up-only migration unexpectedly discovered")
	}
}

func TestScanMigrationsIgnoresDownOnlyOrphan(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	down := filepath.Join(dir, "20260730010203_create_users_table.down.sql")
	if err := os.WriteFile(down, []byte("DROP TABLE users;"), 0o644); err != nil {
		t.Fatal(err)
	}
	files, err := scanMigrations(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 0 {
		t.Fatalf("down-only orphan was discovered: %v", files)
	}
}

func TestPublishedPairIsDiscoverableOnlyWhenComplete(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	created, err := createMigrationAt(dir, "create_users_table", fixedTestTime())
	if err != nil {
		t.Fatal(err)
	}
	files, err := scanMigrations(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || files[0].UpPath != created.UpPath || files[0].DownPath != created.DownPath {
		t.Fatalf("discovered files = %#v, created = %#v", files, created)
	}
}

func TestCreateMigrationRejectsSymlinkDirectory(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	realDir := filepath.Join(root, "real")
	if err := os.Mkdir(realDir, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(realDir, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, err := createMigrationAt(link, "create_users_table", fixedTestTime()); err == nil {
		t.Fatal("symlink migration directory unexpectedly accepted")
	}
}

func TestCreateMigrationRejectsSymlinkAncestor(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	realDir := filepath.Join(root, "real")
	if err := os.Mkdir(realDir, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(realDir, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, err := createMigrationAt(filepath.Join(link, "migrations"), "create_users_table", fixedTestTime()); err == nil {
		t.Fatal("symlink ancestor unexpectedly accepted")
	}
}

func fixedTestTime() time.Time {
	return time.Date(2026, 7, 30, 1, 2, 3, 0, time.UTC)
}

func readTestFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}

// --- LM-011 tests: checksums, validation, irreversible marker ---

func TestChecksumFile(t *testing.T) {
	t.Parallel()
	content := []byte("SELECT 1;")
	path := filepath.Join(t.TempDir(), "test.sql")
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatal(err)
	}
	sum, err := checksumFile(path)
	if err != nil {
		t.Fatalf("checksumFile() error = %v", err)
	}
	// Verify against known SHA-256 of "SELECT 1;"
	expected := checksumBytes(content)
	if sum != expected {
		t.Fatalf("checksumFile() = %s, want %s", checksumHex(sum), checksumHex(expected))
	}
}

func TestChecksumBytes(t *testing.T) {
	t.Parallel()
	sum := checksumBytes([]byte("hello"))
	// Known SHA-256 of "hello"
	expected := "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824"
	if got := checksumHex(sum); got != expected {
		t.Fatalf("checksumBytes(\"hello\") = %s, want %s", got, expected)
	}
}

func TestValidateMigrationName(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		wantErr bool
	}{
		{"20260730120000_create_users.up.sql", false},   // valid
		{"20260730120000_create_users.down.sql", false}, // valid
		{"not_a_timestamp_up.sql", true},                // no timestamp
		{"1234567890abc_.up.sql", true},                 // only 10 digits before non-digit
		{"20260730120000_a.up.sql", false},              // single char desc, valid
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := validateFilename(tt.name)
			if (err != nil) != tt.wantErr {
				t.Errorf("validateFilename(%q) error = %v, wantErr %v", tt.name, err, tt.wantErr)
			}
		})
	}
}

func TestValidateFilenameAccepts(t *testing.T) {
	t.Parallel()
	valid := []string{
		"20260730120000_create_users.up.sql",
		"20260730120000_create_users.down.sql",
		"20000101000000_a.up.sql",
		"20991231235959_x_y_z.up.sql",
	}
	for _, name := range valid {
		if err := validateFilename(name); err != nil {
			t.Errorf("validateFilename(%q) unexpected error: %v", name, err)
		}
	}
}

func TestValidateFilenameRejects(t *testing.T) {
	t.Parallel()
	invalid := []string{
		"",                                    // empty
		"create_users.up.sql",                 // no timestamp
		"20260730120000_create_users.sql",     // no up/down
		"20260730120000_create_users.up.sqlx", // wrong extension
		"20260730120000_Create_Users.up.sql",  // uppercase
		"20260730120000_create-users.up.sql",  // hyphen in name
		"202607301200001_create_users.up.sql", // 15 digits
		"2026073012000_create_users.up.sql",   // 13 digits
	}
	for _, name := range invalid {
		if err := validateFilename(name); err == nil {
			t.Errorf("validateFilename(%q) expected error but got nil", name)
		}
	}
}

func TestValidateTimestamp(t *testing.T) {
	t.Parallel()
	tests := []struct {
		ts      int64
		wantErr bool
	}{
		{20260730120000, false}, // valid
		{20000101000000, false}, // min bound
		{20991231235959, false}, // near max bound
		{19991231235959, true},  // before 2000
		{21010101000000, true},  // after 2100
		{20260000000000, true},  // month 00
		{20261300000000, true},  // month 13
		{20260132000000, true},  // day 32
		{20260230000000, true},  // Feb 30
		{20260101240000, true},  // hour 24
		{20260101126100, true},  // minute 61
		{20260101120061, true},  // second 61
	}
	for _, tt := range tests {
		tt := tt
		t.Run(strconv.FormatInt(tt.ts, 10), func(t *testing.T) {
			t.Parallel()
			err := validateTimestamp(tt.ts)
			if (err != nil) != tt.wantErr {
				t.Errorf("validateTimestamp(%d) error = %v, wantErr %v", tt.ts, err, tt.wantErr)
			}
		})
	}
}

func TestDetectIrreversibleMarker(t *testing.T) {
	t.Parallel()

	t.Run("irreversible", func(t *testing.T) {
		t.Parallel()
		path := filepath.Join(t.TempDir(), "down.sql")
		content := "-- lamigrate: irreversible\n-- reason: data transformation cannot be safely reversed\n"
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		got, err := detectIrreversible(path)
		if err != nil {
			t.Fatalf("detectIrreversible() error = %v", err)
		}
		if !got {
			t.Fatal("detectIrreversible() = false, want true")
		}
	})

	t.Run("reversible", func(t *testing.T) {
		t.Parallel()
		path := filepath.Join(t.TempDir(), "down.sql")
		content := "DROP TABLE IF EXISTS users;\n"
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		got, err := detectIrreversible(path)
		if err != nil {
			t.Fatalf("detectIrreversible() error = %v", err)
		}
		if got {
			t.Fatal("detectIrreversible() = true, want false")
		}
	})

	t.Run("marker_in_middle", func(t *testing.T) {
		t.Parallel()
		path := filepath.Join(t.TempDir(), "down.sql")
		content := "-- Rollback: test\n-- lamigrate: irreversible\nDROP TABLE x;\n"
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		got, err := detectIrreversible(path)
		if err != nil {
			t.Fatalf("detectIrreversible() error = %v", err)
		}
		if !got {
			t.Fatal("detectIrreversible() = false, want true")
		}
	})
}

func TestRejectDuplicateIDs(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	// Create two pairs with the same ID but different filenames (both up files).
	base := "20260730120000_create_users"
	up1 := base + ".up.sql"
	down1 := base + ".down.sql"
	if err := os.WriteFile(filepath.Join(dir, up1), []byte("SELECT 1;"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, down1), []byte("SELECT 1;"), 0o644); err != nil {
		t.Fatal(err)
	}

	files, err := scanMigrations(dir)
	if err != nil {
		t.Fatalf("scanMigrations() error = %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("expected 1 file, got %d", len(files))
	}

	// Now test validateSourceFiles with a synthetic duplicate.
	dup := migrationFile{Name: base, Filename: up1, Timestamp: 20260730120000}
	all := []migrationFile{files[0], dup}
	err = validateSourceFiles(all, 0)
	if err == nil {
		t.Fatal("validateSourceFiles() expected error for duplicate ID, got nil")
	}
}

func TestFileSizeLimit(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	bigContent := strings.Repeat("X", 2048) // 2KB

	// Create a migration pair with a large up file.
	upPath := filepath.Join(dir, "20260730120000_create_big.up.sql")
	downPath := filepath.Join(dir, "20260730120000_create_big.down.sql")
	if err := os.WriteFile(upPath, []byte(bigContent), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(downPath, []byte("DROP TABLE big;"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Should fail with a 1KB limit.
	err := validateFileSize(upPath, 1024)
	if err == nil {
		t.Fatal("validateFileSize() expected error for oversized file, got nil")
	}

	// Should pass with a 4KB limit.
	err = validateFileSize(upPath, 4096)
	if err != nil {
		t.Fatalf("validateFileSize() unexpected error with 4KB limit: %v", err)
	}
}

func TestRejectFutureTimestamp(t *testing.T) {
	t.Parallel()
	ts := int64(21010101000000) // 2101
	if err := validateTimestamp(ts); err == nil {
		t.Errorf("validateTimestamp(%d) expected error for future timestamp", ts)
	}
}

func TestRejectOldTimestamp(t *testing.T) {
	t.Parallel()
	ts := int64(19991231235959) // 1999
	if err := validateTimestamp(ts); err == nil {
		t.Errorf("validateTimestamp(%d) expected error for old timestamp", ts)
	}
}

func TestScanMigrationsPopulatesChecksums(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	upContent := []byte("CREATE TABLE t1 (id INT);")
	downContent := []byte("DROP TABLE IF EXISTS t1;")
	upPath := filepath.Join(dir, "20260730120000_create_t1.up.sql")
	downPath := filepath.Join(dir, "20260730120000_create_t1.down.sql")
	if err := os.WriteFile(upPath, upContent, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(downPath, downContent, 0o644); err != nil {
		t.Fatal(err)
	}

	files, err := scanMigrations(dir)
	if err != nil {
		t.Fatalf("scanMigrations() error = %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("expected 1 file, got %d", len(files))
	}

	f := files[0]
	expectedUp := checksumBytes(upContent)
	expectedDown := checksumBytes(downContent)
	if f.UpChecksum != expectedUp {
		t.Errorf("UpChecksum = %s, want %s", checksumHex(f.UpChecksum), checksumHex(expectedUp))
	}
	if f.DownChecksum != expectedDown {
		t.Errorf("DownChecksum = %s, want %s", checksumHex(f.DownChecksum), checksumHex(expectedDown))
	}
}
