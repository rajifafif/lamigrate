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

// metadata_test.go — Integration tests for metadata schema, validation,
// bootstrap, and batch allocation.
//
// All tests use the harness with lamigrate_test_ prefix databases.

// ---------- TestBootstrapCreatesTables ----------

// TestBootstrapCreatesTables verifies that bootstrapping an empty
// database creates both the lamigrate_control table and the requested
// migration-state table with the correct v1 schema.
func TestBootstrapCreatesTables(t *testing.T) {
	tb := newTestDB(t)
	tb.requireTestDBName()
	m := newTestMigrator(t, tb, "migrations")

	// Use WithLockSessionForTest to run bootstrap under a lock.
	// Since bootstrap is called internally, we'll call it directly
	// via the public API pattern: try to read metadata which triggers
	// bootstrap when metadata is not initialized.
	//
	// For this test, we use a custom approach: create the Migrator and
	// invoke bootstrap by attempting a Status call (which will fail
	// because status isn't implemented yet, so we test bootstrap
	// directly through the private API via a helper).
	err := runBootstrap(t, m)
	if err != nil {
		t.Fatalf("bootstrap failed: %v", err)
	}

	// Verify lamigrate_control table exists.
	var controlExists int
	err = tb.DB().QueryRow(
		"SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = ? AND table_name = 'lamigrate_control'",
		tb.Name,
	).Scan(&controlExists)
	if err != nil {
		t.Fatalf("query control table existence: %v", err)
	}
	if controlExists != 1 {
		t.Fatal("lamigrate_control table was not created")
	}

	// Verify migrations table exists.
	var stateExists int
	err = tb.DB().QueryRow(
		"SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = ? AND table_name = 'migrations'",
		tb.Name,
	).Scan(&stateExists)
	if err != nil {
		t.Fatalf("query state table existence: %v", err)
	}
	if stateExists != 1 {
		t.Fatal("migrations table was not created")
	}

	t.Log("bootstrap created both lamigrate_control and migrations tables")
}

// ---------- TestBootstrapIdempotent ----------

// TestBootstrapIdempotent verifies that running bootstrap twice
// against the same database is safe and idempotent.
func TestBootstrapIdempotent(t *testing.T) {
	tb := newTestDB(t)
	tb.requireTestDBName()
	m := newTestMigrator(t, tb, "migrations")

	// First bootstrap.
	if err := runBootstrap(t, m); err != nil {
		t.Fatalf("first bootstrap failed: %v", err)
	}

	// Second bootstrap — should be a no-op.
	if err := runBootstrap(t, m); err != nil {
		t.Fatalf("second bootstrap failed (should be idempotent): %v", err)
	}

	// Verify control row is still correct.
	var schemaVersion, nextBatch int
	err := tb.DB().QueryRow(
		"SELECT schema_version, next_batch FROM lamigrate_control WHERE tracking_table = 'migrations'",
	).Scan(&schemaVersion, &nextBatch)
	if err != nil {
		t.Fatalf("query control row: %v", err)
	}
	if schemaVersion != 1 {
		t.Errorf("schema_version = %d, want 1", schemaVersion)
	}
	if nextBatch != 1 {
		t.Errorf("next_batch = %d, want 1", nextBatch)
	}

	t.Log("bootstrap is idempotent — running twice produces same result")
}

// ---------- TestControlRowInitialValues ----------

