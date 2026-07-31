//go:build integration

package integration

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/rajifafif/lamigrate"
)

func TestSeedExecutesOrderedSQLWithoutMigrationMetadata(t *testing.T) {
	tb := newTestDB(t)
	tb.requireTestDBName()

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "010_insert_second.sql"), []byte("INSERT INTO seeded_values (value) VALUES ('second');"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "001_create_table.sql"), []byte("CREATE TABLE seeded_values (value VARCHAR(32) NOT NULL); INSERT INTO seeded_values (value) VALUES ('first');"), 0o600); err != nil {
		t.Fatal(err)
	}

	m, err := lamigrate.OpenMySQL(tb.DSN, lamigrate.Options{Directory: filepath.Join(t.TempDir(), "migrations")})
	if err != nil {
		t.Fatalf("OpenMySQL() error = %v", err)
	}
	result, err := m.Seed(context.Background(), lamigrate.SeedRequest{Directory: dir})
	if err != nil {
		t.Fatalf("Seed() error = %v", err)
	}
	if len(result.Seeded) != 2 || result.Seeded[0].Name != "001_create_table.sql" || result.Seeded[1].Name != "010_insert_second.sql" {
		t.Fatalf("Seeded = %+v, want ordered pair", result.Seeded)
	}

	var count int
	if err := tb.QueryRow("SELECT COUNT(*) FROM seeded_values").Scan(&count); err != nil {
		t.Fatalf("count seeded values: %v", err)
	}
	if count != 2 {
		t.Fatalf("seeded_values count = %d, want 2", count)
	}
	if err := tb.QueryRow("SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = ? AND table_name = 'migrations'", tb.Name).Scan(&count); err != nil {
		t.Fatalf("inspect migrations metadata: %v", err)
	}
	if count != 0 {
		t.Fatalf("seed should not create migrations metadata table; found %d", count)
	}
}

func TestSeedClassSelectsOneSQLFile(t *testing.T) {
	tb := newTestDB(t)
	tb.requireTestDBName()

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "RolesSeeder.sql"), []byte("CREATE TABLE roles_seeded (name VARCHAR(32) NOT NULL); INSERT INTO roles_seeded (name) VALUES ('admin');"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "UsersSeeder.sql"), []byte("SELECT invalid SQL"), 0o600); err != nil {
		t.Fatal(err)
	}

	m, err := lamigrate.OpenMySQL(tb.DSN, lamigrate.Options{Directory: filepath.Join(t.TempDir(), "migrations")})
	if err != nil {
		t.Fatalf("OpenMySQL() error = %v", err)
	}
	result, err := m.Seed(context.Background(), lamigrate.SeedRequest{Directory: dir, Class: "RolesSeeder"})
	if err != nil {
		t.Fatalf("Seed() error = %v", err)
	}
	if len(result.Seeded) != 1 || result.Seeded[0].Name != "RolesSeeder.sql" {
		t.Fatalf("Seeded = %+v, want RolesSeeder.sql only", result.Seeded)
	}
}
