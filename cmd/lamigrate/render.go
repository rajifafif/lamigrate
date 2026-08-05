package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/rajifafif/lamigrate"
)

// RedactDSN replaces the password portion of a MySQL DSN with ***.
// DSN format: user:password@tcp(host:port)/dbname?params
func RedactDSN(dsn string) string {
	atIdx := strings.Index(dsn, "@")
	if atIdx < 0 {
		return dsn
	}
	colonIdx := strings.Index(dsn[:atIdx], ":")
	if colonIdx < 0 {
		return dsn
	}
	return dsn[:colonIdx+1] + "***" + dsn[atIdx:]
}

// renderResult renders a migration execution result to w.
func renderResult(w io.Writer, result lamigrate.Result, cmdName string, jsonOut bool) {
	if jsonOut {
		data := map[string]interface{}{
			"command":  result.Command,
			"migrated": result.Migrated,
			"errors":   result.Errors,
			"count":    len(result.Migrated),
		}
		writeJSON(w, cmdName, data, nil)
		return
	}

	for _, m := range result.Migrated {
		direction := "Applied"
		if m.Direction == "down" {
			direction = "Rolled back"
		}
		fmt.Fprintf(w, "%s %s (batch %d)\n", direction, m.Name, m.Batch)
	}
	for _, e := range result.Errors {
		fmt.Fprintf(w, "Error %s: %v\n", e.Name, e.Error)
	}
	if len(result.Migrated) > 0 {
		fmt.Fprintf(w, "%d migration(s) %s.\n", len(result.Migrated), verbPast(result.Command))
	} else if len(result.Errors) == 0 {
		fmt.Fprintf(w, "Nothing to %s.\n", verbPresent(result.Command))
	}
}

// renderPlanView renders a preview/dry-run plan to w.
func renderPlanView(w io.Writer, plan lamigrate.PlanView, cmdName string, jsonOut bool) {
	if jsonOut {
		data := map[string]interface{}{
			"command":    plan.Command,
			"directory":  plan.Directory,
			"table_name": plan.TableName,
			"migrations": plan.Migrations,
			"dry_run":    plan.DryRun,
			"batch":      plan.Batch,
			"count":      len(plan.Migrations),
		}
		writeJSON(w, cmdName, data, nil)
		return
	}

	if len(plan.Migrations) == 0 {
		fmt.Fprintf(w, "Nothing to %s.\n", verbPresent(cmdName))
		return
	}
	fmt.Fprintln(w, "WARNING: SQL content may be sensitive.")
	fmt.Fprintln(w)
	fmt.Fprintf(w, "Would %s:\n", verbPresent(cmdName))
	for _, name := range plan.Migrations {
		fmt.Fprintf(w, "  %s\n", name)
	}
	fmt.Fprintf(w, "Pretend: %d migration(s) would be %s.\n", len(plan.Migrations), verbPast(cmdName))
}

// renderStatus renders a status report to w.
func renderStatus(w io.Writer, report lamigrate.StatusReport, jsonOut bool) {
	if jsonOut {
		data := map[string]interface{}{
			"migrations": report.Migrations,
			"count":      len(report.Migrations),
		}
		writeJSON(w, "status", data, nil)
		return
	}

	fmt.Fprintln(w)
	fmt.Fprintf(w, "  %-55s %-10s %-8s %s\n", "Migration", "Status", "Batch", "Applied At")
	fmt.Fprintf(w, "  %-55s %-10s %-8s %s\n",
		strings.Repeat("-", 55), strings.Repeat("-", 10),
		strings.Repeat("-", 8), strings.Repeat("-", 19))

	for _, s := range report.Migrations {
		statusStr := strings.ToUpper(s.Status)
		batchStr := ""
		if s.Batch > 0 {
			batchStr = fmt.Sprintf("%d", s.Batch)
		}
		fmt.Fprintf(w, "  %-55s %-10s %-8s %s\n", s.Filename, statusStr, batchStr, s.AppliedAt)
	}
	fmt.Fprintln(w)
}