// TestControlRowInitialValues verifies that the initial control row
// has schema_version=1 and next_batch=1.
func TestControlRowInitialValues(t *testing.T) {
	tb := newTestDB(t)
	tb.requireTestDBName()
	m := newTestMigrator(t, tb, "migrations")

	if err := runBootstrap(t, m); err != nil {
		t.Fatalf("bootstrap failed: %v", err)
	}

	var schemaVersion int
	var nextBatch int
	err := tb.DB().QueryRow(
		"SELECT schema_version, next_batch FROM lamigrate_control WHERE tracking_table = 'migrations'",
	).Scan(&schemaVersion, &nextBatch)
	if err != nil {
		t.Fatalf("query control row: %v", err)
	}

	if schemaVersion != 1 {
		t.Errorf("schema_version = %d, want 1", schemaVersion)
	}
	if nextBatch != 1 {
		t.Errorf("next_batch = %d, want 1", nextBatch)
	}

	// Verify updated_at is a recent timestamp.
	var updatedAt time.Time
	err = tb.DB().QueryRow(
		"SELECT updated_at FROM lamigrate_control WHERE tracking_table = 'migrations'",
	).Scan(&updatedAt)
	if err != nil {
		t.Fatalf("query updated_at: %v", err)
	}
	if updatedAt.IsZero() {
		t.Error("updated_at should not be zero")
	}

	// Verify tracking_table column uses ASCII charset.
	var charset, collation string
	err = tb.DB().QueryRow(
		"SELECT character_set_name, collation_name FROM information_schema.columns "+
			"WHERE table_schema = ? AND table_name = 'lamigrate_control' AND column_name = 'tracking_table'",
		tb.Name,
	).Scan(&charset, &collation)
	if err != nil {
		t.Fatalf("query tracking_table charset: %v", err)
	}
	if charset != "ascii" {
		t.Errorf("tracking_table charset = %q, want ascii", charset)
	}
	if !strings.Contains(collation, "bin") {
		t.Errorf("tracking_table collation = %q, want binary", collation)
	}

	t.Logf("control row: schema_version=%d, next_batch=%d, updated_at=%v",
		schemaVersion, nextBatch, updatedAt)
}

// ---------- TestValidateTableShape ----------

// TestValidateTableShape verifies that a correctly created v1
// table passes shape validation.
func TestValidateTableShape(t *testing.T) {
	tb := newTestDB(t)
	tb.requireTestDBName()
	m := newTestMigrator(t, tb, "migrations")

	if err := runBootstrap(t, m); err != nil {
		t.Fatalf("bootstrap failed: %v", err)
	}

	// Use a Migrator session to run validateTableShape.
	// We access it through the lock session lifecycle.
	err := m.WithLockSessionForTest(context.Background(), func(ctx context.Context) error {
		return nil
	})
	if err != nil {
		t.Fatalf("withLockSession: %v", err)
	}

	// Directly validate both tables.
	// Open a connection for validation.
	conn := openTestConn(t, tb.DSN)
	defer conn.Close()

	ctx := context.Background()

	// Validate state table.
	err = lamigrate.ValidateTableShapeForTest(ctx, conn, tb.Name, "migrations", "state")
	if err != nil {
		t.Errorf("validateTableShape(migrations, state) failed: %v", err)
	}

	// Validate control table.
	err = lamigrate.ValidateTableShapeForTest(ctx, conn, tb.Name, "lamigrate_control", "control")
	if err != nil {
		t.Errorf("validateTableShape(lamigrate_control, control) failed: %v", err)
	}

	t.Log("both tables pass shape validation")
}

// ---------- TestValidateRejectsExtraColumn ----------

// TestValidateRejectsExtraColumn verifies that a table with an extra
// column is rejected by shape validation.
func TestValidateRejectsExtraColumn(t *testing.T) {
	tb := newTestDB(t)
	tb.requireTestDBName()

	// Create a table with the correct v1 schema plus an extra column.
	_, err := tb.Exec(`CREATE TABLE migrations (
		id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT PRIMARY KEY,
		migration VARCHAR(255) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
		source_kind VARCHAR(24) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
		source_version BIGINT UNSIGNED NULL,
		source_name VARCHAR(255) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
		up_checksum BINARY(32) NOT NULL,
		down_checksum BINARY(32) NULL,
		batch BIGINT UNSIGNED NOT NULL,
		state VARCHAR(24) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
		is_baseline BOOLEAN NOT NULL DEFAULT FALSE,
		runner_id CHAR(36) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
		started_at DATETIME(6) NOT NULL,
		applied_at DATETIME(6) NULL,
		updated_at DATETIME(6) NOT NULL,
		extra_column VARCHAR(100),
		UNIQUE KEY uk_migration (migration),
		KEY idx_batch_state (batch, state)
	) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci`)
	if err != nil {
		t.Fatalf("create table with extra column: %v", err)
	}

	conn := openTestConn(t, tb.DSN)
	defer conn.Close()

	err = lamigrate.ValidateTableShapeForTest(context.Background(), conn, tb.Name, "migrations", "state")
	if err == nil {
		t.Fatal("expected error for table with extra column, got nil")
	}
	if !strings.Contains(err.Error(), "extra columns") {
		t.Errorf("expected error about extra columns, got: %v", err)
	}

	t.Logf("correctly rejected table with extra column: %v", err)
}

