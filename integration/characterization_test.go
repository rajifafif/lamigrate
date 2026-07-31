//go:build integration

package integration

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"

	lamigrate "github.com/rajifafif/lamigrate"
)

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// mustCreateMigration writes a timestamped 14-digit migration pair into dir.
func mustCreateMigration(t *testing.T, dir, timestamp, name, upSQL, downSQL string) {
	t.Helper()
	base := timestamp + "_" + name
	for _, p := range [2][2]string{
		{".up.sql", upSQL},
		{".down.sql", downSQL},
	} {
		if err := os.WriteFile(filepath.Join(dir, base+p[0]), []byte(p[1]), 0o644); err != nil {
			t.Fatalf("write migration file: %v", err)
		}
	}
}

// mustCreateLegacyMigration writes a 6-digit numbered legacy migration pair into dir.
func mustCreateLegacyMigration(t *testing.T, dir, number, name, upSQL, downSQL string) {
	t.Helper()
	base := number + "_" + name
	for _, p := range [2][2]string{
		{".up.sql", upSQL},
		{".down.sql", downSQL},
	} {
		if err := os.WriteFile(filepath.Join(dir, base+p[0]), []byte(p[1]), 0o644); err != nil {
			t.Fatalf("write legacy migration file: %v", err)
		}
	}
}

// ---------------------------------------------------------------------------
// LM-005 Database Characterization Tests
//
// These tests document the ACTUAL behavior of the prototype database layer.
// Unsafe results are asserted as regression evidence, never as desired behavior.
// Each test notes which architecture §4 gap it demonstrates.
// ---------------------------------------------------------------------------

// TestCharacterizeConstructorSideEffects proves that New() connects to the
// database and creates the migrations table immediately — a side-effect-full
// construction pattern.
//
// GAP: §4/§6.2 "No side-effect-free construction" — New() must perform local
// validation only; connecting and creating tables belongs in an explicit
// Start/Open lifecycle method.
func TestCharacterizeConstructorSideEffects(t *testing.T) {
	tb := newTestDB(t)
	tb.requireTestDBName()

	dir := t.TempDir()
	m, err := lamigrate.New(dir, tb.DSN)
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	defer m.Close()

	// After New() returns, the migrations table must already exist.
	var count int
	if err := tb.QueryRow(
		"SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = ? AND table_name = ?",
		tb.Name, "migrations",
	).Scan(&count); err != nil {
		t.Fatalf("information_schema query: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected migrations table to exist immediately after New(), got count=%d", count)
	}
	t.Log("CONFIRMED: New() creates the migrations table as a side effect of construction")
	t.Log("GAP: §4 — construction must not open DB connections or create tables")
}

// TestCharacterizeTrackingTableSchema verifies the current 4-column schema
// of the migrations tracking table (id, migration, batch, applied_at).
//
// GAP: §4/§9 "No metadata state machine" — target schema requires ~12+
// columns including state, source tracking, checksums, runner_id, and
// CHECK constraints.
func TestCharacterizeTrackingTableSchema(t *testing.T) {
	tb := newTestDB(t)
	tb.requireTestDBName()

	dir := t.TempDir()
	m, err := lamigrate.New(dir, tb.DSN)
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	defer m.Close()

	type colInfo struct {
		Name     string
		Type     string
		Nullable string
	}
	rows, err := tb.DB().Query(
		"SELECT column_name, data_type, is_nullable FROM information_schema.columns WHERE table_schema = ? AND table_name = ? ORDER BY ordinal_position",
		tb.Name, "migrations",
	)
	if err != nil {
		t.Fatalf("columns query: %v", err)
	}
	defer rows.Close()

	var cols []colInfo
	for rows.Next() {
		var c colInfo
		if err := rows.Scan(&c.Name, &c.Type, &c.Nullable); err != nil {
			t.Fatal(err)
		}
		cols = append(cols, c)
	}

	want := []string{"id", "migration", "batch", "applied_at"}
	if len(cols) != len(want) {
		t.Fatalf("expected %d columns, got %d", len(want), len(cols))
	}
	for i, name := range want {
		if cols[i].Name != name {
			t.Errorf("column %d: want %q, got %q", i, name, cols[i].Name)
		}
	}

	// Verify unique key exists
	var indexCount int
	tb.QueryRow(
		"SELECT COUNT(*) FROM information_schema.statistics WHERE table_schema = ? AND table_name = ? AND non_unique = 0",
		tb.Name, "migrations",
	).Scan(&indexCount)
	if indexCount < 1 {
		t.Error("expected at least one unique key on migrations table")
	}

	for _, c := range cols {
		t.Logf("  column: %-15s type=%-20s nullable=%s", c.Name, c.Type, c.Nullable)
	}
	t.Logf("SCHEMA GAP: current has %d columns; target (§9) requires state, source_kind, source_version, up_checksum, down_checksum, runner_id, started_at, updated_at, is_baseline + CHECK constraints",
		len(cols))
}

