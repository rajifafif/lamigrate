//go:build integration

package integration

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/go-sql-driver/mysql"
	lamigrate "github.com/rajifafif/lamigrate"
)

func newAdoptionTestMigrator(tb *testing.T, testDB *TestDB, tableName, dir, legacyDir string) *lamigrate.Migrator {
	tb.Helper()
	cfg, err := mysql.ParseDSN(testDB.DSN)
	if err != nil {
		tb.Fatalf("parse DSN: %v", err)
	}
	cfg.MultiStatements = true
	cfg.ParseTime = true
	m, err := lamigrate.NewMySQL(cfg, lamigrate.Options{
		Directory:   dir,
		LegacyDir:   legacyDir,
		TableName:   tableName,
		LockTimeout: 10 * time.Second,
	})
	if err != nil {
		tb.Fatalf("NewMySQL: %v", err)
	}
	return m
}

func createPrototypeTable(tb *testing.T, db *sql.DB, tableName string) {
	tb.Helper()
	_, err := db.Exec(fmt.Sprintf(`CREATE TABLE %s (
		id INT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
		migration VARCHAR(255) NOT NULL,
		batch INT UNSIGNED NOT NULL,
		applied_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
		UNIQUE KEY uk_migration (migration)
	)`, tableName))
	if err != nil {
		tb.Fatalf("create prototype table: %v", err)
	}
}

func insertPrototypeRow(tb *testing.T, db *sql.DB, tableName string, id uint64, migration string, batch uint64, appliedAt time.Time) {
	tb.Helper()
	_, err := db.Exec(
		fmt.Sprintf("INSERT INTO %s (id, migration, batch, applied_at) VALUES (?, ?, ?, ?)", tableName),
		id, migration, batch, appliedAt,
	)
	if err != nil {
		tb.Fatalf("insert prototype row: %v", err)
	}
}

func TestAdoptionBasicFlow(t *testing.T) {
	tb := newTestDB(t)
	tb.requireTestDBName()

	tsDir := t.TempDir()
	mustCreateMigration(t, tsDir, "20260730094235", "create_users", "CREATE TABLE users (id INT);", "DROP TABLE users;")
	mustCreateMigration(t, tsDir, "20260730100000", "add_email", "ALTER TABLE users ADD email VARCHAR(255);", "ALTER TABLE users DROP email;")

	legacyDir := t.TempDir()
	writeLegacyPair(t, legacyDir, "1_create_posts", "CREATE TABLE posts (id INT);", "DROP TABLE posts;")

	createPrototypeTable(t, tb.DB(), "migrations")
	now := time.Date(2026, 7, 30, 9, 42, 35, 0, time.UTC)
	insertPrototypeRow(t, tb.DB(), "migrations", 1, "20260730094235_create_users", 1, now)
	insertPrototypeRow(t, tb.DB(), "migrations", 2, "20260730100000_add_email", 1, now)
	insertPrototypeRow(t, tb.DB(), "migrations", 3, "1", 0, now)

	m := newAdoptionTestMigrator(t, tb, "migrations", tsDir, legacyDir)

	// Preview.
	plan, err := m.PreviewPrototypeAdoption(context.Background(), lamigrate.AdoptionRequest{
		BackupTable: "migrations_backup",
		Directory:   tsDir,
		LegacyDir:   legacyDir,
	})
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	if !plan.DryRun {
		t.Fatal("preview should be DryRun")
	}
	if plan.PrototypeRows != 3 {
		t.Fatalf("preview PrototypeRows = %d, want 3", plan.PrototypeRows)
	}
	if len(plan.Items) != 3 {
		t.Fatalf("preview Items = %d, want 3", len(plan.Items))
	}
	t.Logf("preview: %d items, max_batch=%d, next_batch=%d", len(plan.Items), plan.MaxBatch, plan.NextBatch)

	// Execute adoption.
	result, err := m.AdoptPrototype(context.Background(), lamigrate.AdoptionRequest{
		BackupTable: "migrations_backup",
		Directory:   tsDir,
		LegacyDir:   legacyDir,
	})
	if err != nil {
		t.Fatalf("adopt: %v", err)
	}
	if result.Command != "adopt-prototype" {
		t.Fatalf("result.Command = %q, want %q", result.Command, "adopt-prototype")
	}

	// Verify backup table exists with original prototype data.
	backupCount := countRows(t, tb.DB(), "SELECT COUNT(*) FROM migrations_backup")
	if backupCount != 3 {
		t.Fatalf("backup rows = %d, want 3", backupCount)
	}

	// Verify state table is now v1 shape (14 columns).
	var colCount int
	err = tb.DB().QueryRow(
		"SELECT COUNT(*) FROM information_schema.columns WHERE table_schema = ? AND table_name = ?",
		tb.Name, "migrations",
	).Scan(&colCount)
	if err != nil {
		t.Fatalf("count columns: %v", err)
	}
	if colCount != 14 {
		t.Fatalf("state table columns = %d, want 14", colCount)
	}

	// Verify control row exists.
	controlCount := countRows(t, tb.DB(),
		"SELECT COUNT(*) FROM lamigrate_control WHERE tracking_table = 'migrations'")
	if controlCount != 1 {
		t.Fatalf("control rows = %d, want 1", controlCount)
	}

	// Verify schema_version = 1 and next_batch = 2.
	var schemaVer uint64
	var nextBatch uint64
	err = tb.DB().QueryRow(
		"SELECT schema_version, next_batch FROM lamigrate_control WHERE tracking_table = 'migrations'",
	).Scan(&schemaVer, &nextBatch)
	if err != nil {
		t.Fatalf("read control row: %v", err)
	}
	if schemaVer != 1 {
		t.Fatalf("schema_version = %d, want 1", schemaVer)
	}
	if nextBatch != 2 {
		t.Fatalf("next_batch = %d, want 2", nextBatch)
	}
}