// ---------- TestAllocateBatchMonotonic ----------

// TestAllocateBatchMonotonic verifies that batch numbers increment
// monotonically and are never reused.
func TestAllocateBatchMonotonic(t *testing.T) {
	tb := newTestDB(t)
	tb.requireTestDBName()
	m := newTestMigrator(t, tb, "migrations")

	if err := runBootstrap(t, m); err != nil {
		t.Fatalf("bootstrap failed: %v", err)
	}

	// Open a connection for batch allocation.
	conn := openTestConn(t, tb.DSN)
	defer conn.Close()

	ctx := context.Background()

	// Allocate several batches.
	batches := make([]uint64, 5)
	for i := range batches {
		batch, err := lamigrate.AllocateBatchForTest(ctx, conn, "migrations")
		if err != nil {
			t.Fatalf("allocateBatch iteration %d: %v", i, err)
		}
		batches[i] = batch
		t.Logf("allocated batch %d", batch)
	}

	// All batch numbers should be strictly increasing.
	for i := 1; i < len(batches); i++ {
		if batches[i] <= batches[i-1] {
			t.Errorf("batch[%d]=%d <= batch[%d]=%d, expected monotonic",
				i, batches[i], i-1, batches[i-1])
		}
	}

	// First batch should be 1.
	if batches[0] != 1 {
		t.Errorf("first batch = %d, want 1", batches[0])
	}

	// No reuse: all values should be unique.
	seen := make(map[uint64]bool)
	for _, b := range batches {
		if seen[b] {
			t.Errorf("batch %d reused", b)
		}
		seen[b] = true
	}

	// Verify next_batch in control table.
	var nextBatch uint64
	err := tb.DB().QueryRow(
		"SELECT next_batch FROM lamigrate_control WHERE tracking_table = 'migrations'",
	).Scan(&nextBatch)
	if err != nil {
		t.Fatalf("query next_batch: %v", err)
	}
	expectedNext := uint64(len(batches) + 1)
	if nextBatch != expectedNext {
		t.Errorf("next_batch = %d, want %d", nextBatch, expectedNext)
	}

	t.Log("batch allocation is monotonic and never reuses")
}

// ---------- TestPrototypeShapeDetected ----------

// TestPrototypeShapeDetected verifies that a 4-column prototype table
// is detected by the inventory function.
func TestPrototypeShapeDetected(t *testing.T) {
	tb := newTestDB(t)
	tb.requireTestDBName()

	// Create the old 4-column prototype table.
	_, err := tb.Exec(`CREATE TABLE migrations (
		id INT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
		migration VARCHAR(255) NOT NULL,
		batch INT UNSIGNED NOT NULL,
		applied_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
		UNIQUE KEY uk_migration (migration)
	)`)
	if err != nil {
		t.Fatalf("create prototype table: %v", err)
	}

	// Bootstrap should detect the prototype and reject it.
	m := newTestMigrator(t, tb, "migrations")
	err = runBootstrap(t, m)
	if err == nil {
		t.Fatal("expected error for prototype table, got nil")
	}
	if !strings.Contains(err.Error(), "prototype") && !strings.Contains(err.Error(), "adopt-prototype") {
		t.Errorf("expected prototype-related error, got: %v", err)
	}

	t.Logf("prototype table correctly detected and rejected: %v", err)
}

