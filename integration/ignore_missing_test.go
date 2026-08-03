//go:build integration

package integration

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/go-sql-driver/mysql"
	lamigrate "github.com/rajifafif/lamigrate"
)

// newTestMigratorWithOpts builds a Migrator with explicit Options, for
// tests that need to exercise Option-level behavior such as
// IgnoreMissingSource.
func newTestMigratorWithOpts(tb *testing.T, testDB *TestDB, tableName, dir string, opts lamigrate.Options) *lamigrate.Migrator {
	tb.Helper()

	cfg, err := mysql.ParseDSN(testDB.DSN)
	if err != nil {
		tb.Fatalf("parse DSN: %v", err)
	}
	cfg.MultiStatements = true
	cfg.ParseTime = true

	opts.Directory = dir
	opts.TableName = tableName
	if opts.LockTimeout == 0 {
		opts.LockTimeout = 10 * time.Second
	}

	m, err := lamigrate.NewMySQL(cfg, opts)
	if err != nil {
		tb.Fatalf("NewMySQL: %v", err)
	}
	return m
}

// TestIgnoreMissingSourceLetsUpProceed verifies that with the option set,
// an orphaned applied migration (source file removed) does not block
// `up`, while the orphan metadata row is left intact.
func TestIgnoreMissingSourceLetsUpProceed(t *testing.T) {
	tb := newTestDB(t)
	tb.requireTestDBName()

	tableName := "migrations"
	dir := t.TempDir()

	// 1. Create and apply a real migration.
	mustCreateMigration(t, dir, "20260801000001", "create_base", "CREATE TABLE base_t (id INT PRIMARY KEY);", "DROP TABLE IF EXISTS base_t;")
	m := newTestMigratorWithDir(t, tb, tableName, dir)
	ctx := context.Background()
	if _, err := m.Up(ctx, lamigrate.All()); err != nil {
		t.Fatalf("initial Up: %v", err)
	}

	// 2. Simulate an orphan: an applied metadata row whose source file no
	// longer exists. Insert a second applied row referencing a file we
	// never create on disk (migration state set directly via SQL).
	runnerID := fmt.Sprintf("ignore-test-%d", time.Now().UnixNano())
	now := time.Now().UTC().Format("2006-01-02 15:04:05.000000")
	upSum := make([]byte, 32)
	for i := range upSum {
		upSum[i] = byte(i + 0x20)
	}
	_, err := tb.DB().Exec(fmt.Sprintf(
		"INSERT INTO `%s` (migration, source_kind, source_version, source_name, up_checksum, down_checksum, batch, state, is_baseline, runner_id, started_at, applied_at, updated_at) "+
			"VALUES (?, 'timestamp', NULL, ?, ?, ?, 99, 'applied', FALSE, ?, ?, ?, ?)",
		tableName,
	), "20260731150000_orphan_from_other_branch", "20260731150000_orphan_from_other_branch", upSum, upSum, runnerID, now, now, now)
	if err != nil {
		t.Fatalf("insert orphan row: %v", err)
	}

	// 3. User did NOT set the flag -> up must block on the orphan.
	mStrict := newTestMigratorWithDir(t, tb, tableName, dir)
	if _, err := mStrict.Up(ctx, lamigrate.All()); err == nil {
		t.Fatal("expected up to fail with strict drift check on orphan")
	} else if !errors.Is(err, lamigrate.ErrChecksumDrift) {
		t.Fatalf("expected ErrChecksumDrift, got: %v", err)
	}

	// 4. With IgnoreMissingSource -> up proceeds (nothing new to run) and
	//    the orphan row is preserved.
	mIgnoring := newTestMigratorWithOpts(t, tb, tableName, dir, lamigrate.Options{IgnoreMissingSource: true})
	if _, err := mIgnoring.Up(ctx, lamigrate.All()); err != nil {
		t.Fatalf("Up with IgnoreMissingSource should succeed, got: %v", err)
	}

	// 5. Orphan row still present.
	var count int
	err = tb.DB().QueryRow(fmt.Sprintf("SELECT COUNT(*) FROM `%s` WHERE migration = ?", tableName), "20260731150000_orphan_from_other_branch").Scan(&count)
	if err != nil {
		t.Fatalf("count orphan: %v", err)
	}
	if count != 1 {
		t.Fatalf("orphan row count = %d, want 1 (must not be deleted)", count)
	}
}
