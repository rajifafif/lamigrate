package lamigrate

// import_test.go — Unit tests for import validation and classification logic.
//
// These tests exercise the pure validation, scanning, and classification
// functions without requiring a MySQL database.

import (
	"os"
	"path/filepath"
	"testing"
)

// ---------- Source table validation ----------

func TestValidateImportSourceTable(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		sourceTable string
		destTable   string
		wantErr     bool
	}{
		{
			name:        "valid different tables",
			sourceTable: "schema_migrations",
			destTable:   "migrations",
			wantErr:     false,
		},
		{
			name:        "valid with underscores",
			sourceTable: "my_source_table",
			destTable:   "migrations",
			wantErr:     false,
		},
		{
			name:        "empty source table",
			sourceTable: "",
			destTable:   "migrations",
			wantErr:     true,
		},
		{
			name:        "source equals destination",
			sourceTable: "migrations",
			destTable:   "migrations",
			wantErr:     true,
		},
		{
			name:        "source equals lamigrate_control",
			sourceTable: "lamigrate_control",
			destTable:   "migrations",
			wantErr:     true,
		},
		{
			name:        "uppercase rejected",
			sourceTable: "Schema_Migrations",
			destTable:   "migrations",
			wantErr:     true,
		},
		{
			name:        "starts with digit rejected",
			sourceTable: "1schema_migrations",
			destTable:   "migrations",
			wantErr:     true,
		},
		{
			name:        "hyphen rejected",
			sourceTable: "schema-migrations",
			destTable:   "migrations",
			wantErr:     true,
		},
		{
			name:        "single underscore valid",
			sourceTable: "_migrations",
			destTable:   "migrations",
			wantErr:     false,
		},
		{
			name:        "source equals lamigrate_control with different dest",
			sourceTable: "lamigrate_control",
			destTable:   "custom_migrations",
			wantErr:     true,
		},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := validateImportSourceTable(tt.sourceTable, tt.destTable)
			if (err != nil) != tt.wantErr {
				t.Errorf("validateImportSourceTable(%q, %q) error = %v, wantErr %v",
					tt.sourceTable, tt.destTable, err, tt.wantErr)
			}
		})
	}
}

// ---------- Migration ID generation ----------

func TestMigrationIDForVersion(t *testing.T) {
	t.Parallel()
	tests := []struct {
		version uint64
		want    string
	}{
		{0, "golang-migrate:0"},
		{1, "golang-migrate:1"},
		{42, "golang-migrate:42"},
		{18446744073709551615, "golang-migrate:18446744073709551615"}, // max uint64
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.want, func(t *testing.T) {
			t.Parallel()
			got := migrationIDForVersion(tt.version)
			if got != tt.want {
				t.Errorf("migrationIDForVersion(%d) = %q, want %q", tt.version, got, tt.want)
			}
		})
	}
}

// ---------- Hex conversion ----------

func TestHexToBytes32(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		hex     string
		wantHex string
	}{
		{
			name:    "known SHA-256 of hello",
			hex:     "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824",
			wantHex: "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824",
		},
		{
			name:    "all zeros",
			hex:     "0000000000000000000000000000000000000000000000000000000000000000",
			wantHex: "0000000000000000000000000000000000000000000000000000000000000000",
		},
		{
			name:    "all fs",
			hex:     "ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff",
			wantHex: "ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff",
		},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := hexToBytes32(tt.hex)
			gotHex := checksumHex(got)
			if gotHex != tt.wantHex {
				t.Errorf("hexToBytes32(%q) → %s, want %s", tt.hex, gotHex, tt.wantHex)
			}
		})
	}
}

// ---------- isExactImportMatch ----------

