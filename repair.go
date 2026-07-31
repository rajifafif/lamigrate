package lamigrate

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// repair.go — Explicit dirty-state repair workflow (architecture §12).
//
// Repair is an explicit, conservative operator-driven workflow. MySQL
// cannot prove that an interrupted DDL operation either fully happened
// or did not happen. Repair never executes migration SQL automatically.
// It implements only legal conditional transitions and requires
// explicit confirmation and a free-text operator reason for every
// mutation.

// RepairRequest describes a repair operation to be performed on a
// single named migration.
type RepairRequest struct {
	// Operation is the repair action: "show", "mark-applied",
	// "mark-rolled-back", or "remove-failed".
	Operation string

	// Migration is the canonical migration ID to repair.
	Migration string

	// Yes indicates explicit operator confirmation.
	Yes bool

	// Reason is a free-text explanation of why this repair is needed.
	// Required for every mutation (show may omit it).
	Reason string
}

// RepairPlanView is a read-only informational snapshot of a repair
// operation. It contains the current metadata state, expected checksums,
// and the planned transition. No mutation is performed by preview.
type RepairPlanView struct {
	// Operation is the requested repair action.
	Operation string

	// Migration is the canonical migration ID being repaired.
	Migration string

	// CurrentState is the migration's current metadata state.
	CurrentState string

	// UpChecksum is the hex-encoded stored up checksum.
	UpChecksum string

	// DownChecksum is the hex-encoded stored down checksum, or empty
	// for irreversible migrations.
	DownChecksum string

	// Batch is the batch number from the metadata row.
	Batch int

	// SourceName is the source file name from the metadata row.
	SourceName string

	// IsIrreversible reports whether this migration is irreversible
	// (NULL down_checksum).
	IsIrreversible bool

	// ProposedTransition describes the state transition that would
	// be performed. Empty for "show" operations.
	ProposedTransition string

	// ConfirmationRequired reports whether --yes must be provided.
	ConfirmationRequired bool

	// OperatorInstructions documents what the operator must inspect
	// in the database before confirming this repair.
	OperatorInstructions []string
}

// PreviewRepair returns a read-only view of what a repair operation
// would do. It acquires the advisory lock for read consistency and
// inspects the current metadata state, but performs no mutations.
func (m *Migrator) PreviewRepair(ctx context.Context, request RepairRequest) (RepairPlanView, error) {
	if err := validateRepairRequest(request); err != nil {
		return RepairPlanView{}, err
	}

	// Bootstrap metadata if needed.
	if err := m.bootstrap(ctx); err != nil {
		return RepairPlanView{}, err
	}

	var view RepairPlanView
	err := m.withLockSession(ctx, func(ctx context.Context, conn *sql.Conn, caps *SessionCapabilities) error {
		v, err := buildRepairView(ctx, conn, m.tableName, request)
		if err != nil {
			return err
		}
		view = *v
		return nil
	})
	if err != nil {
		return RepairPlanView{}, err
	}
	return view, nil
}

// Repair executes a repair operation on a single named migration.
// It acquires the advisory lock, validates the current state, requires
// explicit confirmation and a reason, and performs the requested
// metadata transition without executing any migration SQL.
func (m *Migrator) Repair(ctx context.Context, request RepairRequest) (Result, error) {
	if err := validateRepairRequest(request); err != nil {
		return Result{}, err
	}

	// Show is a read-only operation that doesn't need confirmation.
	if request.Operation == "show" {
		var result Result
		err := m.withLockSession(ctx, func(ctx context.Context, conn *sql.Conn, caps *SessionCapabilities) error {
			// Bootstrap metadata if needed.
			if bErr := m.bootstrap(ctx); bErr != nil {
				return bErr
			}
			view, err := buildRepairView(ctx, conn, m.tableName, request)
			if err != nil {
				return err
			}
			result = Result{
				Command: "repair",
				Migrated: []MigrationResult{{
					Name:      view.Migration,
					Direction: "show",
				}},
			}
			return nil
		})
		if err != nil {
			return Result{}, err
		}
		return result, nil
	}

	// Mutation operations require confirmation.
	if !request.Yes {
		return Result{}, fmt.Errorf(
			"%w: repair %s requires --yes confirmation",
			ErrConfirmationRequired, request.Operation,
		)
	}

	// Reason is required for every mutation.
	if request.Reason == "" {
		return Result{}, fmt.Errorf(
			"%w: repair %s requires a reason (--reason)",
			ErrConfirmationRequired, request.Operation,
		)
	}

	// Bootstrap metadata before acquiring the lock.
	if err := m.bootstrap(ctx); err != nil {
		return Result{}, err
	}

	var result Result
	err := m.withLockSession(ctx, func(ctx context.Context, conn *sql.Conn, caps *SessionCapabilities) error {
		// Build the view (validates state).
		view, err := buildRepairView(ctx, conn, m.tableName, request)
		if err != nil {
			return err
		}

		// Execute the repair transition.
		startedAt := time.Now().UTC()
		runnerID := generateRunnerID()

		switch request.Operation {
		case "mark-applied":
			if err := m.markAppliedByRepair(ctx, conn, m.tableName, request.Migration); err != nil {
				return err
			}
		case "mark-rolled-back":
			if err := m.markRolledBackByRepair(ctx, conn, m.tableName, request.Migration); err != nil {
				return err
			}
		case "remove-failed":
			if err := m.removeFailedByRepair(ctx, conn, m.tableName, request.Migration); err != nil {
				return err
			}
		default:
			return fmt.Errorf("%w: unknown repair operation %q", ErrInvalidConfig, request.Operation)
		}

		duration := time.Since(startedAt)

		result = Result{
			Command: "repair",
			Migrated: []MigrationResult{{
				Name:      view.Migration,
				Direction: request.Operation,
				Batch:     view.Batch,
				Applied:   true,
				Duration:  duration,
			}},
		}

		_ = runnerID // available for audit logging in the future

		return nil
	})
	if err != nil {
		return Result{}, err
	}
	return result, nil
}

