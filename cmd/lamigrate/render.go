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
	default:
		return "execution_error"
	}
}

// verbPresent returns the present-tense verb for a command.
func verbPresent(cmd string) string {
	switch cmd {
	case "up":
		return "migrate"
	case "down":
		return "rollback"
	case "reset":
		return "rollback"
	default:
		return "process"
	}
}

// verbPast returns the past-tense verb for a command.
func verbPast(cmd string) string {
	switch cmd {
	case "up":
		return "applied"
	case "down":
		return "rolled back"
	case "reset":
		return "rolled back"
	default:
		return "processed"
	}
}