// renderRepairPlanView renders a read-only repair "show" preview to w.
func renderRepairPlanView(w io.Writer, view lamigrate.RepairPlanView, jsonOut bool) {
	if jsonOut {
		data := map[string]interface{}{
			"operation":             view.Operation,
			"migration":             view.Migration,
			"current_state":         view.CurrentState,
			"batch":                 view.Batch,
			"up_checksum":           view.UpChecksum,
			"down_checksum":         view.DownChecksum,
			"source_name":           view.SourceName,
			"is_irreversible":       view.IsIrreversible,
			"proposed_transition":   view.ProposedTransition,
			"confirmation_required": view.ConfirmationRequired,
			"operator_instructions": view.OperatorInstructions,
		}
		writeJSON(w, "repair", data, nil)
		return
	}

	fmt.Fprintf(w, "Migration: %s\n", view.Migration)
	fmt.Fprintf(w, "Operation: %s\n", view.Operation)
	fmt.Fprintf(w, "State:     %s\n", view.CurrentState)
	if view.Batch > 0 {
		fmt.Fprintf(w, "Batch:     %d\n", view.Batch)
	}
	if view.UpChecksum != "" {
		fmt.Fprintf(w, "Up sum:    %s\n", view.UpChecksum)
	}
	if view.DownChecksum != "" {
		fmt.Fprintf(w, "Down sum:  %s\n", view.DownChecksum)
	}
	if view.SourceName != "" {
		fmt.Fprintf(w, "Source:    %s\n", view.SourceName)
	}
	if view.IsIrreversible {
		fmt.Fprintln(w, "Irreversible: yes")
	}
	if view.ProposedTransition != "" {
		fmt.Fprintf(w, "Proposed:  %s\n", view.ProposedTransition)
	}
	for _, line := range view.OperatorInstructions {
		fmt.Fprintf(w, "  - %s\n", line)
	}
	if view.ConfirmationRequired {
		fmt.Fprintln(w, "Confirm:   --yes required")
	}
}

// renderVersion renders version output to w.
func renderVersion(w io.Writer, jsonOut bool) {
	if jsonOut {
		writeJSON(w, "version", map[string]interface{}{"version": version}, nil)
		return
	}
	fmt.Fprintln(w, version)
}

// renderMake renders migration creation output to w.
func renderMake(w io.Writer, created lamigrate.CreatedMigration, jsonOut bool) {
	if jsonOut {
		data := map[string]interface{}{
			"name":      created.Name,
			"up_path":   created.UpPath,
			"down_path": created.DownPath,
			"template":  created.Template,
		}
		writeJSON(w, "make", data, nil)
		return
	}
	fmt.Fprintf(w, "Created:  %s\n", created.UpPath)
	fmt.Fprintf(w, "Created:  %s\n", created.DownPath)
	fmt.Fprintf(w, "Template: %s\n", created.Template)
}

// renderRefreshResult renders a refresh execution result to w.
func renderRefreshResult(w io.Writer, result lamigrate.RefreshResult, jsonOut bool) {
	if jsonOut {
		data := map[string]interface{}{
			"command":   "refresh",
			"rollback":  result.Rollback,
			"apply":     result.Apply,
			"rb_count":  len(result.Rollback.Migrated),
			"ap_count":  len(result.Apply.Migrated),
		}
		writeJSON(w, "refresh", data, nil)
		return
	}

	// Rollback phase.
	for _, m := range result.Rollback.Migrated {
		fmt.Fprintf(w, "Rolled back %s (batch %d)\n", m.Name, m.Batch)
	}
	if len(result.Rollback.Errors) > 0 {
		for _, e := range result.Rollback.Errors {
			fmt.Fprintf(w, "Error %s: %v\n", e.Name, e.Error)
		}
	}

	// Apply phase.
	for _, m := range result.Apply.Migrated {
		fmt.Fprintf(w, "Applied %s (batch %d)\n", m.Name, m.Batch)
	}
	if len(result.Apply.Errors) > 0 {
		for _, e := range result.Apply.Errors {
			fmt.Fprintf(w, "Error %s: %v\n", e.Name, e.Error)
		}
	}

	total := len(result.Rollback.Migrated) + len(result.Apply.Migrated)
	if total > 0 {
		fmt.Fprintf(w, "%d migration(s) refreshed.\n", total)
	} else if len(result.Rollback.Errors) == 0 && len(result.Apply.Errors) == 0 {
		fmt.Fprintf(w, "Nothing to refresh.\n")
	}
}