// TestCharacterizeTableMutator proves that Table() mutates the tracking table
// name post-construction, leaving the default "migrations" table orphaned.
// Subsequent operations target the new (non-existent) table and fail.
//
// GAP: §4 "Custom tracking table selected after default is created" — Table()
// mutates state after New() has already created the default "migrations" table.
func TestCharacterizeTableMutator(t *testing.T) {
	tb := newTestDB(t)
	tb.requireTestDBName()

	dir := t.TempDir()
	m, err := lamigrate.New(dir, tb.DSN)
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	defer m.Close()

	// Default "migrations" table exists after construction
	var count int
	if err := tb.QueryRow(
		"SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = ? AND table_name = ?",
		tb.Name, "migrations",
	).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatal("default migrations table should exist after New()")
	}

	// Mutate the tracking table name
	m.Table("custom_migrations")

	// Subsequent Up() queries the non-existent custom_migrations table.
	// Even with no pending migrations, allAppliedSet() still issues a SELECT.
	ctx := context.Background()
	err = m.Up(ctx)
	if err == nil {
		t.Fatal("Up() after Table() should fail — custom_migrations does not exist")
	}
	t.Logf("CONFIRMED: Up() after Table(\"custom_migrations\") fails: %v", err)
	t.Log("GAP CONFIRMED: Table() mutates post-construction; default table created but unused")
}

// TestCharacterizeBatchAllocation proves batch numbers use MAX(batch)+1 and
// are monotonically increasing across Up() calls.
//
// GAP: §4/§9 — batch number is the sole ordering mechanism; no sequence
// counter, no atomic batch allocation, no dirty state tracking.
func TestCharacterizeBatchAllocation(t *testing.T) {
	tb := newTestDB(t)
	tb.requireTestDBName()

	dir := t.TempDir()
	mustCreateMigration(t, dir, "20260101000001", "create_alpha",
		"CREATE TABLE alpha (id INT PRIMARY KEY);",
		"DROP TABLE IF EXISTS alpha;")
	mustCreateMigration(t, dir, "20260102000001", "create_beta",
		"CREATE TABLE beta (id INT PRIMARY KEY);",
		"DROP TABLE IF EXISTS beta;")
	mustCreateMigration(t, dir, "20260103000001", "create_gamma",
		"CREATE TABLE gamma (id INT PRIMARY KEY);",
		"DROP TABLE IF EXISTS gamma;")

	m, err := lamigrate.New(dir, tb.DSN)
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	defer m.Close()

	ctx := context.Background()

	// First Up: all 3 should be batch 1
	if err := m.Up(ctx); err != nil {
		t.Fatalf("first Up() error: %v", err)
	}

	var batch1 int
	if err := tb.QueryRow("SELECT COUNT(*) FROM migrations WHERE batch = 1").Scan(&batch1); err != nil {
		t.Fatal(err)
	}
	if batch1 != 3 {
		t.Errorf("expected 3 migrations in batch 1, got %d", batch1)
	}

	// Add a fourth migration and run Up again
	mustCreateMigration(t, dir, "20260104000001", "create_delta",
		"CREATE TABLE delta (id INT PRIMARY KEY);",
		"DROP TABLE IF EXISTS delta;")

	if err := m.Up(ctx); err != nil {
		t.Fatalf("second Up() error: %v", err)
	}

	var batch2 int
	if err := tb.QueryRow("SELECT COUNT(*) FROM migrations WHERE batch = 2").Scan(&batch2); err != nil {
		t.Fatal(err)
	}
	if batch2 != 1 {
		t.Errorf("expected 1 migration in batch 2, got %d", batch2)
	}

	var maxBatch int
	if err := tb.QueryRow("SELECT MAX(batch) FROM migrations").Scan(&maxBatch); err != nil {
		t.Fatal(err)
	}
	if maxBatch != 2 {
		t.Errorf("expected MAX(batch)=2, got %d", maxBatch)
	}

	t.Logf("CONFIRMED: first batch=1 (%d migrations), second batch=2 (%d migration), MAX=%d",
		batch1, batch2, maxBatch)
	t.Log("GAP: batch allocation is MAX(batch)+1 with no atomicity or sequence counter")
}

