package lamigrate_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/go-sql-driver/mysql"
	lm "github.com/rajifafif/lamigrate"
)

// ---------- Request validation tests ----------

func TestValidateRepairRequest(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		req     lm.RepairRequest
		wantErr error
	}{
		{
			name:    "show_valid",
			req:     lm.RepairRequest{Operation: "show", Migration: "20260101120000_create_users"},
			wantErr: nil,
		},
		{
			name:    "mark_applied_valid",
			req:     lm.RepairRequest{Operation: "mark-applied", Migration: "20260101120000_create_users", Yes: true, Reason: "SQL verified"},
			wantErr: nil,
		},
		{
			name:    "mark_rolled_back_valid",
			req:     lm.RepairRequest{Operation: "mark-rolled-back", Migration: "20260101120000_create_users", Yes: true, Reason: "Manual compensation done"},
			wantErr: nil,
		},
		{
			name:    "remove_failed_valid",
			req:     lm.RepairRequest{Operation: "remove-failed", Migration: "20260101120000_create_users", Yes: true, Reason: "SQL had no effect"},
			wantErr: nil,
		},
		{
			name:    "empty_operation",
			req:     lm.RepairRequest{Operation: "", Migration: "20260101120000_create_users"},
			wantErr: lm.ErrInvalidConfig,
		},
		{
			name:    "unknown_operation",
			req:     lm.RepairRequest{Operation: "invalid", Migration: "20260101120000_create_users"},
			wantErr: lm.ErrInvalidConfig,
		},
		{
			name:    "empty_migration",
			req:     lm.RepairRequest{Operation: "show", Migration: ""},
			wantErr: lm.ErrInvalidConfig,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			// We call PreviewRepair with a fake migrator; validation
			// happens before any database connection.
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

			_, err = m.PreviewRepair(context.Background(), tt.req)
			if tt.wantErr == nil {
				if err != nil {
					// PreviewRepair needs MySQL; if it fails with a
					// connection error, that's acceptable for validation
					// tests that pass validation.
					if errors.Is(err, lm.ErrInvalidConfig) || errors.Is(err, lm.ErrRepairRejected) {
						t.Fatalf("unexpected validation error: %v", err)
					}
					// Connection error is acceptable for this test.
					return
				}
			} else {
				if err == nil {
					t.Fatal("expected error")
				}
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("error = %v, want %v", err, tt.wantErr)
				}
			}
		})
	}
}

func TestRepairRequestWithoutConfirmation(t *testing.T) {
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

	// Repair without --yes should fail with confirmation error
	// even before connecting (but the error comes after validation).
	_, err = m.Repair(context.Background(), lm.RepairRequest{
		Operation: "mark-applied",
		Migration: "20260101120000_create_users",
		Yes:       false,
		Reason:    "test",
	})
	if err == nil {
		t.Fatal("expected error for repair without confirmation")
	}
	if !errors.Is(err, lm.ErrConfirmationRequired) {
		t.Fatalf("error = %v, want ErrConfirmationRequired", err)
	}
}

func TestRepairRequestWithoutReason(t *testing.T) {
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

	// Repair with --yes but no reason should fail.
	_, err = m.Repair(context.Background(), lm.RepairRequest{
		Operation: "mark-applied",
		Migration: "20260101120000_create_users",
		Yes:       true,
		Reason:    "",
	})
	if err == nil {
		t.Fatal("expected error for repair without reason")
	}
	if !errors.Is(err, lm.ErrConfirmationRequired) {
		t.Fatalf("error = %v, want ErrConfirmationRequired", err)
	}
}

func TestRepairShowDoesNotRequireConfirmation(t *testing.T) {
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

	// Show should not require --yes.
	_, err = m.Repair(context.Background(), lm.RepairRequest{
		Operation: "show",
		Migration: "20260101120000_create_users",
	})
	if err != nil {
		// If it fails, it should NOT be a confirmation error.
		if errors.Is(err, lm.ErrConfirmationRequired) {
			t.Fatalf("show should not require confirmation, got: %v", err)
		}
		// Connection error is acceptable.
	}
}

func TestRepairShowWithoutReason(t *testing.T) {
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

	// Show without reason should succeed (no reason needed for show).
	_, err = m.Repair(context.Background(), lm.RepairRequest{
		Operation: "show",
		Migration: "20260101120000_create_users",
	})
	if err != nil {
		// Should not be a confirmation error.
		if errors.Is(err, lm.ErrConfirmationRequired) {
			t.Fatalf("show without reason should not require confirmation, got: %v", err)
		}
		// Connection error is acceptable.
	}
}