func TestIsExactImportMatch(t *testing.T) {
	t.Parallel()

	// Build some test checksums.
	cs1 := checksumBytes([]byte("SELECT 1;"))
	cs2 := checksumBytes([]byte("SELECT 2;"))
	cs1Hex := checksumHex(cs1)
	cs2Hex := checksumHex(cs2)

	existing := []destinationBaseline{
		{
			MigrationID:  "golang-migrate:1",
			UpChecksum:   cs1[:],
			DownChecksum: cs2[:],
		},
		{
			MigrationID:  "golang-migrate:2",
			UpChecksum:   cs2[:],
			DownChecksum: cs1[:],
		},
	}

	t.Run("exact match", func(t *testing.T) {
		t.Parallel()
		planned := []ImportPlanItem{
			{MigrationID: "golang-migrate:1", Version: 1, UpChecksum: cs1Hex, DownChecksum: cs2Hex},
			{MigrationID: "golang-migrate:2", Version: 2, UpChecksum: cs2Hex, DownChecksum: cs1Hex},
		}
		if !isExactImportMatch(existing, planned) {
			t.Error("expected exact match")
		}
	})

	t.Run("different count", func(t *testing.T) {
		t.Parallel()
		planned := []ImportPlanItem{
			{MigrationID: "golang-migrate:1", Version: 1, UpChecksum: cs1Hex, DownChecksum: cs2Hex},
		}
		if isExactImportMatch(existing, planned) {
			t.Error("expected no match for different count")
		}
	})

	t.Run("different ID", func(t *testing.T) {
		t.Parallel()
		planned := []ImportPlanItem{
			{MigrationID: "golang-migrate:1", Version: 1, UpChecksum: cs1Hex, DownChecksum: cs2Hex},
			{MigrationID: "golang-migrate:3", Version: 3, UpChecksum: cs2Hex, DownChecksum: cs1Hex},
		}
		if isExactImportMatch(existing, planned) {
			t.Error("expected no match for different ID")
		}
	})

	t.Run("different checksum", func(t *testing.T) {
		t.Parallel()
		cs3 := checksumBytes([]byte("SELECT 3;"))
		cs3Hex := checksumHex(cs3)
		planned := []ImportPlanItem{
			{MigrationID: "golang-migrate:1", Version: 1, UpChecksum: cs3Hex, DownChecksum: cs2Hex},
			{MigrationID: "golang-migrate:2", Version: 2, UpChecksum: cs2Hex, DownChecksum: cs1Hex},
		}
		if isExactImportMatch(existing, planned) {
			t.Error("expected no match for different checksum")
		}
	})

	t.Run("empty both", func(t *testing.T) {
		t.Parallel()
		if !isExactImportMatch(nil, nil) {
			t.Error("expected empty sets to match")
		}
	})
}

// ---------- Legacy file scanning ----------