// TestCharacterizeUpApplyAndRecord proves that Up() both executes migration
// SQL and records the migration in the tracking table — as separate operations.
//
// GAP: §4/§9 — SQL execution and metadata recording are not atomic; a crash
// between them creates a dirty state with no recovery mechanism.
func TestCharacterizeUpApplyAndRecord(t *testing.T) {
	tb := newTestDB(t)
	tb.requireTestDBName()

	dir := t.TempDir()
	mustCreateMigration(t, dir, "20260101000001", "create_items",
		"CREATE TABLE items (id BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY, name VARCHAR(100));",
		"DROP TABLE IF EXISTS items;")

	m, err := lamigrate.New(dir, tb.DSN)
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	defer m.Close()

	ctx := context.Background()
	if err := m.Up(ctx); err != nil {
		t.Fatalf("Up() error: %v", err)
	}

	// Verify the items table was created (SQL executed)
	var tableExists int
	tb.QueryRow(
		"SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = ? AND table_name = ?",
		tb.Name, "items",
	).Scan(&tableExists)
	if tableExists != 1 {
		t.Fatal("items table was not created by Up()")
	}

	// Verify the migration was recorded (metadata written)
	var migration string
	var batch int
	err = tb.QueryRow(
		"SELECT migration, batch FROM migrations WHERE migration = ?",
		"20260101000001_create_items",
	).Scan(&migration, &batch)
	if err != nil {
		t.Fatalf("migration record not found: %v", err)
	}
	if batch != 1 {
		t.Errorf("expected batch=1, got %d", batch)
	}

	t.Logf("CONFIRMED: Up() executes SQL (items table created) AND records metadata (batch=%d)", batch)
	t.Log("GAP: SQL execution and metadata recording are separate non-atomic operations")
}

// TestCharacterizeDownRemoveRecord proves that Down() executes rollback SQL
// and removes the migration record from the tracking table.
//
// GAP: §4/§9 — no dirty/rolling_back state; if rollback SQL fails midway,
// the metadata is left inconsistent with the actual database state.
func TestCharacterizeDownRemoveRecord(t *testing.T) {
	tb := newTestDB(t)
	tb.requireTestDBName()

	dir := t.TempDir()
	mustCreateMigration(t, dir, "20260101000001", "create_items",
		"CREATE TABLE items (id BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY, name VARCHAR(100));",
		"DROP TABLE IF EXISTS items;")

	m, err := lamigrate.New(dir, tb.DSN)
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	defer m.Close()

	ctx := context.Background()

	// Apply
	if err := m.Up(ctx); err != nil {
		t.Fatalf("Up() error: %v", err)
	}

	// Verify applied
	var count int
	tb.QueryRow("SELECT COUNT(*) FROM migrations").Scan(&count)
	if count != 1 {
		t.Fatalf("expected 1 record after Up, got %d", count)
	}

	// Rollback
	if err := m.Down(ctx); err != nil {
		t.Fatalf("Down() error: %v", err)
	}

	// Verify table dropped
	tb.QueryRow(
		"SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = ? AND table_name = ?",
		tb.Name, "items",
	).Scan(&count)
	if count != 0 {
		t.Error("items table should not exist after Down()")
	}

	// Verify record deleted
	tb.QueryRow("SELECT COUNT(*) FROM migrations").Scan(&count)
	if count != 0 {
		t.Errorf("expected 0 migration records after Down, got %d", count)
	}

	t.Log("CONFIRMED: Down() executes rollback SQL and deletes the migration record")
	t.Log("GAP: No rolling_back state tracked; partial rollback failure leaves inconsistent metadata")
}