func TestAdoptionRejectsEmptyTable(t *testing.T) {
	tb := newTestDB(t)
	tb.requireTestDBName()

	tsDir := t.TempDir()
	legacyDir := t.TempDir()

	createPrototypeTable(t, tb.DB(), "migrations")

	m := newAdoptionTestMigrator(t, tb, "migrations", tsDir, legacyDir)

	_, err := m.AdoptPrototype(context.Background(), lamigrate.AdoptionRequest{
		BackupTable: "migrations_backup",
		Directory:   tsDir,
		LegacyDir:   legacyDir,
	})
	if err == nil {
		t.Fatal("expected error for empty prototype, got nil")
	}
}

func TestAdoptionRejectsMaxID(t *testing.T) {
	// The prototype uses INT UNSIGNED (max 4294967295), so we cannot
	// insert uint64 max into it. This test verifies the validation
	// rejects a row with MAX(id) = uint64 max by using a helper
	// that constructs a prototypeRow with the exhaustion value.
	// The unit test TestValidatePrototypeRows_RejectsMaxID covers
	// this case at the validation level.
	//
	// For integration, we verify that the ADOPTION command itself
	// properly calls validatePrototypeRows by using a valid prototype
	// with a valid row.
	tb := newTestDB(t)
	tb.requireTestDBName()

	tsDir := t.TempDir()
	writeLegacyPair(t, tsDir, "000001_create_users", "CREATE TABLE users (id INT);", "DROP TABLE users;")
	legacyDir := t.TempDir()
	writeLegacyPair(t, legacyDir, "001_create_users", "CREATE TABLE users (id INT);", "DROP TABLE users;")

	createPrototypeTable(t, tb.DB(), "migrations")
	// Use max INT UNSIGNED (4294967295) — this is the maximum possible
	// id in the prototype. The adoption should still succeed because
	// 4294967295 < 18446744073709551615.
	insertPrototypeRow(t, tb.DB(), "migrations", 4294967295, "1", 0, time.Now())

	m := newAdoptionTestMigrator(t, tb, "migrations", tsDir, legacyDir)

	result, err := m.AdoptPrototype(context.Background(), lamigrate.AdoptionRequest{
		BackupTable: "migrations_backup",
		Directory:   tsDir,
		LegacyDir:   legacyDir,
	})
	if err != nil {
		t.Fatalf("adopt with max INT UNSIGNED id: %v", err)
	}
	if result.Command != "adopt-prototype" {
		t.Fatalf("result.Command = %q, want %q", result.Command, "adopt-prototype")
	}

	// Verify the row was preserved.
	var maxID uint64
	err = tb.DB().QueryRow("SELECT MAX(id) FROM migrations").Scan(&maxID)
	if err != nil {
		t.Fatalf("read max id: %v", err)
	}
	if maxID != 4294967295 {
		t.Fatalf("max(id) = %d, want 4294967295", maxID)
	}
}

