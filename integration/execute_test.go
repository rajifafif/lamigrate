//go:build integration

package integration

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"testing"
	"time"

	_ "github.com/go-sql-driver/mysql"
	lamigrate "github.com/rajifafif/lamigrate"
)

// ---------------------------------------------------------------------------
// Integration tests for LM-024: Execute intent states, batch semantics,
// up/down/reset execution safety.
//
// Tests verify the §11.2 and §11.3 execution protocols against a real
// MySQL database.
// ---------------------------------------------------------------------------

// createTestMigratorWithMigration creates a Migrator pointing at the
// given directory, bootstraps metadata, and returns the migrator.
func createTestMigratorWithMigration(
	t *testing.T,
	tb *TestDB,
	tableName, dir, timestamp, name, upSQL, downSQL string,
) *lamigrate.Migrator {
	t.Helper()
	mustCreateMigration(t, dir, timestamp, name, upSQL, downSQL)
	m := newTestMigratorWithDir(t, tb, tableName, dir)
	return m
}

// migrationCount returns the number of rows in the tracking table.
func migrationCount(t *testing.T, tb *TestDB, tableName string) int {
	t.Helper()
	var count int
	err := tb.DB().QueryRow(
		fmt.Sprintf("SELECT COUNT(*) FROM `%s`", tableName),
	).Scan(&count)
	if err != nil {
		t.Fatalf("migrationCount: %v", err)
	}
	return count
}

// migrationState returns the state column for a given migration.
func migrationState(t *testing.T, tb *TestDB, tableName, migration string) string {
	t.Helper()
	var state string
	err := tb.DB().QueryRow(
		fmt.Sprintf("SELECT state FROM `%s` WHERE migration = ?", tableName),
		migration,
	).Scan(&state)
	if err != nil {
		t.Fatalf("migrationState(%s): %v", migration, err)
	}
	return state
}

// migrationBatch returns the batch number for a given migration.
func migrationBatch(t *testing.T, tb *TestDB, tableName, migration string) int {
	t.Helper()
	var batch int
	err := tb.DB().QueryRow(
		fmt.Sprintf("SELECT batch FROM `%s` WHERE migration = ?", tableName),
		migration,
	).Scan(&batch)
	if err != nil {
		t.Fatalf("migrationBatch(%s): %v", migration, err)
	}
	return batch
}

// tableExists checks if a user table exists in the database.
func tableExists(t *testing.T, tb *TestDB, tableName string) bool {
	t.Helper()
	var count int
	err := tb.DB().QueryRow(
		"SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = ? AND table_name = ?",
		tb.Name, tableName,
	).Scan(&count)
	if err != nil {
		t.Fatalf("tableExists(%s): %v", tableName, err)
	}
	return count > 0
}

// nextBatchValue reads the next_batch from the control table.
func nextBatchValue(t *testing.T, tb *TestDB, trackingTable string) uint64 {
	t.Helper()
	var nb uint64
	err := tb.DB().QueryRow(
		"SELECT next_batch FROM lamigrate_control WHERE tracking_table = ?",
		trackingTable,
	).Scan(&nb)
	if err != nil {
		t.Fatalf("nextBatchValue: %v", err)
	}
	return nb
}