// TestCharacterizeResetRemovesAll proves that Reset() rolls back all applied
// migrations and leaves the tracking table empty.
//
// GAP: §4/§9 — no atomic batch rollback; each migration is rolled back
// individually with no coordination or state tracking.
func TestCharacterizeResetRemovesAll(t *testing.T) {
	tb := newTestDB(t)
	tb.requireTestDBName()

	dir := t.TempDir()
	mustCreateMigration(t, dir, "20260101000001", "create_alpha",
		"CREATE TABLE alpha (id INT PRIMARY KEY);",
		"DROP TABLE IF EXISTS alpha;")
	mustCreateMigration(t, dir, "20260102000001", "create_beta",
		"CREATE TABLE beta (id INT PRIMARY KEY);",
		"DROP TABLE IF EXISTS beta;")
	mustCreateMigration(t, dir, "20260103000001", "create_gamma",
		"CREATE TABLE gamma (id INT PRIMARY KEY);",
		"DROP TABLE IF EXISTS gamma;")

	m, err := lamigrate.New(dir, tb.DSN)
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	defer m.Close()

	ctx := context.Background()
	if err := m.Up(ctx); err != nil {
		t.Fatalf("Up() error: %v", err)
	}

	// Verify all applied
	var count int
	tb.QueryRow("SELECT COUNT(*) FROM migrations").Scan(&count)
	if count != 3 {
		t.Fatalf("expected 3 records after Up, got %d", count)
	}

	// Reset
	if err := m.Reset(ctx); err != nil {
		t.Fatalf("Reset() error: %v", err)
	}

	// Verify all records gone
	tb.QueryRow("SELECT COUNT(*) FROM migrations").Scan(&count)
	if count != 0 {
		t.Errorf("expected 0 records after Reset, got %d", count)
	}

	// Verify all tables dropped
	for _, table := range []string{"alpha", "beta", "gamma"} {
		tb.QueryRow(
			"SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = ? AND table_name = ?",
			tb.Name, table,
		).Scan(&count)
		if count != 0 {
			t.Errorf("table %s should not exist after Reset()", table)
		}
	}

	t.Log("CONFIRMED: Reset() rolls back all migrations, deletes all records, drops all user tables")
	t.Log("GAP: No atomic batch rollback; each migration rolled back individually")
}

