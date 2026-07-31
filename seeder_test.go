package lamigrate

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/go-sql-driver/mysql"
)

func testSeederMigrator(t *testing.T, maxFile int64) *Migrator {
	t.Helper()
	m, err := NewMySQL(&mysql.Config{
		User:   "test",
		Passwd: "test",
		Net:    "tcp",
		Addr:   "127.0.0.1:3306",
		DBName: "testdb",
	}, Options{Directory: t.TempDir(), MaxFileSize: maxFile})
	if err != nil {
		t.Fatalf("NewMySQL() error = %v", err)
	}
	return m
}

func TestPrepareSeedFilesOrdersAndSelectsClass(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	for name, sql := range map[string]string{
		"20_roles.sql": "SELECT 20;",
		"10_users.sql": "SELECT 10;",
		"notes.txt":    "ignored",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(sql), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	m := testSeederMigrator(t, 0)

	files, class, err := m.prepareSeedFiles(SeedRequest{Directory: dir})
	if err != nil {
		t.Fatalf("prepareSeedFiles() error = %v", err)
	}
	if class != "" {
		t.Fatalf("class = %q, want empty", class)
	}
	got := []string{files[0].name, files[1].name}
	if want := []string{"10_users.sql", "20_roles.sql"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("files = %v, want %v", got, want)
	}

	files, class, err = m.prepareSeedFiles(SeedRequest{Directory: dir, Class: "20_roles"})
	if err != nil {
		t.Fatalf("prepareSeedFiles(--class) error = %v", err)
	}
	if class != "20_roles" || len(files) != 1 || files[0].name != "20_roles.sql" {
		t.Fatalf("class=%q files=%v, want 20_roles.sql only", class, files)
	}
}

func TestPrepareSeedFilesRejectsUnsafeOrInvalidInput(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	m := testSeederMigrator(t, 8)
	if err := os.WriteFile(filepath.Join(dir, "large.sql"), []byte("SELECT 100;"), 0o600); err != nil {
		t.Fatal(err)
	}

	for _, request := range []SeedRequest{
		{Directory: ""},
		{Directory: dir, Class: "../escape"},
		{Directory: dir},
	} {
		if _, _, err := m.prepareSeedFiles(request); err == nil {
			t.Errorf("prepareSeedFiles(%+v) unexpectedly succeeded", request)
		}
	}

	if err := os.WriteFile(filepath.Join(dir, "empty.sql"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := m.prepareSeedFiles(SeedRequest{Directory: dir, Class: "empty"}); err == nil {
		t.Fatal("empty seed file unexpectedly accepted")
	}
}

func TestNormalizeSeederClass(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		input string
		want  string
		err   bool
	}{
		{"", "", false},
		{"DatabaseSeeder", "DatabaseSeeder", false},
		{"users.sql", "users", false},
		{"bad-name", "", true},
		{"../users", "", true},
	} {
		got, err := normalizeSeederClass(test.input)
		if test.err {
			if err == nil {
				t.Errorf("normalizeSeederClass(%q) unexpectedly succeeded", test.input)
			}
			continue
		}
		if err != nil || got != test.want {
			t.Errorf("normalizeSeederClass(%q) = %q, %v; want %q, nil", test.input, got, err, test.want)
		}
	}
}