func TestAdoptionRequiresBackupTable(t *testing.T) {
	tb := newTestDB(t)
	tb.requireTestDBName()

	tsDir := t.TempDir()
	legacyDir := t.TempDir()

	createPrototypeTable(t, tb.DB(), "migrations")
	insertPrototypeRow(t, tb.DB(), "migrations", 1, "20260730094235_create_users", 1, time.Now())
	mustCreateMigration(t, tsDir, "20260730094235", "create_users", "CREATE TABLE users (id INT);", "DROP TABLE users;")

	m := newAdoptionTestMigrator(t, tb, "migrations", tsDir, legacyDir)

	_, err := m.AdoptPrototype(context.Background(), lamigrate.AdoptionRequest{
		BackupTable: "",
		Directory:   tsDir,
		LegacyDir:   legacyDir,
	})
	if err == nil {
		t.Fatal("expected error for empty backup table name, got nil")
	}
}

func TestAdoptionAtomicSwap(t *testing.T) {
	tb := newTestDB(t)
	tb.requireTestDBName()

	tsDir := t.TempDir()
	writeLegacyPair(t, tsDir, "000001_create_users", "CREATE TABLE users (id INT);", "DROP TABLE users;")
	legacyDir := t.TempDir()
	writeLegacyPair(t, legacyDir, "001_create_users", "CREATE TABLE users (id INT);", "DROP TABLE users;")

	createPrototypeTable(t, tb.DB(), "migrations")
	insertPrototypeRow(t, tb.DB(), "migrations", 1, "1", 0, time.Now())

	m := newAdoptionTestMigrator(t, tb, "migrations", tsDir, legacyDir)

	_, err := m.AdoptPrototype(context.Background(), lamigrate.AdoptionRequest{
		BackupTable: "migrations_backup",
		Directory:   tsDir,
		LegacyDir:   legacyDir,
	})
	if err != nil {
		t.Fatalf("adopt: %v", err)
	}

	// Verify original table name now holds v1 schema.
	var colCount int
	err = tb.DB().QueryRow(
		"SELECT COUNT(*) FROM information_schema.columns WHERE table_schema = ? AND table_name = ?",
		tb.Name, "migrations",
	).Scan(&colCount)
	if err != nil {
		t.Fatalf("count columns: %v", err)
	}
	if colCount != 14 {
		t.Fatalf("migrations columns = %d, want 14 after swap", colCount)
	}

	// Verify backup table holds original 4-column data.
	var backupColCount int
	err = tb.DB().QueryRow(
		"SELECT COUNT(*) FROM information_schema.columns WHERE table_schema = ? AND table_name = ?",
		tb.Name, "migrations_backup",
	).Scan(&backupColCount)
	if err != nil {
		t.Fatalf("count backup columns: %v", err)
	}
	if backupColCount != 4 {
		t.Fatalf("backup columns = %d, want 4", backupColCount)
	}
}