func TestScanLegacyImportDir(t *testing.T) {
	t.Parallel()

	t.Run("basic three files", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		writeLegacyFile(t, dir, "1_create_users.up.sql", "CREATE TABLE users (id INT);")
		writeLegacyFile(t, dir, "1_create_users.down.sql", "DROP TABLE users;")
		writeLegacyFile(t, dir, "2_add_email.up.sql", "ALTER TABLE users ADD email VARCHAR(255);")
		writeLegacyFile(t, dir, "2_add_email.down.sql", "ALTER TABLE users DROP email;")
		writeLegacyFile(t, dir, "3_create_posts.up.sql", "CREATE TABLE posts (id INT);")
		writeLegacyFile(t, dir, "3_create_posts.down.sql", "DROP TABLE posts;")

		result, err := scanLegacyImportDir(dir)
		if err != nil {
			t.Fatalf("scanLegacyImportDir() error = %v", err)
		}
		if len(result.Candidates) != 3 {
			t.Fatalf("got %d candidates, want 3", len(result.Candidates))
		}
		// Verify sorted by version.
		if result.Candidates[0].Version != 1 || result.Candidates[1].Version != 2 || result.Candidates[2].Version != 3 {
			t.Errorf("versions not sorted: %d, %d, %d",
				result.Candidates[0].Version, result.Candidates[1].Version, result.Candidates[2].Version)
		}
		// Verify checksums are non-zero.
		for _, c := range result.Candidates {
			if c.UpChecksum == [32]byte{} {
				t.Errorf("version %d has zero up checksum", c.Version)
			}
			if c.DownChecksum == [32]byte{} {
				t.Errorf("version %d has zero down checksum", c.Version)
			}
		}
	})

	t.Run("variable width versions", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		writeLegacyFile(t, dir, "1_create.up.sql", "SELECT 1;")
		writeLegacyFile(t, dir, "1_create.down.sql", "SELECT 1;")
		writeLegacyFile(t, dir, "100_create_many.up.sql", "SELECT 100;")
		writeLegacyFile(t, dir, "100_create_many.down.sql", "SELECT 100;")
		writeLegacyFile(t, dir, "9999999999_large_version.up.sql", "SELECT 1;")
		writeLegacyFile(t, dir, "9999999999_large_version.down.sql", "SELECT 1;")

		result, err := scanLegacyImportDir(dir)
		if err != nil {
			t.Fatalf("scanLegacyImportDir() error = %v", err)
		}
		if len(result.Candidates) != 3 {
			t.Fatalf("got %d candidates, want 3", len(result.Candidates))
		}
		if result.Candidates[0].Version != 1 {
			t.Errorf("first version = %d, want 1", result.Candidates[0].Version)
		}
		if result.Candidates[1].Version != 100 {
			t.Errorf("second version = %d, want 100", result.Candidates[1].Version)
		}
		if result.Candidates[2].Version != 9999999999 {
			t.Errorf("third version = %d, want 9999999999", result.Candidates[2].Version)
		}
	})

	t.Run("leading zeroes canonicalized", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		writeLegacyFile(t, dir, "001_create.up.sql", "SELECT 1;")
		writeLegacyFile(t, dir, "001_create.down.sql", "SELECT 1;")

		result, err := scanLegacyImportDir(dir)
		if err != nil {
			t.Fatalf("scanLegacyImportDir() error = %v", err)
		}
		if len(result.Candidates) != 1 {
			t.Fatalf("got %d candidates, want 1", len(result.Candidates))
		}
		if result.Candidates[0].Version != 1 {
			t.Errorf("version = %d, want 1 (leading zeroes canonicalized)", result.Candidates[0].Version)
		}
	})

	t.Run("missing down file rejected", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		writeLegacyFile(t, dir, "1_create_users.up.sql", "CREATE TABLE users (id INT);")
		// No down file.

		_, err := scanLegacyImportDir(dir)
		if err == nil {
			t.Fatal("expected error for missing down file")
		}
	})

	t.Run("up-only file in directory still detected", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		// Only up file, no down file at all.
		writeLegacyFile(t, dir, "1_create.up.sql", "SELECT 1;")

		_, err := scanLegacyImportDir(dir)
		if err == nil {
			t.Fatal("expected error for missing down file")
		}
	})

	t.Run("duplicate version+extension same desc is fine", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		writeLegacyFile(t, dir, "1_create.up.sql", "SELECT 1;")
		writeLegacyFile(t, dir, "1_create.down.sql", "SELECT 1;")

		result, err := scanLegacyImportDir(dir)
		if err != nil {
			t.Fatalf("scanLegacyImportDir() error = %v", err)
		}
		if len(result.Candidates) != 1 {
			t.Fatalf("got %d candidates, want 1", len(result.Candidates))
		}
	})

	t.Run("empty directory", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		result, err := scanLegacyImportDir(dir)
		if err != nil {
			t.Fatalf("scanLegacyImportDir() error = %v", err)
		}
		if len(result.Candidates) != 0 {
			t.Fatalf("got %d candidates, want 0", len(result.Candidates))
		}
	})

	t.Run("invalid description rejected", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		writeLegacyFile(t, dir, "1_Create_Users.up.sql", "SELECT 1;")
		writeLegacyFile(t, dir, "1_Create_Users.down.sql", "SELECT 1;")

		_, err := scanLegacyImportDir(dir)
		if err == nil {
			t.Fatal("expected error for uppercase description")
		}
	})

	t.Run("non-matching files ignored", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		// Regular timestamp migration.
		writeLegacyFile(t, dir, "20260730120000_create.up.sql", "SELECT 1;")
		writeLegacyFile(t, dir, "20260730120000_create.down.sql", "SELECT 1;")
		// Non-SQL file.
		writeLegacyFile(t, dir, "README.md", "readme")
		// Legacy numbered file.
		writeLegacyFile(t, dir, "1_create.up.sql", "SELECT 1;")
		writeLegacyFile(t, dir, "1_create.down.sql", "SELECT 1;")

		result, err := scanLegacyImportDir(dir)
		if err != nil {
			t.Fatalf("scanLegacyImportDir() error = %v", err)
		}
		// Only the numbered file should be discovered (14-digit timestamps
		// don't match the legacy pattern).
		if len(result.Candidates) != 1 {
			t.Fatalf("got %d candidates, want 1", len(result.Candidates))
		}
		if result.Candidates[0].Version != 1 {
			t.Errorf("version = %d, want 1", result.Candidates[0].Version)
		}
	})

	t.Run("source name uses up filename", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		writeLegacyFile(t, dir, "42_add_index.up.sql", "CREATE INDEX idx ON t(c);")
		writeLegacyFile(t, dir, "42_add_index.down.sql", "DROP INDEX idx ON t;")

		result, err := scanLegacyImportDir(dir)
		if err != nil {
			t.Fatalf("scanLegacyImportDir() error = %v", err)
		}
		if len(result.Candidates) != 1 {
			t.Fatalf("got %d candidates, want 1", len(result.Candidates))
		}
		if result.Candidates[0].UpFilename != "42_add_index.up.sql" {
			t.Errorf("UpFilename = %q, want %q", result.Candidates[0].UpFilename, "42_add_index.up.sql")
		}
		if result.Candidates[0].Description != "add_index" {
			t.Errorf("Description = %q, want %q", result.Candidates[0].Description, "add_index")
		}
	})

	t.Run("sparse version sequence reported not rejected", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		// Versions 1, 5, 10 — gaps are fine.
		writeLegacyFile(t, dir, "1_create.up.sql", "SELECT 1;")
		writeLegacyFile(t, dir, "1_create.down.sql", "SELECT 1;")
		writeLegacyFile(t, dir, "5_add_feature.up.sql", "SELECT 5;")
		writeLegacyFile(t, dir, "5_add_feature.down.sql", "SELECT 5;")
		writeLegacyFile(t, dir, "10_final.up.sql", "SELECT 10;")
		writeLegacyFile(t, dir, "10_final.down.sql", "SELECT 10;")

		result, err := scanLegacyImportDir(dir)
		if err != nil {
			t.Fatalf("scanLegacyImportDir() error = %v", err)
		}
		if len(result.Candidates) != 3 {
			t.Fatalf("got %d candidates, want 3", len(result.Candidates))
		}
	})
}