// buildRepairView constructs a RepairPlanView by inspecting the current
// metadata state. It does not perform any mutations.
func buildRepairView(
	ctx context.Context,
	conn *sql.Conn,
	tableName string,
	request RepairRequest,
) (*RepairPlanView, error) {
	// Read the migration row.
	row := conn.QueryRowContext(ctx,
		fmt.Sprintf(
			"SELECT migration, state, up_checksum, down_checksum, batch, source_name "+
				"FROM `%s` WHERE migration = ?",
			tableName,
		),
		request.Migration,
	)

	var migration, state, sourceName string
	var upChecksum, downChecksum []byte
	var batch int

	err := row.Scan(&migration, &state, &upChecksum, &downChecksum, &batch, &sourceName)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf(
			"%w: migration %q not found in metadata table",
			ErrRepairRejected, request.Migration,
		)
	}
	if err != nil {
		return nil, fmt.Errorf("buildRepairView: read row: %w", err)
	}

	view := &RepairPlanView{
		Operation:  request.Operation,
		Migration:  migration,
		Batch:      batch,
		SourceName: sourceName,
	}

	// Set checksum displays.
	if len(upChecksum) == 32 {
		view.UpChecksum = fmt.Sprintf("%x", upChecksum)
	}
	if len(downChecksum) == 32 {
		view.DownChecksum = fmt.Sprintf("%x", downChecksum)
	}
	view.IsIrreversible = downChecksum == nil

	// Determine current state and proposed transition based on operation.
	switch request.Operation {
	case "show":
		view.CurrentState = state
		view.ConfirmationRequired = false
		view.OperatorInstructions = repairInstructions(state)

	case "mark-applied":
		view.CurrentState = state
		if state == "applying" || state == "apply_failed" {
			view.ProposedTransition = fmt.Sprintf("%s -> applied", state)
		} else {
			return nil, fmt.Errorf(
				"%w: mark-applied requires state 'applying' or 'apply_failed', got %q",
				ErrRepairRejected, state,
			)
		}
		view.ConfirmationRequired = true
		view.OperatorInstructions = markAppliedInstructions(state)

	case "mark-rolled-back":
		view.CurrentState = state
		if state == "rolling_back" || state == "rollback_failed" {
			view.ProposedTransition = fmt.Sprintf("%s -> applied -> row absent", state)
		} else if state == "applied" && view.IsIrreversible {
			view.ProposedTransition = "applied (irreversible) -> row absent"
		} else {
			return nil, fmt.Errorf(
				"%w: mark-rolled-back requires dirty rollback state or clean irreversible applied, got %q",
				ErrRepairRejected, state,
			)
		}
		view.ConfirmationRequired = true
		view.OperatorInstructions = markRolledBackInstructions(state, view.IsIrreversible)

	case "remove-failed":
		view.CurrentState = state
		if !isDirtyState(state) {
			return nil, fmt.Errorf(
				"%w: remove-failed requires a dirty state, got %q",
				ErrRepairRejected, state,
			)
		}
		view.ProposedTransition = fmt.Sprintf("%s -> row absent", state)
		view.ConfirmationRequired = true
		view.OperatorInstructions = removeFailedInstructions(state)

	default:
		return nil, fmt.Errorf("%w: unknown repair operation %q", ErrInvalidConfig, request.Operation)
	}

	return view, nil
}