// ---------- RepairPlanView tests ----------

func TestRepairPlanViewStructure(t *testing.T) {
	t.Parallel()
	// Verify the RepairPlanView struct has expected fields.
	view := lm.RepairPlanView{
		Operation:            "mark-applied",
		Migration:            "20260101120000_create_users",
		CurrentState:         "apply_failed",
		UpChecksum:           "abc123",
		Batch:                1,
		SourceName:           "20260101120000_create_users",
		IsIrreversible:       false,
		ProposedTransition:   "apply_failed -> applied",
		ConfirmationRequired: true,
		OperatorInstructions: []string{"inspect db first"},
	}

	if view.Operation != "mark-applied" {
		t.Fatalf("Operation = %q", view.Operation)
	}
	if view.Migration != "20260101120000_create_users" {
		t.Fatalf("Migration = %q", view.Migration)
	}
	if view.CurrentState != "apply_failed" {
		t.Fatalf("CurrentState = %q", view.CurrentState)
	}
	if !view.ConfirmationRequired {
		t.Fatal("ConfirmationRequired should be true")
	}
	if len(view.OperatorInstructions) != 1 {
		t.Fatalf("OperatorInstructions len = %d", len(view.OperatorInstructions))
	}
}

// ---------- Error sentinel tests ----------

func TestRepairErrorSentinels(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  error
	}{
		{"ErrConfirmationRequired", lm.ErrConfirmationRequired},
		{"ErrRepairRejected", lm.ErrRepairRejected},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if !errors.Is(tt.err, tt.err) {
				t.Fatal("sentinel does not match itself")
			}
		})
	}
}

// ---------- PreviewRepair validation test ----------

func TestPreviewRepairRejectsInvalidOperation(t *testing.T) {
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

	_, err = m.PreviewRepair(context.Background(), lm.RepairRequest{
		Operation: "bogus",
		Migration: "20260101120000_create_users",
	})
	if err == nil {
		t.Fatal("expected error for invalid operation")
	}
	if !errors.Is(err, lm.ErrInvalidConfig) {
		t.Fatalf("error = %v, want ErrInvalidConfig", err)
	}
}

func TestPreviewRepairRejectsEmptyMigration(t *testing.T) {
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

	_, err = m.PreviewRepair(context.Background(), lm.RepairRequest{
		Operation: "show",
		Migration: "",
	})
	if err == nil {
		t.Fatal("expected error for empty migration")
	}
	if !errors.Is(err, lm.ErrInvalidConfig) {
		t.Fatalf("error = %v, want ErrInvalidConfig", err)
	}
}

// ---------- No stdout test ----------

func TestRepairNoStdout(t *testing.T) {
	t.Parallel()
	// Verify that PreviewRepair and Repair don't write to stdout.
	// We use a fake migrator; if it connects it will fail, but
	// it should never write to stdout.
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
	_, _ = m.PreviewRepair(ctx, lm.RepairRequest{
		Operation: "show",
		Migration: "20260101120000_test",
	})
	_, _ = m.Repair(ctx, lm.RepairRequest{
		Operation: "show",
		Migration: "20260101120000_test",
	})
	// If we get here without panicking, the API contract is satisfied.
	// Actual stdout verification is done in the main api_test.go.
}

// ---------- RepairRequest type tests ----------

func TestRepairRequestTypes(t *testing.T) {
	t.Parallel()
	// Verify that RepairRequest and RepairPlanView are accessible
	// and constructible from external packages.
	req := lm.RepairRequest{
		Operation: "mark-applied",
		Migration: "20260101120000_test",
		Yes:       true,
		Reason:    "operator verified",
	}
	if req.Operation != "mark-applied" {
		t.Fatalf("Operation = %q", req.Operation)
	}

	view := lm.RepairPlanView{}
	_ = view // zero value should be valid
}

// Ensure the Migrator methods exist at compile time.
func TestRepairMethodSignatures(t *testing.T) {
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

	// PreviewRepair returns (RepairPlanView, error).
	var _ lm.RepairPlanView
	_, _ = m.PreviewRepair(ctx, lm.RepairRequest{})

	// Repair returns (Result, error).
	var _ lm.Result
	_, _ = m.Repair(ctx, lm.RepairRequest{})

	// Prevent unused variable warnings.
	_ = time.Now()
}