// ---------- TestIncompatibleMetadataRejected ----------

// TestIncompatibleMetadataRejected verifies that a table with an
// incompatible schema version is rejected.
func TestIncompatibleMetadataRejected(t *testing.T) {
	tb := newTestDB(t)
	tb.requireTestDBName()

	// Create a lamigrate_control table with schema_version=99.
	_, err := tb.Exec(`CREATE TABLE lamigrate_control (
		tracking_table VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
		schema_version INT UNSIGNED NOT NULL,
		next_batch BIGINT UNSIGNED NOT NULL,
		updated_at DATETIME(6) NOT NULL,
		PRIMARY KEY (tracking_table)
	) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci`)
	if err != nil {
		t.Fatalf("create control table: %v", err)
	}

	_, err = tb.Exec(
		"INSERT INTO lamigrate_control (tracking_table, schema_version, next_batch, updated_at) VALUES ('migrations', 99, 1, NOW(6))",
	)
	if err != nil {
		t.Fatalf("insert control row: %v", err)
	}

	// Also create the v1 state table.
	_, err = tb.Exec(`CREATE TABLE migrations (
		id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT PRIMARY KEY,
		migration VARCHAR(255) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
		source_kind VARCHAR(24) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
		source_version BIGINT UNSIGNED NULL,
		source_name VARCHAR(255) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
		up_checksum BINARY(32) NOT NULL,
		down_checksum BINARY(32) NULL,
		batch BIGINT UNSIGNED NOT NULL,
		state VARCHAR(24) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
		is_baseline BOOLEAN NOT NULL DEFAULT FALSE,
		runner_id CHAR(36) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
		started_at DATETIME(6) NOT NULL,
		applied_at DATETIME(6) NULL,
		updated_at DATETIME(6) NOT NULL,
		UNIQUE KEY uk_migration (migration),
		KEY idx_batch_state (batch, state),
		CONSTRAINT migrations_chk_state CHECK (state IN ('applying', 'applied', 'apply_failed', 'rolling_back', 'rollback_failed')),
		CONSTRAINT migrations_chk_source CHECK (source_kind IN ('timestamp', 'golang_migrate')),
		CONSTRAINT migrations_chk_fields CHECK (
			(source_kind = 'timestamp' AND source_version IS NULL AND is_baseline = FALSE AND batch > 0)
			OR
			(source_kind = 'golang_migrate' AND source_version IS NOT NULL AND is_baseline = TRUE AND batch = 0 AND state = 'applied')
		),
		CONSTRAINT migrations_chk_times CHECK (
			(state IN ('applying', 'apply_failed') AND applied_at IS NULL)
			OR
			(state IN ('applied', 'rolling_back', 'rollback_failed') AND applied_at IS NOT NULL)
		)
	) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci`)
	if err != nil {
		t.Fatalf("create state table: %v", err)
	}

	// Bootstrap should fail because schema_version is 99.
	m := newTestMigrator(t, tb, "migrations")
	err = runBootstrap(t, m)
	if err == nil {
		t.Fatal("expected error for incompatible metadata, got nil")
	}

	t.Logf("incompatible metadata correctly rejected: %v", err)
}

// ---------- TestCustomTableName ----------