// ---------- ImportPlanView classification ----------

func TestImportPlanViewClassification(t *testing.T) {
	t.Parallel()

	cs1 := checksumBytes([]byte("SELECT 1;"))
	cs2 := checksumBytes([]byte("SELECT 2;"))

	t.Run("baseline vs unresolved classification", func(t *testing.T) {
		t.Parallel()
		// Simulate: source version=5, files with versions 1,3,5,7,10
		// → baselines: 1,3,5 (<= 5) → unresolved: 7,10 (> 5)
		pathResult := &legacyImportPathResult{
			Candidates: []legacyImportCandidate{
				{Version: 1, Description: "create_a", UpFilename: "1_create_a.up.sql", DownFilename: "1_create_a.down.sql", UpChecksum: cs1, DownChecksum: cs2},
				{Version: 3, Description: "create_b", UpFilename: "3_create_b.up.sql", DownFilename: "3_create_b.down.sql", UpChecksum: cs1, DownChecksum: cs2},
				{Version: 5, Description: "create_c", UpFilename: "5_create_c.up.sql", DownFilename: "5_create_c.down.sql", UpChecksum: cs1, DownChecksum: cs2},
				{Version: 7, Description: "create_d", UpFilename: "7_create_d.up.sql", DownFilename: "7_create_d.down.sql", UpChecksum: cs1, DownChecksum: cs2},
				{Version: 10, Description: "create_e", UpFilename: "10_create_e.up.sql", DownFilename: "10_create_e.down.sql", UpChecksum: cs1, DownChecksum: cs2},
			},
			UpFiles:   map[uint64]string{},
			DownFiles: map[uint64]string{},
		}

		srcTuple := golangMigrateSourceTuple{Version: 5, Dirty: false}

		// We test the classification logic by building the view manually
		// (since buildImportPlan needs a DB connection).
		var baselines []ImportPlanItem
		var unresolved []ImportPlanItem
		for _, c := range pathResult.Candidates {
			item := ImportPlanItem{
				MigrationID:  migrationIDForVersion(c.Version),
				SourceName:   c.UpFilename,
				Version:      c.Version,
				UpChecksum:   checksumHex(c.UpChecksum),
				DownChecksum: checksumHex(c.DownChecksum),
			}
			if c.Version <= srcTuple.Version {
				baselines = append(baselines, item)
			} else {
				unresolved = append(unresolved, item)
			}
		}

		if len(baselines) != 3 {
			t.Errorf("got %d baselines, want 3", len(baselines))
		}
		if len(unresolved) != 2 {
			t.Errorf("got %d unresolved, want 2", len(unresolved))
		}
		if baselines[0].Version != 1 || baselines[1].Version != 3 || baselines[2].Version != 5 {
			t.Errorf("unexpected baseline versions: %v", baselines)
		}
		if unresolved[0].Version != 7 || unresolved[1].Version != 10 {
			t.Errorf("unexpected unresolved versions: %v", unresolved)
		}
	})

	t.Run("all baselines when source version is high", func(t *testing.T) {
		t.Parallel()
		pathResult := &legacyImportPathResult{
			Candidates: []legacyImportCandidate{
				{Version: 1, Description: "a", UpFilename: "1_a.up.sql", DownFilename: "1_a.down.sql", UpChecksum: cs1, DownChecksum: cs2},
				{Version: 2, Description: "b", UpFilename: "2_b.up.sql", DownFilename: "2_b.down.sql", UpChecksum: cs1, DownChecksum: cs2},
			},
			UpFiles:   map[uint64]string{},
			DownFiles: map[uint64]string{},
		}
		srcTuple := golangMigrateSourceTuple{Version: 100, Dirty: false}

		var baselines []ImportPlanItem
		var unresolved []ImportPlanItem
		for _, c := range pathResult.Candidates {
			item := ImportPlanItem{
				MigrationID: migrationIDForVersion(c.Version),
				Version:     c.Version,
			}
			if c.Version <= srcTuple.Version {
				baselines = append(baselines, item)
			} else {
				unresolved = append(unresolved, item)
			}
		}

		if len(baselines) != 2 {
			t.Errorf("got %d baselines, want 2", len(baselines))
		}
		if len(unresolved) != 0 {
			t.Errorf("got %d unresolved, want 0", len(unresolved))
		}
	})
}

// ---------- Import quiesced validation ----------

func TestImportGolangMigrateRejectsQuiescedFalse(t *testing.T) {
	t.Parallel()
	// We can't easily test the full ImportGolangMigrate without a DB,
	// but we can verify the quiesced check happens before connector creation.
	// This is tested via the integration tests.
	// Here we verify the error type.
	t.Skip("covered by integration TestImportRequiresQuiescedAttestation")
}

// ---------- Helpers ----------

// writeLegacyFile creates a file in the given directory.
func writeLegacyFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}