// TestCharacterizeLegacyImportAsBatchZero proves that ImportLegacy() marks
// legacy numbered files as applied in batch 0 without executing any SQL.
//
// GAP: §4/§13 — no source metadata table reconciliation, no version/dirty
// check, no atomic import transaction, no checksums.
func TestCharacterizeLegacyImportAsBatchZero(t *testing.T) {
	tb := newTestDB(t)
	tb.requireTestDBName()

	dir := t.TempDir()
	mustCreateLegacyMigration(t, dir, "000001", "legacy_create_users",
		"CREATE TABLE legacy_users (id INT PRIMARY KEY);",
		"DROP TABLE IF EXISTS legacy_users;")
	mustCreateLegacyMigration(t, dir, "000002", "legacy_create_orders",
		"CREATE TABLE legacy_orders (id INT PRIMARY KEY);",
		"DROP TABLE IF EXISTS legacy_orders;")

	m, err := lamigrate.New(dir, tb.DSN)
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	defer m.Close()

	ctx := context.Background()
	if err := m.ImportLegacy(ctx); err != nil {
		t.Fatalf("ImportLegacy() error: %v", err)
	}

	// Verify records exist with batch=0
	type record struct {
		Migration string
		Batch     int
	}
	var records []record
	rows, err := tb.DB().Query("SELECT migration, batch FROM migrations ORDER BY id ASC")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var r record
		if err := rows.Scan(&r.Migration, &r.Batch); err != nil {
			t.Fatal(err)
		}
		records = append(records, r)
	}

	if len(records) != 2 {
		t.Fatalf("expected 2 imported records, got %d", len(records))
	}
	for _, r := range records {
		if r.Batch != 0 {
			t.Errorf("migration %s: expected batch=0, got batch=%d", r.Migration, r.Batch)
		}
		t.Logf("  record: migration=%s batch=%d", r.Migration, r.Batch)
	}

	// Verify SQL was NOT executed — user tables should not exist
	var tableCount int
	tb.QueryRow(
		"SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = ? AND table_name IN ('legacy_users', 'legacy_orders')",
		tb.Name,
	).Scan(&tableCount)
	if tableCount != 0 {
		t.Errorf("ImportLegacy should not execute SQL; found %d user tables that should not exist", tableCount)
	}

	t.Log("CONFIRMED: ImportLegacy() records in batch=0 without executing SQL")
	t.Log("GAP: §13 — no source table reconciliation, no version/dirty check, no atomic transaction")
}

// TestCharacterizePretendDoesNotWrite proves that PretendUp() prints SQL
// without modifying the database in any way.
//
// NOTE: Current PretendUp re-reads files independently (no plan/execution
// parity — see §5.5 gap).
func TestCharacterizePretendDoesNotWrite(t *testing.T) {
	tb := newTestDB(t)
	tb.requireTestDBName()

	dir := t.TempDir()
	mustCreateMigration(t, dir, "20260101000001", "create_pretend_table",
		"CREATE TABLE pretend_table (id INT PRIMARY KEY);",
		"DROP TABLE IF EXISTS pretend_table;")

	m, err := lamigrate.New(dir, tb.DSN)
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	defer m.Close()

	ctx := context.Background()
	if err := m.PretendUp(ctx); err != nil {
		t.Fatalf("PretendUp() error: %v", err)
	}

	// Verify no migration recorded
	var count int
	tb.QueryRow("SELECT COUNT(*) FROM migrations").Scan(&count)
	if count != 0 {
		t.Errorf("PretendUp should not record migrations; got %d records", count)
	}

	// Verify table not created
	tb.QueryRow(
		"SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = ? AND table_name = ?",
		tb.Name, "pretend_table",
	).Scan(&count)
	if count != 0 {
		t.Error("PretendUp should not create tables")
	}

	t.Log("CONFIRMED: PretendUp() prints SQL without modifying the database")
	t.Log("GAP: §5.5 — PretendUp re-reads files independently; no plan/execution parity")
}

// TestCharacterizeMultiStatementMigration proves that multi-statement SQL in
// a single migration file is executed as a single multiStatement batch.
// Both CREATE TABLE statements succeed when multiStatements=true.
//
// NOTE: No per-statement atomicity — if statement N fails, statements 1..N-1
// are already committed. This is regression evidence, not desired behavior.
func TestCharacterizeMultiStatementMigration(t *testing.T) {
	tb := newTestDB(t)
	tb.requireTestDBName()

	dir := t.TempDir()
	mustCreateMigration(t, dir, "20260101000001", "create_multi",
		"CREATE TABLE multi_a (id INT PRIMARY KEY);\nCREATE TABLE multi_b (name VARCHAR(50) NOT NULL);",
		"DROP TABLE IF EXISTS multi_b;\nDROP TABLE IF EXISTS multi_a;")

	m, err := lamigrate.New(dir, tb.DSN)
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	defer m.Close()

	ctx := context.Background()
	if err := m.Up(ctx); err != nil {
		t.Fatalf("Up() error: %v", err)
	}

	// Both tables should exist
	for _, table := range []string{"multi_a", "multi_b"} {
		var count int
		tb.QueryRow(
			"SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = ? AND table_name = ?",
			tb.Name, table,
		).Scan(&count)
		if count != 1 {
			t.Errorf("table %s should exist after multi-statement Up()", table)
		}
	}

	// Verify the migration was recorded once
	var recordCount int
	tb.QueryRow("SELECT COUNT(*) FROM migrations WHERE migration = ?",
		"20260101000001_create_multi").Scan(&recordCount)
	if recordCount != 1 {
		t.Errorf("expected 1 migration record, got %d", recordCount)
	}

	t.Log("CONFIRMED: Multi-statement SQL executes all statements in a single batch")
	t.Log("NOTE: No per-statement atomicity — partial failures leave committed DDL behind")
}