// TestCustomTableName verifies that bootstrap works with a custom
// tracking table name.
func TestCustomTableName(t *testing.T) {
	tb := newTestDB(t)
	tb.requireTestDBName()
	m := newTestMigrator(t, tb, "custom_migrations")

	if err := runBootstrap(t, m); err != nil {
		t.Fatalf("bootstrap with custom table failed: %v", err)
	}

	// Verify the custom table exists.
	var exists int
	err := tb.DB().QueryRow(
		"SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = ? AND table_name = 'custom_migrations'",
		tb.Name,
	).Scan(&exists)
	if err != nil {
		t.Fatalf("query custom table: %v", err)
	}
	if exists != 1 {
		t.Fatal("custom_migrations table was not created")
	}

	// Verify control row uses the custom table name.
	var trackedTable string
	err = tb.DB().QueryRow(
		"SELECT tracking_table FROM lamigrate_control WHERE tracking_table = 'custom_migrations'",
	).Scan(&trackedTable)
	if err != nil {
		t.Fatalf("query control row for custom table: %v", err)
	}
	if trackedTable != "custom_migrations" {
		t.Errorf("tracking_table = %q, want %q", trackedTable, "custom_migrations")
	}

	t.Log("custom table name bootstrap works correctly")
}

// ---------- TestMultipleScopes ----------

// TestMultipleScopes verifies that different tracking tables can be
// bootstrapped independently in the same database.
func TestMultipleScopes(t *testing.T) {
	tb := newTestDB(t)
	tb.requireTestDBName()

	m1 := newTestMigrator(t, tb, "migrations_a")
	m2 := newTestMigrator(t, tb, "migrations_b")

	if err := runBootstrap(t, m1); err != nil {
		t.Fatalf("bootstrap migrations_a: %v", err)
	}
	if err := runBootstrap(t, m2); err != nil {
		t.Fatalf("bootstrap migrations_b: %v", err)
	}

	// Both should have their own control rows.
	var count int
	err := tb.DB().QueryRow(
		"SELECT COUNT(*) FROM lamigrate_control",
	).Scan(&count)
	if err != nil {
		t.Fatalf("query control rows: %v", err)
	}
	if count != 2 {
		t.Errorf("expected 2 control rows, got %d", count)
	}

	// Both state tables should exist.
	for _, name := range []string{"migrations_a", "migrations_b"} {
		var exists int
		err := tb.DB().QueryRow(
			"SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = ? AND table_name = ?",
			tb.Name, name,
		).Scan(&exists)
		if err != nil {
			t.Fatalf("query table %s: %v", name, err)
		}
		if exists != 1 {
			t.Errorf("table %s was not created", name)
		}
	}

	t.Log("multiple scopes bootstrapped independently")
}

// ---------- helpers ----------

// openTestConn opens a single dedicated connection for test use.
func openTestConn(t *testing.T, dsn string) *sql.Conn {
	t.Helper()
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	db.SetMaxOpenConns(1)
	conn, err := db.Conn(context.Background())
	if err != nil {
		db.Close()
		t.Fatalf("conn: %v", err)
	}
	t.Cleanup(func() {
		conn.Close()
		db.Close()
	})
	return conn
}

// runBootstrap creates a Migrator from a test DSN and runs bootstrap.
// Bootstrap manages its own sessions and locks internally; it should
// NOT be wrapped in WithLockSessionForTest.
func runBootstrap(t *testing.T, m *lamigrate.Migrator) error {
	t.Helper()
	return m.BootstrapForTest(context.Background())
}

// createMigratorFromDSN creates a Migrator directly from a DSN for
// use in tests where we need fine-grained control.
func createMigratorFromDSN(t *testing.T, dsn, tableName string) *lamigrate.Migrator {
	t.Helper()
	cfg, err := mysql.ParseDSN(dsn)
	if err != nil {
		t.Fatalf("parse DSN: %v", err)
	}
	cfg.MultiStatements = true
	cfg.ParseTime = true

	m, err := lamigrate.NewMySQL(cfg, lamigrate.Options{
		Directory:   t.TempDir(),
		TableName:   tableName,
		LockTimeout: 10 * time.Second,
	})
	if err != nil {
		t.Fatalf("NewMySQL: %v", err)
	}
	return m
}

// testDBPrefix and defaultDSN are already defined in harness_test.go.

// defaultDSNOverride is the test DSN without database name.
const defaultDSNOverride = "root:reviewpass@tcp(127.0.0.1:56198)/?multiStatements=true&parseTime=true"

// Ensure fmt is used.
var _ = fmt.Sprintf
