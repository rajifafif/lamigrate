package lamigrate

import (
	"os"
	"path/filepath"
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