// TestCharacterizeConcurrentUpRace proves that two goroutines calling Up()
// simultaneously race without any advisory lock. One succeeds while the
// other fails on DDL execution. The SQL is executed by both goroutines
// without coordination.
//
// GAP: §4/§10 "No advisory lock protocol" — concurrent processes can
// execute the same migration SQL simultaneously, causing DDL errors and
// inconsistent metadata state.
func TestCharacterizeConcurrentUpRace(t *testing.T) {
	tb := newTestDB(t)
	tb.requireTestDBName()

	dir := t.TempDir()
	mustCreateMigration(t, dir, "20260101000001", "create_race_table",
		"CREATE TABLE race_table (id INT PRIMARY KEY);",
		"DROP TABLE IF EXISTS race_table;")

	// Both goroutines share the same Migrate instance — same DB pool, same
	// tableName, same migration directory. The race is at the application
	// level: no advisory lock prevents concurrent execution.
	m, err := lamigrate.New(dir, tb.DSN)
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	defer m.Close()

	ctx := context.Background()
	var wg sync.WaitGroup
	errs := make([]error, 2)

	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			errs[idx] = m.Up(ctx)
		}(i)
	}
	wg.Wait()

	succeeded, failed := 0, 0
	for i, err := range errs {
		if err == nil {
			succeeded++
		} else {
			failed++
			t.Logf("goroutine %d error (expected race condition): %v", i, err)
		}
	}

	t.Logf("CONCURRENT RACE RESULT: %d succeeded, %d failed out of 2 goroutines", succeeded, failed)
	if succeeded == 0 {
		t.Fatal("expected at least one goroutine to succeed")
	}

	// Verify the table exists (at least one goroutine's SQL was applied)
	var tableExists int
	tb.QueryRow(
		"SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = ? AND table_name = ?",
		tb.Name, "race_table",
	).Scan(&tableExists)
	if tableExists != 1 {
		t.Errorf("race_table should exist, found %d", tableExists)
	}

	// Verify exactly one migration record (duplicate would be metadata corruption)
	var recordCount int
	tb.QueryRow(
		"SELECT COUNT(*) FROM migrations WHERE migration = ?",
		"20260101000001_create_race_table",
	).Scan(&recordCount)
	t.Logf("migration records for race_table: %d (should be 1; duplicates = metadata corruption)", recordCount)
	if recordCount != 1 {
		t.Errorf("expected exactly 1 migration record after concurrent Up, got %d", recordCount)
	}

	// Document that the failing goroutine's SQL may have partially executed
	// without being recorded (dirty state).
	if failed > 0 {
		t.Log("GAP DEMONSTRATED: No advisory lock prevents concurrent Up() execution")
		t.Log("GAP DEMONSTRATED: Failed goroutine's SQL may have partially executed without metadata recording")
		t.Log("GAP DEMONSTRATED: No dirty state or rollback mechanism exists for partial failures")
	}

	// Verify migration is not in status list as pending (it was applied)
	status, err := m.Status(ctx)
	if err != nil {
		t.Fatalf("Status() error: %v", err)
	}
	appliedCount := 0
	for _, s := range status {
		if s.Applied {
			appliedCount++
		}
	}
	if appliedCount != 1 {
		t.Errorf("expected Status to show 1 applied migration, got %d", appliedCount)
	}

	// Check that the no-more-pending path works
	err = m.Up(ctx)
	if err != nil {
		t.Errorf("second Up() after race should succeed (nothing pending), got: %v", err)
	}
}