// validateRepairRequest checks the repair request for basic validity.
func validateRepairRequest(request RepairRequest) error {
	switch request.Operation {
	case "show", "mark-applied", "mark-rolled-back", "remove-failed":
		// valid
	default:
		return fmt.Errorf(
			"%w: invalid repair operation %q (valid: show, mark-applied, mark-rolled-back, remove-failed)",
			ErrInvalidConfig, request.Operation,
		)
	}

	if request.Migration == "" {
		return fmt.Errorf("%w: repair requires a migration name", ErrInvalidConfig)
	}

	return nil
}

// repairInstructions returns the operator instructions for a "show"
// operation based on the current state.
func repairInstructions(state string) []string {
	switch state {
	case "applying":
		return []string{
			"The migration is in 'applying' state — the SQL may or may not have been executed.",
			"Inspect the database schema to determine whether the DDL statements took effect.",
			"If the SQL fully succeeded, use 'mark-applied' to complete the record.",
			"If the SQL had no effect or partial effect, use 'remove-failed' to discard the intent.",
		}
	case "apply_failed":
		return []string{
			"The migration is in 'apply_failed' state — the SQL was attempted but failed.",
			"Inspect the database schema to verify no partial changes remain.",
			"If all changes were rolled back or never applied, use 'remove-failed'.",
			"If the SQL actually succeeded despite the error, use 'mark-applied'.",
		}
	case "rolling_back":
		return []string{
			"The migration is in 'rolling_back' state — the rollback SQL may or may not have executed.",
			"Inspect the database schema to determine whether the rollback completed.",
			"If the rollback succeeded, use 'mark-rolled-back' to remove the record.",
			"If the rollback had no effect, you may need to manually roll back, then use 'mark-rolled-back'.",
		}
	case "rollback_failed":
		return []string{
			"The migration is in 'rollback_failed' state — the rollback SQL was attempted but failed.",
			"Inspect the database schema to determine what changes the rollback did or did not make.",
			"After manual compensation, use 'mark-rolled-back' to remove the record.",
		}
	default:
		return []string{
			fmt.Sprintf("Current state: %s", state),
		}
	}
}

// markAppliedInstructions returns operator instructions for the
// mark-applied repair operation.
func markAppliedInstructions(state string) []string {
	switch state {
	case "applying":
		return []string{
			"BEFORE confirming, inspect the database to verify the migration SQL fully succeeded.",
			"Check for any partial DDL changes, open transactions, or session state issues.",
			"Confirm that all statements in the migration file were applied.",
			"WARNING: If only partial SQL succeeded, marking as applied will hide the inconsistency.",
		}
	case "apply_failed":
		return []string{
			"BEFORE confirming, inspect the database to verify the SQL actually succeeded.",
			"Compare the expected schema changes against the actual database state.",
			"The SQL may have partially committed (MySQL DDL auto-commits).",
			"WARNING: If the SQL truly failed, marking as applied will hide the inconsistency.",
		}
	default:
		return []string{"Inspect the database before confirming."}
	}
}

// markRolledBackInstructions returns operator instructions for the
// mark-rolled-back repair operation.
func markRolledBackInstructions(state string, isIrreversible bool) []string {
	if isIrreversible && state == "applied" {
		return []string{
			"This is an irreversible migration (no down file).",
			"BEFORE confirming, you must have manually compensated the migration effects.",
			"Verify the database state reflects the manual compensation.",
			"After confirmation, the migration record will be removed from metadata.",
		}
	}

	switch state {
	case "rolling_back":
		return []string{
			"BEFORE confirming, inspect the database to verify the rollback SQL fully succeeded.",
			"Check that the original migration's effects have been undone.",
			"After confirmation, the migration record will be removed from metadata.",
		}
	case "rollback_failed":
		return []string{
			"BEFORE confirming, inspect the database to determine the actual rollback state.",
			"You may need to manually complete the rollback before confirming.",
			"After confirmation, the migration record will be removed from metadata.",
		}
	default:
		return []string{"Inspect the database before confirming."}
	}
}

// removeFailedInstructions returns operator instructions for the
// remove-failed repair operation.
func removeFailedInstructions(state string) []string {
	switch state {
	case "applying":
		return []string{
			"BEFORE confirming, inspect the database to verify the SQL had no effect.",
			"If any DDL did take effect, it will remain in the database after removal.",
			"After confirmation, the migration record will be deleted from metadata.",
		}
	case "apply_failed":
		return []string{
			"BEFORE confirming, inspect the database to verify no partial changes remain.",
			"MySQL DDL may have auto-committed partial changes before the failure.",
			"After confirmation, the migration record will be deleted from metadata.",
		}
	case "rolling_back":
		return []string{
			"BEFORE confirming, inspect the database to verify the rollback had no effect.",
			"After confirmation, the migration record will be deleted from metadata.",
		}
	case "rollback_failed":
		return []string{
			"BEFORE confirming, inspect the database to verify no partial rollback changes remain.",
			"After confirmation, the migration record will be deleted from metadata.",
		}
	default:
		return []string{"Inspect the database before confirming."}
	}
}
