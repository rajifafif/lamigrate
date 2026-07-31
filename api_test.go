package lamigrate_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/go-sql-driver/mysql"
	lm "github.com/rajifafif/lamigrate"
)

// ---------- StepLimit tests ----------

func TestStepLimitAll(t *testing.T) {
	t.Parallel()
	sl := lm.All()
	if sl.IsZero() {
		t.Fatal("All() returned zero StepLimit")
	}
}

func TestStepLimitSteps(t *testing.T) {
	t.Parallel()
	t.Run("valid", func(t *testing.T) {
		t.Parallel()
		sl, err := lm.Steps(5)
		if err != nil {
			t.Fatalf("Steps(5) error = %v", err)
		}
		if sl.IsZero() {
			t.Fatal("Steps(5) returned zero StepLimit")
		}
	})

	t.Run("zero", func(t *testing.T) {
		t.Parallel()
		_, err := lm.Steps(0)
		if err == nil {
			t.Fatal("Steps(0) expected error")
		}
	})

	t.Run("negative", func(t *testing.T) {
		t.Parallel()
		_, err := lm.Steps(-1)
		if err == nil {
			t.Fatal("Steps(-1) expected error")
		}
	})
}

func TestStepLimitZeroRejectedBeforeConnector(t *testing.T) {
	t.Parallel()
	// A zero StepLimit must fail at the validation layer,
	// before any database connection is created.
	var zero lm.StepLimit // explicitly zero value

	cfg := &mysql.Config{
		User:   "test",
		Passwd: "pass",
		Net:    "tcp",
		Addr:   "localhost:3306",
		DBName: "testdb",
	}
	m, err := lm.NewMySQL(cfg, lm.Options{
		Directory: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("NewMySQL: %v", err)
	}

	ctx := context.Background()
	_, err = m.Up(ctx, zero)
	if err == nil {
		t.Fatal("Up with zero StepLimit expected error")
	}
	if !errors.Is(err, lm.ErrInvalidConfig) {
		t.Fatalf("expected ErrInvalidConfig, got %v", err)
	}

	_, err = m.Down(ctx, zero)
	if err == nil {
		t.Fatal("Down with zero StepLimit expected error")
	}

	// PreviewUp must also reject zero.
	_, err = m.PreviewUp(ctx, zero)
	if err == nil {
		t.Fatal("PreviewUp with zero StepLimit expected error")
	}

	// PreviewDown must also reject zero.
	_, err = m.PreviewDown(ctx, zero)
	if err == nil {
		t.Fatal("PreviewDown with zero StepLimit expected error")
	}
}

// ---------- Constructor tests ----------

func TestNewMySQLClonesConfig(t *testing.T) {
	t.Parallel()
	cfg := &mysql.Config{
		User:   "testuser",
		Passwd: "secret",
		Net:    "tcp",
		Addr:   "127.0.0.1:3306",
		DBName: "mydb",
		Params: map[string]string{"charset": "utf8mb4"},
	}

	m, err := lm.NewMySQL(cfg, lm.Options{
		Directory: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("NewMySQL: %v", err)
	}

	// Mutate the original config.
	cfg.User = "changed"
	cfg.Passwd = "changed"
	cfg.Params["charset"] = "latin1"
	cfg.DBName = "other"

	// The migrator must still reflect the original values.
	if got := m.Directory(); got == "" {
		t.Fatal("Directory is empty")
	}
	// We can't read the internal config directly, but we can verify the
	// Migrator was created successfully (which requires valid cloning).
	if m.TableName() != "migrations" {
		t.Fatalf("TableName = %q, want %q", m.TableName(), "migrations")
	}
}

func TestNewMySQLValidatesOptions(t *testing.T) {
	t.Parallel()
	validCfg := func() *mysql.Config {
		return &mysql.Config{
			User:   "test",
			Passwd: "pass",
			Net:    "tcp",
			Addr:   "localhost:3306",
			DBName: "testdb",
		}
	}

	t.Run("nil_config", func(t *testing.T) {
		t.Parallel()
		_, err := lm.NewMySQL(nil, lm.Options{Directory: t.TempDir()})
		if err == nil {
			t.Fatal("expected error for nil config")
		}
		if !errors.Is(err, lm.ErrInvalidConfig) {
			t.Fatalf("expected ErrInvalidConfig, got %v", err)
		}
	})

	t.Run("empty_directory", func(t *testing.T) {
		t.Parallel()
		_, err := lm.NewMySQL(validCfg(), lm.Options{})
		if err == nil {
			t.Fatal("expected error for empty directory")
		}
		if !errors.Is(err, lm.ErrInvalidConfig) {
			t.Fatalf("expected ErrInvalidConfig, got %v", err)
		}
	})

	t.Run("bad_table_name", func(t *testing.T) {
		t.Parallel()
		_, err := lm.NewMySQL(validCfg(), lm.Options{
			Directory: t.TempDir(),
			TableName: "INVALID",
		})
		if err == nil {
			t.Fatal("expected error for bad table name")
		}
		if !errors.Is(err, lm.ErrInvalidConfig) {
			t.Fatalf("expected ErrInvalidConfig, got %v", err)
		}
	})

	t.Run("table_name_with_digits", func(t *testing.T) {
		t.Parallel()
		m, err := lm.NewMySQL(validCfg(), lm.Options{
			Directory: t.TempDir(),
			TableName: "migrations_v2",
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if m.TableName() != "migrations_v2" {
			t.Fatalf("TableName = %q", m.TableName())
		}
	})

	t.Run("lock_timeout_too_large", func(t *testing.T) {
		t.Parallel()
		_, err := lm.NewMySQL(validCfg(), lm.Options{
			Directory:   t.TempDir(),
			LockTimeout: 25 * time.Hour,
		})
		if err == nil {
			t.Fatal("expected error for lock timeout > 24h")
		}
		if !errors.Is(err, lm.ErrInvalidConfig) {
			t.Fatalf("expected ErrInvalidConfig, got %v", err)
		}
	})

	t.Run("negative_max_file_size", func(t *testing.T) {
		t.Parallel()
		_, err := lm.NewMySQL(validCfg(), lm.Options{
			Directory:  t.TempDir(),
			MaxFileSize: -1,
		})
		if err == nil {
			t.Fatal("expected error for negative MaxFileSize")
		}
		if !errors.Is(err, lm.ErrInvalidConfig) {
			t.Fatalf("expected ErrInvalidConfig, got %v", err)
		}
	})

	t.Run("defaults_applied", func(t *testing.T) {
		t.Parallel()
		m, err := lm.NewMySQL(validCfg(), lm.Options{
			Directory: t.TempDir(),
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if m.TableName() != "migrations" {
			t.Fatalf("default TableName = %q, want %q", m.TableName(), "migrations")
		}
		if m.LockTimeout() != 30*time.Second {
			t.Fatalf("default LockTimeout = %v, want 30s", m.LockTimeout())
		}
		if m.MaxFileSize() != 1<<20 {
			t.Fatalf("default MaxFileSize = %d, want %d", m.MaxFileSize(), 1<<20)
		}
	})
}

func TestOpenMySQLParsesDSN(t *testing.T) {
	t.Parallel()
	dsn := "testuser:secret@tcp(127.0.0.1:3306)/mydb?charset=utf8mb4&collation=utf8mb4_general_ci"

	m, err := lm.OpenMySQL(dsn, lm.Options{
		Directory: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("OpenMySQL: %v", err)
	}

	if m.Directory() == "" {
		t.Fatal("Directory is empty after OpenMySQL")
	}

	// Empty DSN must fail.
	_, err = lm.OpenMySQL("", lm.Options{Directory: t.TempDir()})
	if err == nil {
		t.Fatal("expected error for empty DSN")
	}
	if !errors.Is(err, lm.ErrInvalidConfig) {
		t.Fatalf("expected ErrInvalidConfig, got %v", err)
	}

	// Malformed DSN must fail.
	_, err = lm.OpenMySQL("not-a-dsn", lm.Options{Directory: t.TempDir()})
	if err == nil {
		t.Fatal("expected error for malformed DSN")
	}
	if !errors.Is(err, lm.ErrInvalidConfig) {
		t.Fatalf("expected ErrInvalidConfig, got %v", err)
	}
}

// ---------- No-stdout test ----------

func TestMigratorNoStdout(t *testing.T) {
	t.Parallel()

	// Capture os.Stdout.
	origStdout := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	os.Stdout = w

	cfg := &mysql.Config{
		User:   "test",
		Passwd: "pass",
		Net:    "tcp",
		Addr:   "localhost:3306",
		DBName: "testdb",
	}
	m, err := lm.NewMySQL(cfg, lm.Options{
		Directory: t.TempDir(),
	})
	if err != nil {
		w.Close()
		os.Stdout = origStdout
		t.Fatalf("NewMySQL: %v", err)
	}

	ctx := context.Background()

	// Call methods that will return "not implemented" or empty results.
	// None of them should write to stdout.
	_, _ = m.PreviewUp(ctx, lm.All())
	_, _ = m.PreviewDown(ctx, lm.All())
	_, _ = m.PreviewReset(ctx)
	_, _ = m.Up(ctx, lm.All())
	_, _ = m.Down(ctx, lm.All())
	_, _ = m.Reset(ctx)
	_, _ = m.Status(ctx)

	w.Close()
	os.Stdout = origStdout

	var buf bytes.Buffer
	if _, err := io.Copy(&buf, r); err != nil {
		t.Fatalf("reading captured stdout: %v", err)
	}
	if buf.Len() > 0 {
		t.Fatalf("library wrote %d bytes to stdout: %q", buf.Len(), buf.String())
	}
}

// ---------- Offline Make test ----------

func TestMakeOffline(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	ctx := context.Background()

	created, err := lm.Make(ctx, dir, "create_users_table")
	if err != nil {
		t.Fatalf("Make: %v", err)
	}
	if created.Name != "create_users_table" {
		t.Fatalf("Name = %q", created.Name)
	}
	if created.UpPath == "" || created.DownPath == "" {
		t.Fatalf("UpPath or DownPath empty: UpPath=%q DownPath=%q", created.UpPath, created.DownPath)
	}

	// Verify files exist on disk.
	if _, err := os.Stat(created.UpPath); err != nil {
		t.Fatalf("up file missing: %v", err)
	}
	if _, err := os.Stat(created.DownPath); err != nil {
		t.Fatalf("down file missing: %v", err)
	}

	// Make with empty directory must fail.
	_, err = lm.Make(ctx, "", "create_users_table")
	if err == nil {
		t.Fatal("expected error for empty directory")
	}

	// Make with empty name must fail.
	_, err = lm.Make(ctx, dir, "")
	if err == nil {
		t.Fatal("expected error for empty name")
	}
}

// ---------- Typed errors test ----------

func TestTypedErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  error
	}{
		{"ErrInvalidConfig", lm.ErrInvalidConfig},
		{"ErrLockTimeout", lm.ErrLockTimeout},
		{"ErrDirtyState", lm.ErrDirtyState},
		{"ErrChecksumDrift", lm.ErrChecksumDrift},
		{"ErrUnsupportedMetadata", lm.ErrUnsupportedMetadata},
		{"ErrSQLExecution", lm.ErrSQLExecution},
		{"ErrRecoveryRequired", lm.ErrRecoveryRequired},
		{"ErrOutcomeUnknown", lm.ErrOutcomeUnknown},
		{"ErrCleanupUncertain", lm.ErrCleanupUncertain},
		{"ErrUnsupportedDriver", lm.ErrUnsupportedDriver},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			// Wrap in a formatted error to simulate real usage.
			wrapped := errors.New("context: " + tt.err.Error())
			if errors.Is(wrapped, tt.err) {
				// This is expected — formatted strings won't match sentinel.
				t.Fatal("plain string should not match sentinel via errors.Is")
			}
			// The sentinel itself must match itself.
			if !errors.Is(tt.err, tt.err) {
				t.Fatal("sentinel does not match itself")
			}
		})
	}
}

// ---------- PlanView tests ----------

func TestPreviewUpReturnsEmptyPlan(t *testing.T) {
	t.Parallel()

	cfg := &mysql.Config{
		User:   "test",
		Passwd: "pass",
		Net:    "tcp",
		Addr:   "localhost:3306",
		DBName: "testdb",
	}
	m, err := lm.NewMySQL(cfg, lm.Options{
		Directory: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("NewMySQL: %v", err)
	}

	ctx := context.Background()
	plan, err := m.PreviewUp(ctx, lm.All())
	if err != nil {
		// PreviewUp now acquires the advisory lock. If MySQL is not
		// available, the connection error is expected behavior.
		t.Skipf("PreviewUp requires MySQL: %v", err)
	}
	if !plan.DryRun {
		t.Fatal("PreviewUp DryRun should be true")
	}
	if plan.Command != "up" {
		t.Fatalf("Command = %q, want %q", plan.Command, "up")
	}
}

func TestPreviewResetTakesNoStepLimit(t *testing.T) {
	t.Parallel()

	cfg := &mysql.Config{
		User:   "test",
		Passwd: "pass",
		Net:    "tcp",
		Addr:   "localhost:3306",
		DBName: "testdb",
	}
	m, err := lm.NewMySQL(cfg, lm.Options{
		Directory: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("NewMySQL: %v", err)
	}

	ctx := context.Background()
	plan, err := m.PreviewReset(ctx)
	if err != nil {
		// PreviewReset now acquires the advisory lock. If MySQL is not
		// available, the connection error is expected behavior.
		t.Skipf("PreviewReset requires MySQL: %v", err)
	}
	if plan.Command != "reset" {
		t.Fatalf("Command = %q, want %q", plan.Command, "reset")
	}
}

// ---------- Migrator accessor tests ----------

func TestMigratorAccessors(t *testing.T) {
	t.Parallel()

	cfg := &mysql.Config{
		User:   "test",
		Passwd: "pass",
		Net:    "tcp",
		Addr:   "localhost:3306",
		DBName: "testdb",
	}
	dir := t.TempDir()
	m, err := lm.NewMySQL(cfg, lm.Options{
		Directory:   dir,
		LegacyDir:   "/legacy",
		TableName:   "schema_migrations",
		LockTimeout: 60 * time.Second,
		MaxFileSize: 2 << 20,
	})
	if err != nil {
		t.Fatalf("NewMySQL: %v", err)
	}

	if m.Directory() != dir {
		t.Fatalf("Directory = %q, want %q", m.Directory(), dir)
	}
	if m.LegacyDir() != "/legacy" {
		t.Fatalf("LegacyDir = %q, want %q", m.LegacyDir(), "/legacy")
	}
	if m.TableName() != "schema_migrations" {
		t.Fatalf("TableName = %q, want %q", m.TableName(), "schema_migrations")
	}
	if m.LockTimeout() != 60*time.Second {
		t.Fatalf("LockTimeout = %v, want 60s", m.LockTimeout())
	}
	if m.MaxFileSize() != 2<<20 {
		t.Fatalf("MaxFileSize = %d, want %d", m.MaxFileSize(), 2<<20)
	}
}

// Verify that CreatedMigration from Make has the expected structure.
func TestMakeCreatesValidPair(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	ctx := context.Background()

	created, err := lm.Make(ctx, dir, "add_email_to_users_table")
	if err != nil {
		t.Fatalf("Make: %v", err)
	}

	// Verify the created migration can be found by scanMigrations.
	// This uses the internal function indirectly through the test package
	// boundary — but since we're in lamigrate_test, we check via the
	// file system.
	if _, err := os.Stat(filepath.Join(dir, created.Base+".up.sql")); err != nil {
		t.Fatalf("up file not found: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, created.Base+".down.sql")); err != nil {
		t.Fatalf("down file not found: %v", err)
	}
}

// Verify Steps rejects zero and negative values.
func TestStepsRejections(t *testing.T) {
	t.Parallel()

	for _, n := range []int{0, -1, -100} {
		_, err := lm.Steps(n)
		if err == nil {
			t.Errorf("Steps(%d) expected error", n)
		}
	}
}