// renderRefreshPlanView renders a refresh preview to w.
func renderRefreshPlanView(w io.Writer, plan lamigrate.RefreshPlanView, jsonOut bool) {
	if jsonOut {
		data := map[string]interface{}{
			"command":   plan.Command,
			"directory": plan.Directory,
			"table_name": plan.TableName,
			"rollback":  plan.Rollback,
			"apply":     plan.Apply,
			"dry_run":   plan.DryRun,
			"rb_count":  len(plan.Rollback),
			"ap_count":  len(plan.Apply),
		}
		writeJSON(w, "refresh", data, nil)
		return
	}

	if len(plan.Rollback) == 0 && len(plan.Apply) == 0 {
		fmt.Fprintf(w, "Nothing to refresh.\n")
		return
	}

	fmt.Fprintln(w, "WARNING: SQL content may be sensitive.")
	fmt.Fprintln(w)

	if len(plan.Rollback) > 0 {
		fmt.Fprintf(w, "Rollback (%d migrations):\n", len(plan.Rollback))
		for i, name := range plan.Rollback {
			fmt.Fprintf(w, "  %d. %s\n", i, name)
		}
	}

	if len(plan.Apply) > 0 {
		fmt.Fprintf(w, "\nRe-apply (%d migrations):\n", len(plan.Apply))
		for i, name := range plan.Apply {
			fmt.Fprintf(w, "  %d. %s\n", i+1, name)
		}
	}

	total := len(plan.Rollback) + len(plan.Apply)
	fmt.Fprintf(w, "\nPretend: %d migration(s) would be refreshed.\n", total)
}

// writeJSON writes a JSONOutput to w.
func writeJSON(w io.Writer, cmdName string, data interface{}, jsonErr *JSONError) {
	out := JSONOutput{
		Version: jsonSchemaVersion,
		Command: cmdName,
		Data:    data,
		Error:   jsonErr,
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(out)
}

// renderErrorJSON writes a JSON error output to w.
func renderErrorJSON(w io.Writer, cmdName string, err error) {
	writeJSON(w, cmdName, nil, &JSONError{
		Category: errorCategory(err),
		Message:  err.Error(),
	})
}

// errorCategory maps a lamigrate error to a category string.
func errorCategory(err error) string {
	switch {
	case errors.Is(err, lamigrate.ErrLockTimeout):
		return "lock_timeout"
	case errors.Is(err, lamigrate.ErrDirtyState):
		return "dirty_state"
	case errors.Is(err, lamigrate.ErrChecksumDrift):
		return "checksum_drift"
	case errors.Is(err, lamigrate.ErrSQLExecution):
		return "sql_execution"
	case errors.Is(err, lamigrate.ErrUnsupportedMetadata):
		return "unsupported_metadata"
	case errors.Is(err, lamigrate.ErrRecoveryRequired):
		return "recovery_required"
	case errors.Is(err, lamigrate.ErrOutcomeUnknown):
		return "outcome_unknown"
	case errors.Is(err, lamigrate.ErrConfirmationRequired):
		return "confirmation_required"
	case errors.Is(err, lamigrate.ErrInvalidConfig):
		return "invalid_config"
	case errors.Is(err, lamigrate.ErrMigrationNotFoundInLatestBatch),
		errors.Is(err, lamigrate.ErrBatchNotLatest),
		errors.Is(err, lamigrate.ErrRefreshNothingToRollback):
		return "invalid_config"
	default:
		return "execution_error"
	}
}

// verbPresent returns the present-tense verb for a command.
func verbPresent(cmd string) string {
	switch cmd {
	case "up":
		return "migrate"
	case "down", "reset":
		return "rollback"
	case "refresh":
		return "refresh"
	case "repair":
		return "repair"
	default:
		return "process"
	}
}

// verbPast returns the past-tense verb for a command.
func verbPast(cmd string) string {
	switch cmd {
	case "up":
		return "applied"
	case "down", "reset":
		return "rolled back"
	case "refresh":
		return "refreshed"
	case "repair":
		return "repaired"
	default:
		return "processed"
	}
}