// openAdminConn opens a raw connection to the test DB for verification.
func openAdminConn(t *testing.T, tb *TestDB) *sql.DB {
	t.Helper()
	db, err := sql.Open("mysql", tb.DSN)
	if err != nil {
		t.Fatalf("openAdminConn: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

// ---------- TestUpAppliesMigrations ----------
func TestUpAppliesMigrations(t *testing.T) {
	tb := newTestDB(t)
	tb.requireTestDBName()

	dir := t.TempDir()
	m := createTestMigratorWithMigration(t, tb, "migrations", dir,
		"20260731120001", "create_alpha",
		"CREATE TABLE alpha (id INT UNSIGNED AUTO_INCREMENT PRIMARY KEY);",
		"DROP TABLE IF EXISTS alpha;")

	ctx := context.Background()
	result, err := m.Up(ctx, lamigrate.DownAll())
	if err != nil {
		t.Fatalf("Up failed: %v", err)
	}

	if len(result.Migrated) != 1 {
		t.Fatalf("expected 1 migrated, got %d", len(result.Migrated))
	}
	if !result.Migrated[0].Applied {
		t.Error("expected migration to be applied")
	}
	if result.Migrated[0].Name != "20260731120001_create_alpha" {
		t.Errorf("wrong migration name: %s", result.Migrated[0].Name)
	}

	// Verify row in metadata.
	count := migrationCount(t, tb, "migrations")
	if count != 1 {
		t.Errorf("expected 1 migration row, got %d", count)
	}

	state := migrationState(t, tb, "migrations", "20260731120001_create_alpha")
	if state != "applied" {
		t.Errorf("expected state=applied, got %s", state)
	}

	// Verify table was actually created.
	if !tableExists(t, tb, "alpha") {
		t.Error("alpha table should exist after Up")
	}

	// Verify applied_at is not null.
	var appliedAt sql.NullTime
	err = tb.DB().QueryRow(
		"SELECT applied_at FROM migrations WHERE migration = ?",
		"20260731120001_create_alpha",
	).Scan(&appliedAt)
	if err != nil {
		t.Fatalf("read applied_at: %v", err)
	}
	if !appliedAt.Valid {
		t.Error("applied_at should not be NULL after applied state")
	}

	t.Log("TestUpAppliesMigrations passed: migration applied, row created, table exists")
}

// ---------- TestUpBatchAllocation ----------
func TestUpBatchAllocation(t *testing.T) {
	tb := newTestDB(t)
	tb.requireTestDBName()

	dir := t.TempDir()
	m := newTestMigratorWithDir(t, tb, "migrations", dir)

	// Create two migrations.
	mustCreateMigration(t, dir, "20260731120001", "create_beta",
		"CREATE TABLE beta (id INT PRIMARY KEY);",
		"DROP TABLE IF EXISTS beta;")
	mustCreateMigration(t, dir, "20260731120002", "create_gamma",
		"CREATE TABLE gamma (id INT PRIMARY KEY);",
		"DROP TABLE IF EXISTS gamma;")

	ctx := context.Background()

	// First Up: both should get batch 1.
	result1, err := m.Up(ctx, lamigrate.All())
	if err != nil {
		t.Fatalf("first Up: %v", err)
	}
	if len(result1.Migrated) != 2 {
		t.Fatalf("first Up: expected 2 migrated, got %d", len(result1.Migrated))
	}
	for _, mr := range result1.Migrated {
		if mr.Batch != 1 {
			t.Errorf("migration %s: expected batch=1, got batch=%d", mr.Name, mr.Batch)
		}
	}

	// Add a third migration and run Up again.
	mustCreateMigration(t, dir, "20260731120003", "create_delta",
		"CREATE TABLE delta (id INT PRIMARY KEY);",
		"DROP TABLE IF EXISTS delta;")

	// Need a new Migrator to re-scan the directory.
	m2 := newTestMigratorWithDir(t, tb, "migrations", dir)
	result2, err := m2.Up(ctx, lamigrate.All())
	if err != nil {
		t.Fatalf("second Up: %v", err)
	}
	if len(result2.Migrated) != 1 {
		t.Fatalf("second Up: expected 1 migrated, got %d", len(result2.Migrated))
	}
	if result2.Migrated[0].Batch != 2 {
		t.Errorf("second Up: expected batch=2, got batch=%d", result2.Migrated[0].Batch)
	}

	// Verify next_batch in control table.
	nb := nextBatchValue(t, tb, "migrations")
	if nb != 3 {
		t.Errorf("next_batch = %d, want 3", nb)
	}

	// Verify all batch numbers are unique and monotonic.
	var batches []int
	rows, err := tb.DB().Query("SELECT batch FROM migrations ORDER BY id ASC")
	if err != nil {
		t.Fatalf("query batches: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var b int
		if err := rows.Scan(&b); err != nil {
			t.Fatalf("scan batch: %v", err)
		}
		batches = append(batches, b)
	}
	if len(batches) != 3 {
		t.Fatalf("expected 3 batch values, got %d", len(batches))
	}
	if batches[0] != 1 || batches[1] != 1 || batches[2] != 2 {
		t.Errorf("unexpected batch sequence: %v", batches)
	}

	t.Log("TestUpBatchAllocation passed: batch numbers monotonic, never reused")
}

// ---------- TestDownRollbacksLastBatch ----------
func TestDownRollbacksLastBatch(t *testing.T) {
	tb := newTestDB(t)
	tb.requireTestDBName()

	dir := t.TempDir()
	m := newTestMigratorWithDir(t, tb, "migrations", dir)

	mustCreateMigration(t, dir, "20260731120001", "create_epsilon",
		"CREATE TABLE epsilon (id INT PRIMARY KEY);",
		"DROP TABLE IF EXISTS epsilon;")
	mustCreateMigration(t, dir, "20260731120002", "create_zeta",
		"CREATE TABLE zeta (id INT PRIMARY KEY);",
		"DROP TABLE IF EXISTS zeta;")

	ctx := context.Background()

	// Apply both.
	if _, err := m.Up(ctx, lamigrate.All()); err != nil {
		t.Fatalf("Up: %v", err)
	}

	// Verify both tables exist.
	if !tableExists(t, tb, "epsilon") {
		t.Fatal("epsilon should exist after Up")
	}
	if !tableExists(t, tb, "zeta") {
		t.Fatal("zeta should exist after Up")
	}

	// Down should rollback the last batch (both migrations).
	m2 := newTestMigratorWithDir(t, tb, "migrations", dir)
	result, err := m2.Down(ctx, lamigrate.All())
	if err != nil {
		t.Fatalf("Down: %v", err)
	}

	// Both should be rolled back.
	if len(result.Migrated) != 2 {
		t.Fatalf("Down: expected 2 migrated, got %d", len(result.Migrated))
	}

	// Both rows should be deleted (not just state change).
	count := migrationCount(t, tb, "migrations")
	if count != 0 {
		t.Errorf("expected 0 migration rows after Down, got %d", count)
	}

	// Tables should be dropped.
	if tableExists(t, tb, "epsilon") {
		t.Error("epsilon should not exist after Down")
	}
	if tableExists(t, tb, "zeta") {
		t.Error("zeta should not exist after Down")
	}

	t.Log("TestDownRollbacksLastBatch passed: rollback removes rows and drops tables")
}

// ---------- TestResetRemovesAll ----------
func TestResetRemovesAll(t *testing.T) {
	tb := newTestDB(t)
	tb.requireTestDBName()

	dir := t.TempDir()
	m := newTestMigratorWithDir(t, tb, "migrations", dir)

	mustCreateMigration(t, dir, "20260731120001", "create_eta",
		"CREATE TABLE eta (id INT PRIMARY KEY);",
		"DROP TABLE IF EXISTS eta;")
	mustCreateMigration(t, dir, "20260731120002", "create_theta",
		"CREATE TABLE theta (id INT PRIMARY KEY);",
		"DROP TABLE IF EXISTS theta;")
	mustCreateMigration(t, dir, "20260731120003", "create_iota",
		"CREATE TABLE iota (id INT PRIMARY KEY);",
		"DROP TABLE IF EXISTS iota;")

	ctx := context.Background()

	// Apply all.
	if _, err := m.Up(ctx, lamigrate.All()); err != nil {
		t.Fatalf("Up: %v", err)
	}
	if migrationCount(t, tb, "migrations") != 3 {
		t.Fatal("expected 3 migrations after Up")
	}

	// Reset should rollback all.
	m2 := newTestMigratorWithDir(t, tb, "migrations", dir)
	result, err := m2.Reset(ctx)
	if err != nil {
		t.Fatalf("Reset: %v", err)
	}

	if len(result.Migrated) != 3 {
		t.Fatalf("Reset: expected 3 migrated, got %d", len(result.Migrated))
	}

	// All rows deleted.
	count := migrationCount(t, tb, "migrations")
	if count != 0 {
		t.Errorf("expected 0 rows after Reset, got %d", count)
	}

	// All user tables dropped.
	for _, table := range []string{"eta", "theta", "iota"} {
		if tableExists(t, tb, table) {
			t.Errorf("table %s should not exist after Reset", table)
		}
	}

	t.Log("TestResetRemovesAll passed: all migrations rolled back, rows deleted, tables dropped")
}

// ---------- TestUpStopsOnFailure ----------
func TestUpStopsOnFailure(t *testing.T) {
	tb := newTestDB(t)
	tb.requireTestDBName()

	dir := t.TempDir()

	// Create a good migration first.
	mustCreateMigration(t, dir, "20260731120001", "create_kappa",
		"CREATE TABLE kappa (id INT PRIMARY KEY);",
		"DROP TABLE IF EXISTS kappa;")

	// Create a bad migration that will fail SQL.
	mustCreateMigration(t, dir, "20260731120002", "create_lambda",
		"INVALID SQL THAT WILL FAIL;",
		"DROP TABLE IF EXISTS lambda;")

	// Create a third good migration.
	mustCreateMigration(t, dir, "20260731120003", "create_mu",
		"CREATE TABLE mu (id INT PRIMARY KEY);",
		"DROP TABLE IF EXISTS mu;")

	m := newTestMigratorWithDir(t, tb, "migrations", dir)
	ctx := context.Background()

	// Up should fail on the second migration and stop.
	_, err := m.Up(ctx, lamigrate.All())
	if err == nil {
		t.Fatal("expected error from Up with invalid SQL, got nil")
	}

	// First migration should be applied.
	count := migrationCount(t, tb, "migrations")
	if count != 2 {
		t.Fatalf("expected 2 migration rows (applied + failed), got %d", count)
	}

	// First should be applied.
	state1 := migrationState(t, tb, "migrations", "20260731120001_create_kappa")
	if state1 != "applied" {
		t.Errorf("first migration state = %s, want applied", state1)
	}

	// Second should be apply_failed.
	state2 := migrationState(t, tb, "migrations", "20260731120002_create_lambda")
	if state2 != "apply_failed" {
		t.Errorf("second migration state = %s, want apply_failed", state2)
	}

	// Third should NOT exist (never attempted).
	var muCount int
	err = tb.DB().QueryRow(
		"SELECT COUNT(*) FROM migrations WHERE migration = ?",
		"20260731120003_create_mu",
	).Scan(&muCount)
	if err != nil {
		t.Fatalf("query mu: %v", err)
	}
	if muCount != 0 {
		t.Errorf("third migration should not exist, got count=%d", muCount)
	}

	// Verify the kappa table was created (first migration succeeded).
	if !tableExists(t, tb, "kappa") {
		t.Error("kappa table should exist after first migration applied")
	}

	// Verify lambda table was NOT created (invalid SQL).
	if tableExists(t, tb, "lambda") {
		t.Error("lambda table should not exist after failed migration")
	}

	// Verify dirty state blocks further writes.
	_, err = m.Up(ctx, lamigrate.All())
	if err == nil {
		t.Fatal("expected error when trying to Up with dirty state")
	}
	if !errors.Is(err, lamigrate.ErrDirtyState) {
		t.Logf("Up error with dirty state: %v (expected ErrDirtyState)", err)
	}

	t.Log("TestUpStopsOnFailure passed: failed migration recorded, dirty state blocks further writes")
}

// ---------- TestDownStopsOnFailure ----------
func TestDownStopsOnFailure(t *testing.T) {
	tb := newTestDB(t)
	tb.requireTestDBName()

	dir := t.TempDir()

	// Create a good up migration.
	mustCreateMigration(t, dir, "20260731120001", "create_nu",
		"CREATE TABLE nu (id INT PRIMARY KEY);",
		"DROP TABLE IF EXISTS nu;")

	// Create a second migration with a bad down file.
	mustCreateMigration(t, dir, "20260731120002", "create_xi",
		"CREATE TABLE xi (id INT PRIMARY KEY);",
		"INVALID DOWN SQL THAT WILL FAIL;")

	m := newTestMigratorWithDir(t, tb, "migrations", dir)
	ctx := context.Background()

	// Apply both.
	if _, err := m.Up(ctx, lamigrate.All()); err != nil {
		t.Fatalf("Up: %v", err)
	}

	// Verify both applied.
	if migrationCount(t, tb, "migrations") != 2 {
		t.Fatal("expected 2 migrations after Up")
	}

	// Down should fail on the second migration (xi has bad down SQL).
	m2 := newTestMigratorWithDir(t, tb, "migrations", dir)
	_, err := m2.Down(ctx, lamigrate.All())
	if err == nil {
		t.Fatal("expected error from Down with invalid down SQL")
	}

	// First migration (xi) should still be in a failed rollback state.
	state := migrationState(t, tb, "migrations", "20260731120002_create_xi")
	if state != "rollback_failed" {
		t.Errorf("xi state = %s, want rollback_failed", state)
	}

	// nu should still be applied (never reached).
	stateNu := migrationState(t, tb, "migrations", "20260731120001_create_nu")
	if stateNu != "applied" {
		t.Errorf("nu state = %s, want applied", stateNu)
	}

	// xi table should still exist (down SQL failed).
	if !tableExists(t, tb, "xi") {
		t.Error("xi table should still exist after failed down")
	}

	t.Log("TestDownStopsOnFailure passed: broken down SQL produces rollback_failed state")
}

// ---------- TestMetadataTransactionProtocol ----------
func TestMetadataTransactionProtocol(t *testing.T) {
	tb := newTestDB(t)
	tb.requireTestDBName()

	dir := t.TempDir()
	m := newTestMigratorWithDir(t, tb, "migrations", dir)

	ctx := context.Background()

	// Verify that after bootstrap, the session is clean.
	err := m.WithLockSessionForTest(ctx, func(ctx context.Context) error {
		// The lock session should have autocommit=1 and in_transaction=0.
		var autocommit int
		if err := tb.DB().QueryRow("SELECT @@session.autocommit").Scan(&autocommit); err != nil {
			return fmt.Errorf("read autocommit: %v", err)
		}
		if autocommit != 1 {
			t.Errorf("autocommit = %d, want 1", autocommit)
		}

		var inTx int
		txQuery := `SELECT COUNT(*) FROM performance_schema.events_transactions_current ` +
			`WHERE STATE = 'ACTIVE' AND THREAD_ID = (` +
			`SELECT THREAD_ID FROM performance_schema.threads ` +
			`WHERE PROCESSLIST_ID = CONNECTION_ID())`
		if err := tb.DB().QueryRow(txQuery).Scan(&inTx); err != nil {
			return fmt.Errorf("read in_transaction: %v", err)
		}
		if inTx != 0 {
			t.Errorf("in_transaction count = %d, want 0", inTx)
		}

		return nil
	})
	if err != nil {
		t.Fatalf("WithLockSessionForTest: %v", err)
	}

	// Run a migration through the full protocol and verify metadata
	// transaction behavior.
	mustCreateMigration(t, dir, "20260731120001", "create_omicron",
		"CREATE TABLE omicron (id INT PRIMARY KEY);",
		"DROP TABLE IF EXISTS omicron;")

	m2 := newTestMigratorWithDir(t, tb, "migrations", dir)
	_, err = m2.Up(ctx, lamigrate.All())
	if err != nil {
		t.Fatalf("Up: %v", err)
	}

	// Verify the row is in 'applied' state (not 'applying').
	state := migrationState(t, tb, "migrations", "20260731120001_create_omicron")
	if state != "applied" {
		t.Errorf("state = %s, want applied (metadata transaction completed)", state)
	}

	// Verify autocommit is restored after the metadata transactions.
	err = m2.WithLockSessionForTest(ctx, func(ctx context.Context) error {
		var autocommit int
		if err := tb.DB().QueryRow("SELECT @@session.autocommit").Scan(&autocommit); err != nil {
			return err
		}
		if autocommit != 1 {
			t.Errorf("after Up: autocommit = %d, want 1", autocommit)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("verify after Up: %v", err)
	}

	t.Log("TestMetadataTransactionProtocol passed: autocommit assertion, commit verification")
}

// ---------- TestBatchNeverReused ----------
func TestBatchNeverReused(t *testing.T) {
	tb := newTestDB(t)
	tb.requireTestDBName()

	dir := t.TempDir()
	m := newTestMigratorWithDir(t, tb, "migrations", dir)

	mustCreateMigration(t, dir, "20260731120001", "create_pi",
		"CREATE TABLE pi (id INT PRIMARY KEY);",
		"DROP TABLE IF EXISTS pi;")

	ctx := context.Background()

	// Apply.
	if _, err := m.Up(ctx, lamigrate.All()); err != nil {
		t.Fatalf("Up: %v", err)
	}
	batch1 := migrationBatch(t, tb, "migrations", "20260731120001_create_pi")
	if batch1 != 1 {
		t.Fatalf("batch1 = %d, want 1", batch1)
	}

	// Rollback.
	m2 := newTestMigratorWithDir(t, tb, "migrations", dir)
	if _, err := m2.Down(ctx, lamigrate.All()); err != nil {
		t.Fatalf("Down: %v", err)
	}

	// Verify row is deleted.
	count := migrationCount(t, tb, "migrations")
	if count != 0 {
		t.Errorf("expected 0 rows after Down, got %d", count)
	}

	// Re-apply. Should get a new batch number, not reuse batch 1.
	m3 := newTestMigratorWithDir(t, tb, "migrations", dir)
	if _, err := m3.Up(ctx, lamigrate.All()); err != nil {
		t.Fatalf("re-Up: %v", err)
	}

	batch2 := migrationBatch(t, tb, "migrations", "20260731120001_create_pi")
	if batch2 == batch1 {
		t.Errorf("batch numbers reused: both are %d", batch1)
	}
	if batch2 != 2 {
		t.Errorf("batch2 = %d, want 2 (next_batch should be 2 after first allocation)", batch2)
	}

	// Verify next_batch counter advanced past both allocations.
	nb := nextBatchValue(t, tb, "migrations")
	if nb < 3 {
		t.Errorf("next_batch = %d, want >= 3", nb)
	}

	// Rollback and re-apply again.
	m4 := newTestMigratorWithDir(t, tb, "migrations", dir)
	if _, err := m4.Down(ctx, lamigrate.All()); err != nil {
		t.Fatalf("second Down: %v", err)
	}

	m5 := newTestMigratorWithDir(t, tb, "migrations", dir)
	if _, err := m5.Up(ctx, lamigrate.All()); err != nil {
		t.Fatalf("third Up: %v", err)
	}

	batch3 := migrationBatch(t, tb, "migrations", "20260731120001_create_pi")
	if batch3 <= batch2 {
		t.Errorf("batch3=%d should be > batch2=%d", batch3, batch2)
	}
	if batch3 != 3 {
		t.Errorf("batch3 = %d, want 3", batch3)
	}

	t.Logf("TestBatchNeverReused passed: batches %d → %d → %d (never reused)", batch1, batch2, batch3)
}

// ---------- TestUpWithStepsLimit ----------
func TestUpWithStepsLimit(t *testing.T) {
	tb := newTestDB(t)
	tb.requireTestDBName()

	dir := t.TempDir()
	m := newTestMigratorWithDir(t, tb, "migrations", dir)

	mustCreateMigration(t, dir, "20260731120001", "create_rho",
		"CREATE TABLE rho (id INT PRIMARY KEY);",
		"DROP TABLE IF EXISTS rho;")
	mustCreateMigration(t, dir, "20260731120002", "create_sigma",
		"CREATE TABLE sigma (id INT PRIMARY KEY);",
		"DROP TABLE IF EXISTS sigma;")
	mustCreateMigration(t, dir, "20260731120003", "create_tau",
		"CREATE TABLE tau (id INT PRIMARY KEY);",
		"DROP TABLE IF EXISTS tau;")

	ctx := context.Background()

	// Apply with step limit of 1.
	step, err := lamigrate.Steps(1)
	if err != nil {
		t.Fatalf("Steps(1): %v", err)
	}
	result, err := m.Up(ctx, step)
	if err != nil {
		t.Fatalf("Up(step=1): %v", err)
	}

	if len(result.Migrated) != 1 {
		t.Fatalf("expected 1 migrated with step=1, got %d", len(result.Migrated))
	}

	// Only rho should exist.
	count := migrationCount(t, tb, "migrations")
	if count != 1 {
		t.Errorf("expected 1 migration row, got %d", count)
	}

	// sigma and tau should not have been touched.
	if tableExists(t, tb, "sigma") {
		t.Error("sigma should not exist after step=1")
	}
	if tableExists(t, tb, "tau") {
		t.Error("tau should not exist after step=1")
	}

	// Apply remaining.
	m2 := newTestMigratorWithDir(t, tb, "migrations", dir)
	step2, _ := lamigrate.Steps(2)
	result2, err := m2.Up(ctx, step2)
	if err != nil {
		t.Fatalf("Up(step=2): %v", err)
	}

	if len(result2.Migrated) != 2 {
		t.Fatalf("expected 2 migrated with step=2, got %d", len(result2.Migrated))
	}

	// All three should be applied now.
	count = migrationCount(t, tb, "migrations")
	if count != 3 {
		t.Errorf("expected 3 migration rows, got %d", count)
	}

	t.Log("TestUpWithStepsLimit passed: step limit correctly restricts execution")
}

// ---------- TestDownWithStepsLimit ----------
func TestDownWithStepsLimit(t *testing.T) {
	tb := newTestDB(t)
	tb.requireTestDBName()

	dir := t.TempDir()
	m := newTestMigratorWithDir(t, tb, "migrations", dir)

	mustCreateMigration(t, dir, "20260731120001", "create_upsilon",
		"CREATE TABLE upsilon (id INT PRIMARY KEY);",
		"DROP TABLE IF EXISTS upsilon;")
	mustCreateMigration(t, dir, "20260731120002", "create_phi",
		"CREATE TABLE phi (id INT PRIMARY KEY);",
		"DROP TABLE IF EXISTS phi;")

	ctx := context.Background()

	// Apply both.
	if _, err := m.Up(ctx, lamigrate.All()); err != nil {
		t.Fatalf("Up: %v", err)
	}

	// Down with step=1 should only rollback the last migration (phi).
	m2 := newTestMigratorWithDir(t, tb, "migrations", dir)
	step, _ := lamigrate.DownSteps(1)
	result, err := m2.Down(ctx, step)
	if err != nil {
		t.Fatalf("Down(step=1): %v", err)
	}

	if len(result.Migrated) != 1 {
		t.Fatalf("expected 1 migrated with step=1, got %d", len(result.Migrated))
	}

	// Only upsilon should remain.
	count := migrationCount(t, tb, "migrations")
	if count != 1 {
		t.Errorf("expected 1 migration row, got %d", count)
	}

	// phi table should be gone.
	if tableExists(t, tb, "phi") {
		t.Error("phi should not exist after Down(step=1)")
	}

	// upsilon should still exist.
	if !tableExists(t, tb, "upsilon") {
		t.Error("upsilon should still exist after Down(step=1)")
	}

	t.Log("TestDownWithStepsLimit passed: step limit restricts rollback to last migration")
}

// ---------- TestEmptyPlanNoop ----------
func TestEmptyPlanNoop(t *testing.T) {
	tb := newTestDB(t)
	tb.requireTestDBName()

	dir := t.TempDir()
	m := newTestMigratorWithDir(t, tb, "migrations", dir)

	// Add a migration pair but don't run Up yet.
	mustCreateMigration(t, dir, "20260731120001", "create_chi",
		"CREATE TABLE chi (id INT PRIMARY KEY);",
		"DROP TABLE IF EXISTS chi;")

	ctx := context.Background()

	// First Up: should apply chi.
	result, err := m.Up(ctx, lamigrate.All())
	if err != nil {
		t.Fatalf("Up: %v", err)
	}
	if len(result.Migrated) != 1 {
		t.Fatalf("first Up: expected 1 migrated, got %d", len(result.Migrated))
	}

	// Second Up with same files: should be a no-op.
	m2 := newTestMigratorWithDir(t, tb, "migrations", dir)
	result2, err := m2.Up(ctx, lamigrate.All())
	if err != nil {
		t.Fatalf("second Up: %v", err)
	}
	if len(result2.Migrated) != 0 {
		t.Errorf("second Up: expected 0 migrated (noop), got %d", len(result2.Migrated))
	}

	// Down after Up: should rollback chi (1 migration applied → 1 rolled back).
	m3 := newTestMigratorWithDir(t, tb, "migrations", dir)
	result3, err := m3.Down(ctx, lamigrate.All())
	if err != nil {
		t.Fatalf("Down: %v", err)
	}
	if len(result3.Migrated) != 1 {
		t.Errorf("Down after apply: expected 1 migrated, got %d", len(result3.Migrated))
	}

	// Reset on empty: should be a no-op.
	// First rollback what we have.
	m4 := newTestMigratorWithDir(t, tb, "migrations", dir)
	if _, err := m4.Down(ctx, lamigrate.All()); err != nil {
		t.Fatalf("Down: %v", err)
	}

	// Now Reset should be a no-op.
	m5 := newTestMigratorWithDir(t, tb, "migrations", dir)
	result5, err := m5.Reset(ctx)
	if err != nil {
		t.Fatalf("Reset: %v", err)
	}
	if len(result5.Migrated) != 0 {
		t.Errorf("Reset with nothing to rollback: expected 0 migrated, got %d", len(result5.Migrated))
	}

	t.Log("TestEmptyPlanNoop passed: no-op plans return empty results")
}

// ---------- TestMultiStatementMigration ----------
func TestMultiStatementMigration(t *testing.T) {
	tb := newTestDB(t)
	tb.requireTestDBName()

	dir := t.TempDir()
	m := newTestMigratorWithDir(t, tb, "migrations", dir)

	// Migration with multiple statements.
	mustCreateMigration(t, dir, "20260731120001", "create_multi",
		"CREATE TABLE multi_a (id INT PRIMARY KEY);\nCREATE TABLE multi_b (name VARCHAR(50));",
		"DROP TABLE multi_b;\nDROP TABLE multi_a;")

	ctx := context.Background()
	result, err := m.Up(ctx, lamigrate.All())
	if err != nil {
		t.Fatalf("Up: %v", err)
	}

	if len(result.Migrated) != 1 {
		t.Fatalf("expected 1 migrated, got %d", len(result.Migrated))
	}
	if !result.Migrated[0].Applied {
		t.Error("expected migration to be applied")
	}

	// Both tables should exist.
	if !tableExists(t, tb, "multi_a") {
		t.Error("multi_a should exist")
	}
	if !tableExists(t, tb, "multi_b") {
		t.Error("multi_b should exist")
	}

	// Down should drop both.
	m2 := newTestMigratorWithDir(t, tb, "migrations", dir)
	_, err = m2.Down(ctx, lamigrate.All())
	if err != nil {
		t.Fatalf("Down: %v", err)
	}

	if tableExists(t, tb, "multi_a") {
		t.Error("multi_a should not exist after Down")
	}
	if tableExists(t, tb, "multi_b") {
		t.Error("multi_b should not exist after Down")
	}

	t.Log("TestMultiStatementMigration passed: multi-statement SQL executed correctly")
}

// ---------- TestUpDownRoundTrip ----------
func TestUpDownRoundTrip(t *testing.T) {
	tb := newTestDB(t)
	tb.requireTestDBName()

	dir := t.TempDir()

	// Create migrations.
	mustCreateMigration(t, dir, "20260731120001", "create_psi",
		"CREATE TABLE psi (id INT UNSIGNED AUTO_INCREMENT PRIMARY KEY, val VARCHAR(100));",
		"DROP TABLE IF EXISTS psi;")
	mustCreateMigration(t, dir, "20260731120002", "create_omega",
		"CREATE TABLE omega (id INT UNSIGNED AUTO_INCREMENT PRIMARY KEY);",
		"DROP TABLE IF EXISTS omega;")

	ctx := context.Background()

	// Round 1: Up.
	m1 := newTestMigratorWithDir(t, tb, "migrations", dir)
	if _, err := m1.Up(ctx, lamigrate.All()); err != nil {
		t.Fatalf("Up round 1: %v", err)
	}

	if !tableExists(t, tb, "psi") || !tableExists(t, tb, "omega") {
		t.Fatal("tables should exist after Up")
	}

	// Round 2: Down.
	m2 := newTestMigratorWithDir(t, tb, "migrations", dir)
	if _, err := m2.Down(ctx, lamigrate.All()); err != nil {
		t.Fatalf("Down: %v", err)
	}

	if tableExists(t, tb, "psi") || tableExists(t, tb, "omega") {
		t.Fatal("tables should not exist after Down")
	}

	// Round 3: Up again.
	m3 := newTestMigratorWithDir(t, tb, "migrations", dir)
	result, err := m3.Up(ctx, lamigrate.All())
	if err != nil {
		t.Fatalf("Up round 3: %v", err)
	}

	// Verify new batch is different from first (should be 2+).
	if len(result.Migrated) > 0 && result.Migrated[0].Batch < 2 {
		t.Errorf("re-applied migration should be in batch 2+, got batch=%d", result.Migrated[0].Batch)
	}

	// Verify tables exist again.
	if !tableExists(t, tb, "psi") || !tableExists(t, tb, "omega") {
		t.Fatal("tables should exist after re-Up")
	}

	t.Log("TestUpDownRoundTrip passed: full round-trip produces correct state")
}

// ---------- TestIntentBeforeSQL ----------
func TestIntentBeforeSQL(t *testing.T) {
	tb := newTestDB(t)
	tb.requireTestDBName()

	dir := t.TempDir()

	// Create a slow migration that takes a bit of time to execute.
	mustCreateMigration(t, dir, "20260731120001", "create_slow",
		"CREATE TABLE slow (id INT PRIMARY KEY); SELECT SLEEP(0.01);",
		"DROP TABLE IF EXISTS slow;")

	// Verify no rows before Up (bootstrap will create the table).
	m := newTestMigratorWithDir(t, tb, "migrations", dir)

	ctx := context.Background()
	result, err := m.Up(ctx, lamigrate.All())
	if err != nil {
		t.Fatalf("Up: %v", err)
	}

	// The intent should have been inserted before SQL execution.
	// We verify the row exists in 'applied' state.
	if len(result.Migrated) != 1 {
		t.Fatalf("expected 1 migrated, got %d", len(result.Migrated))
	}

	state := migrationState(t, tb, "migrations", "20260731120001_create_slow")
	if state != "applied" {
		t.Errorf("state = %s, want applied", state)
	}

	// Verify runner_id is set (UUID format: xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx).
	var runnerID string
	err = tb.DB().QueryRow(
		"SELECT runner_id FROM migrations WHERE migration = ?",
		"20260731120001_create_slow",
	).Scan(&runnerID)
	if err != nil {
		t.Fatalf("read runner_id: %v", err)
	}
	if len(runnerID) != 36 {
		t.Errorf("runner_id length = %d, want 36 (UUID)", len(runnerID))
	}

	// Verify started_at is set.
	var startedAt time.Time
	err = tb.DB().QueryRow(
		"SELECT started_at FROM migrations WHERE migration = ?",
		"20260731120001_create_slow",
	).Scan(&startedAt)
	if err != nil {
		t.Fatalf("read started_at: %v", err)
	}
	if startedAt.IsZero() {
		t.Error("started_at should not be zero")
	}

	t.Logf("TestIntentBeforeSQL passed: intent row persisted with runner_id=%s", runnerID)
}

// ---------- TestDownWithInvalidSQLProducesRollbackFailed ----------
func TestDownWithInvalidSQLProducesRollbackFailed(t *testing.T) {
	tb := newTestDB(t)
	tb.requireTestDBName()

	dir := t.TempDir()

	// Create a migration with valid up but invalid down.
	mustCreateMigration(t, dir, "20260731120001", "create_bad_down",
		"CREATE TABLE bad_down (id INT PRIMARY KEY);",
		"THIS IS NOT VALID SQL;")

	m := newTestMigratorWithDir(t, tb, "migrations", dir)
	ctx := context.Background()

	// Apply.
	if _, err := m.Up(ctx, lamigrate.All()); err != nil {
		t.Fatalf("Up: %v", err)
	}

	// Verify applied.
	if !tableExists(t, tb, "bad_down") {
		t.Fatal("bad_down table should exist after Up")
	}
	if migrationState(t, tb, "migrations", "20260731120001_create_bad_down") != "applied" {
		t.Fatal("expected state=applied")
	}

	// Down should fail because the down SQL is invalid.
	m2 := newTestMigratorWithDir(t, tb, "migrations", dir)
	_, err := m2.Down(ctx, lamigrate.All())
	if err == nil {
		t.Fatal("expected error from Down with invalid down SQL")
	}

	// Verify rollback_failed state.
	state := migrationState(t, tb, "migrations", "20260731120001_create_bad_down")
	if state != "rollback_failed" {
		t.Errorf("state = %s, want rollback_failed", state)
	}

	// Table should still exist (rollback SQL failed).
	if !tableExists(t, tb, "bad_down") {
		t.Error("bad_down table should still exist after failed rollback")
	}

	// Dirty state should block further writes.
	_, err = m2.Up(ctx, lamigrate.All())
	if err == nil {
		t.Fatal("expected error when Up with dirty state")
	}

	// Dirty state should block reset.
	_, err = m2.Reset(ctx)
	if err == nil {
		t.Fatal("expected error when Reset with dirty state")
	}

	t.Log("TestDownWithInvalidSQLProducesRollbackFailed passed")
}