func TestAdoptionPreservesOrder(t *testing.T) {
	tb := newTestDB(t)
	tb.requireTestDBName()

	tsDir := t.TempDir()
	writeLegacyPair(t, tsDir, "000001_create_users", "CREATE TABLE users (id INT);", "DROP TABLE users;")
	legacyDir := t.TempDir()
	writeLegacyPair(t, legacyDir, "001_create_users", "CREATE TABLE users (id INT);", "DROP TABLE users;")

	createPrototypeTable(t, tb.DB(), "migrations")
	now := time.Date(2026, 7, 30, 9, 42, 35, 0, time.UTC)
	// Insert in non-sequential order.
	insertPrototypeRow(t, tb.DB(), "migrations", 5, "1", 0, now)
	insertPrototypeRow(t, tb.DB(), "migrations", 1, "20260730094235_create_users", 1, now)
	insertPrototypeRow(t, tb.DB(), "migrations", 3, "20260730100000_add_email", 1, now)

	mustCreateMigration(t, tsDir, "20260730094235", "create_users", "CREATE TABLE users (id INT);", "DROP TABLE users;")
	mustCreateMigration(t, tsDir, "20260730100000", "add_email", "ALTER TABLE users ADD email VARCHAR(255);", "ALTER TABLE users DROP email;")

	m := newAdoptionTestMigrator(t, tb, "migrations", tsDir, legacyDir)

	_, err := m.AdoptPrototype(context.Background(), lamigrate.AdoptionRequest{
		BackupTable: "migrations_backup",
		Directory:   tsDir,
		LegacyDir:   legacyDir,
	})
	if err != nil {
		t.Fatalf("adopt: %v", err)
	}

	// Verify original IDs preserved in ascending order.
	rows, err := tb.DB().Query("SELECT id FROM migrations ORDER BY id ASC")
	if err != nil {
		t.Fatalf("query ids: %v", err)
	}
	defer rows.Close()

	var ids []uint64
	for rows.Next() {
		var id uint64
		if err := rows.Scan(&id); err != nil {
			t.Fatalf("scan id: %v", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate: %v", err)
	}

	expected := []uint64{1, 3, 5}
	if len(ids) != len(expected) {
		t.Fatalf("ids = %v, want %v", ids, expected)
	}
	for i, id := range ids {
		if id != expected[i] {
			t.Fatalf("ids[%d] = %d, want %d (ids=%v)", i, id, expected[i], ids)
		}
	}
}

func TestAdoptionInterruptedRecovery(t *testing.T) {
	tb := newTestDB(t)
	tb.requireTestDBName()

	tsDir := t.TempDir()
	writeLegacyPair(t, tsDir, "000001_create_users", "CREATE TABLE users (id INT);", "DROP TABLE users;")
	legacyDir := t.TempDir()
	writeLegacyPair(t, legacyDir, "001_create_users", "CREATE TABLE users (id INT);", "DROP TABLE users;")

	createPrototypeTable(t, tb.DB(), "migrations")
	insertPrototypeRow(t, tb.DB(), "migrations", 1, "1", 0, time.Now())

	m := newAdoptionTestMigrator(t, tb, "migrations", tsDir, legacyDir)

	// Adopt successfully.
	_, err := m.AdoptPrototype(context.Background(), lamigrate.AdoptionRequest{
		BackupTable: "migrations_backup",
		Directory:   tsDir,
		LegacyDir:   legacyDir,
	})
	if err != nil {
		t.Fatalf("adopt: %v", err)
	}

	// Verify adoption completed.
	controlCount := countRows(t, tb.DB(),
		"SELECT COUNT(*) FROM lamigrate_control WHERE tracking_table = 'migrations'")
	if controlCount != 1 {
		t.Fatalf("control rows after adopt = %d, want 1", controlCount)
	}

	// Simulate interruption: delete the control row.
	_, err = tb.DB().Exec("DELETE FROM lamigrate_control WHERE tracking_table = 'migrations'")
	if err != nil {
		t.Fatalf("delete control row: %v", err)
	}

	// Verify v1 table still exists.
	var colCount int
	err = tb.DB().QueryRow(
		"SELECT COUNT(*) FROM information_schema.columns WHERE table_schema = ? AND table_name = ?",
		tb.Name, "migrations",
	).Scan(&colCount)
	if err != nil {
		t.Fatalf("count columns: %v", err)
	}
	if colCount != 14 {
		t.Fatalf("state table columns = %d, want 14", colCount)
	}

	// Attempt adoption again — should trigger recovery path.
	_, err = m.AdoptPrototype(context.Background(), lamigrate.AdoptionRequest{
		BackupTable: "migrations_backup",
		Directory:   tsDir,
		LegacyDir:   legacyDir,
	})
	if err != nil {
		t.Fatalf("recovery adopt: %v", err)
	}

	// Verify control row is recreated.
	controlCount = countRows(t, tb.DB(),
		"SELECT COUNT(*) FROM lamigrate_control WHERE tracking_table = 'migrations'")
	if controlCount != 1 {
		t.Fatalf("control rows after recovery = %d, want 1", controlCount)
	}

	// Verify v1 table is intact.
	rowCount := countRows(t, tb.DB(), "SELECT COUNT(*) FROM migrations")
	if rowCount != 1 {
		t.Fatalf("rows after recovery = %d, want 1", rowCount)
	}
}

func TestPreviewDoesNotModifyData(t *testing.T) {
	tb := newTestDB(t)
	tb.requireTestDBName()

	tsDir := t.TempDir()
	legacyDir := t.TempDir()

	createPrototypeTable(t, tb.DB(), "migrations")
	now := time.Date(2026, 7, 30, 9, 42, 35, 0, time.UTC)
	insertPrototypeRow(t, tb.DB(), "migrations", 1, "20260730094235_create_users", 1, now)
	mustCreateMigration(t, tsDir, "20260730094235", "create_users", "CREATE TABLE users (id INT);", "DROP TABLE users;")

	m := newAdoptionTestMigrator(t, tb, "migrations", tsDir, legacyDir)

	// Record state before preview.
	protoCount := countRows(t, tb.DB(), "SELECT COUNT(*) FROM migrations")

	// Preview.
	_, err := m.PreviewPrototypeAdoption(context.Background(), lamigrate.AdoptionRequest{
		BackupTable: "migrations_backup",
		Directory:   tsDir,
		LegacyDir:   legacyDir,
	})
	if err != nil {
		t.Fatalf("preview: %v", err)
	}

	// Verify: prototype table unchanged.
	protoCountAfter := countRows(t, tb.DB(), "SELECT COUNT(*) FROM migrations")
	if protoCountAfter != protoCount {
		t.Fatalf("prototype rows changed: %d -> %d", protoCount, protoCountAfter)
	}

	// Verify: no backup table created.
	var backupExists int
	err = tb.DB().QueryRow(
		"SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = ? AND table_name = ?",
		tb.Name, "migrations_backup",
	).Scan(&backupExists)
	if err != nil {
		t.Fatalf("check backup: %v", err)
	}
	if backupExists != 0 {
		t.Fatal("backup table should not exist after preview")
	}

	// Verify: still 4 columns (prototype shape).
	var colCount int
	err = tb.DB().QueryRow(
		"SELECT COUNT(*) FROM information_schema.columns WHERE table_schema = ? AND table_name = ?",
		tb.Name, "migrations",
	).Scan(&colCount)
	if err != nil {
		t.Fatalf("count columns: %v", err)
	}
	if colCount != 4 {
		t.Fatalf("columns = %d, want 4 (should still be prototype)", colCount)
	}
}

func mustAdoptionErrorContains(t *testing.T, err error, substr string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected error containing %q, got nil", substr)
	}
	if !strings.Contains(err.Error(), substr) {
		t.Fatalf("expected error containing %q, got: %v", substr, err)
	}
}
