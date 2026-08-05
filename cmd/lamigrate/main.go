package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/rajifafif/lamigrate"
)

const version = "0.3.0-experimental"

func main() {
	globalFlags, cmdName, cmdArgs := splitArgs(os.Args[1:])

	dir, dsn, configPath, table, pretend, yes, help, jsonOut, ignoreMissing, ignoreDrift, err := parseGlobalFlags(globalFlags)
	if err != nil {
		fatal(err)
	}
	reason, cmdArgs := parseReasonFlag(cmdArgs)

	if *help {
		printUsage()
		os.Exit(ExitSuccess)
	}

	if cmdName == "" {
		printUsage()
		os.Exit(ExitUsage)
	}

	// --- version command (offline, no DSN required) ---
	if cmdName == "version" {
		if err := requireNoArgs("version", cmdArgs); err != nil {
			fatal(err)
		}
		renderVersion(os.Stdout, *jsonOut)
		return
	}

	// Signal context for all operations that may use DB.
	ctx, stop := signalContext(context.Background())
	defer stop()

	// --- make / migration create (offline, no DSN required) ---
	if name, matched, err := parseMigrationCreate(cmdName, cmdArgs); matched {
		if err != nil {
			fatal(err)
		}
		created, err := lamigrate.Make(ctx, *dir, name)
		if err != nil {
			fatal(err)
		}
		renderMake(os.Stdout, created, *jsonOut)
		return
	}

	if !isDatabaseCommand(cmdName) {
		fatalExit(ExitUsage, fmt.Errorf("unknown command: %s", cmdName))
	}

	// Config resolution for database commands.
	// Precedence: -dsn > LAMIGRATE_DSN > --config > default search.
	usedInlineDSN := strings.TrimSpace(*dsn) != ""

	resolved, err := resolveDSN(*dsn, *configPath, ".")
	if err != nil {
		fatal(err)
	}
	*dsn = resolved

	// Warn when -dsn is used directly (appears in shell history / ps output).
	if usedInlineDSN {
		fmt.Fprintln(os.Stderr, "Warning: -dsn flag exposes credentials in shell history and process list. Consider using LAMIGRATE_DSN or a config file instead.")
	}

	// Confirmation prompts for destructive commands (before DB connection).
	switch cmdName {
	case "reset":
		ConfirmReset(*yes)
	case "import":
		ConfirmImport(*yes)
	}

	// --- import uses the legacy API (old Migrate type) ---
	if cmdName == "import" {
		if err := requireNoArgs("import", cmdArgs); err != nil {
			handleCommandError(cmdName, *jsonOut, err)
		}
		m, err := lamigrate.New(*dir, *dsn)
		if err != nil {
			handleCommandError(cmdName, *jsonOut, err)
		}
		defer m.Close()
		m.Table(*table)

		if err := m.ImportLegacy(ctx); err != nil {
			handleCommandError(cmdName, *jsonOut, err)
		}
		if *jsonOut {
			writeJSON(os.Stdout, cmdName, map[string]interface{}{
				"command": cmdName,
				"status":  "success",
			}, nil)
		}
		return
	}

	// --- Create migrator using new API ---
	opts := lamigrate.Options{
		Directory:           *dir,
		TableName:           *table,
		IgnoreMissingSource:   *ignoreMissing,
		IgnoreChecksumDrift:  *ignoreDrift,
	}
	m, err := lamigrate.OpenMySQL(*dsn, opts)
	if err != nil {
		handleCommandError(cmdName, *jsonOut, err)
	}

	switch cmdName {
	case "up":
		limit, err := resolveLimit(cmdArgs)
		if err != nil {
			handleCommandError(cmdName, *jsonOut, fmt.Errorf("up: %w", err))
		}
		if *pretend {
			plan, err := m.PreviewUp(ctx, limit)
			if err != nil {
				handleCommandError(cmdName, *jsonOut, err)
			}
			renderPlanView(os.Stdout, plan, cmdName, *jsonOut)
		} else {
			result, err := m.Up(ctx, limit)
			if err != nil {
				handleCommandError(cmdName, *jsonOut, err)
			}
			renderResult(os.Stdout, result, cmdName, *jsonOut)
		}

	case "down":
		target, err := resolveDownTarget(cmdArgs)
		if err != nil {
			handleCommandError(cmdName, *jsonOut, fmt.Errorf("down: %w", err))
		}
		if *pretend {
			plan, err := m.PreviewDown(ctx, target)
			if err != nil {
				handleCommandError(cmdName, *jsonOut, err)
			}
			renderPlanView(os.Stdout, plan, cmdName, *jsonOut)
		} else {
			result, err := m.Down(ctx, target)
			if err != nil {
				handleCommandError(cmdName, *jsonOut, err)
			}
			renderResult(os.Stdout, result, cmdName, *jsonOut)
		}

	case "reset":
		if err := requireNoArgs("reset", cmdArgs); err != nil {
			handleCommandError(cmdName, *jsonOut, err)
		}
		if *pretend {
			plan, err := m.PreviewReset(ctx)
			if err != nil {
				handleCommandError(cmdName, *jsonOut, err)
			}
			renderPlanView(os.Stdout, plan, cmdName, *jsonOut)
		} else {
			result, err := m.Reset(ctx)
			if err != nil {
				handleCommandError(cmdName, *jsonOut, err)
			}
			renderResult(os.Stdout, result, cmdName, *jsonOut)
		}

	case "refresh":
		ConfirmRefresh(*yes)
		target, err := resolveRefreshTarget(cmdArgs)
		if err != nil {
			handleCommandError(cmdName, *jsonOut, fmt.Errorf("refresh: %w", err))
		}
		if *pretend {
			plan, err := m.PreviewRefresh(ctx, target)
			if err != nil {
				handleCommandError(cmdName, *jsonOut, err)
			}
			renderRefreshPlanView(os.Stdout, plan, *jsonOut)
		} else {
			result, err := m.Refresh(ctx, target)
			if err != nil {
				handleCommandError(cmdName, *jsonOut, err)
			}
			renderRefreshResult(os.Stdout, result, *jsonOut)
		}

	case "status":
		if err := requireNoArgs("status", cmdArgs); err != nil {
			handleCommandError(cmdName, *jsonOut, err)
		}
		report, err := m.Status(ctx)
		if err != nil {
			handleCommandError(cmdName, *jsonOut, err)
		}
		renderStatus(os.Stdout, report, *jsonOut)

	case "repair":
		if err := runRepair(ctx, m, cmdArgs, *jsonOut, *yes, reason); err != nil {
			handleCommandError("repair", *jsonOut, err)
		}
	}
}

// handleCommandError renders the error (JSON or human) and exits.
func handleCommandError(cmdName string, jsonOut bool, err error) {
	if jsonOut {
		renderErrorJSON(os.Stdout, cmdName, err)
	}
	fatalExit(exitCodeForError(err), err)
}

// exitCodeForError maps a lamigrate error to the appropriate exit code.
func exitCodeForError(err error) int {
	switch {
	case errors.Is(err, lamigrate.ErrLockTimeout):
		return ExitLockTimeout
	case errors.Is(err, lamigrate.ErrDirtyState),
		errors.Is(err, lamigrate.ErrChecksumDrift):
		return ExitDirtyState
	case errors.Is(err, lamigrate.ErrMigrationNotFoundInLatestBatch),
		errors.Is(err, lamigrate.ErrBatchNotLatest),
		errors.Is(err, lamigrate.ErrRefreshNothingToRollback):
		return ExitUsage
	default:
		return ExitExecution
	}
}

// resolveLimit parses --step from args and falls back to positional N.
// Returns a validated StepLimit.
func resolveLimit(args []string) (lamigrate.StepLimit, error) {
	stepVal, remaining, err := parseStepFlag(args)
	if err != nil {
		return lamigrate.StepLimit{}, err
	}
	if stepVal > 0 {
		if len(remaining) > 0 {
			return lamigrate.StepLimit{}, fmt.Errorf("unexpected arguments: %v", remaining)
		}
		return lamigrate.Steps(stepVal)
	}
	// Fall back to positional N for backward compatibility.
	ns, parseErr := parseN(remaining)
	if parseErr != nil {
		return lamigrate.StepLimit{}, parseErr
	}
	if len(ns) > 0 {
		return lamigrate.Steps(ns[0])
	}
	return lamigrate.All(), nil
}


// resolveDownTarget parses down command args and returns a DownTarget.
// Supports: --step N, --batch N, positional name, or bare (All).
func resolveDownTarget(args []string) (lamigrate.DownTarget, error) {
	batchVal, remaining, err := parseBatchFlag(args)
	if err != nil {
		return lamigrate.DownTarget{}, err
	}

	stepVal, remaining2, err := parseStepFlag(remaining)
	if err != nil {
		return lamigrate.DownTarget{}, err
	}

	// Mutual exclusivity checks.
	if batchVal > 0 && stepVal > 0 {
		return lamigrate.DownTarget{}, fmt.Errorf("--batch and --step are mutually exclusive")
	}

	if batchVal > 0 {
		if len(remaining2) > 0 {
			return lamigrate.DownTarget{}, fmt.Errorf("--batch does not accept additional arguments")
		}
		return lamigrate.DownToBatch(batchVal)
	}

	if stepVal > 0 {
		if len(remaining2) > 0 {
			return lamigrate.DownTarget{}, fmt.Errorf("--step and positional name are mutually exclusive")
		}
		return lamigrate.DownSteps(stepVal)
	}

	// Check for positional name (non-numeric arg).
	if len(remaining2) == 1 {
		name := remaining2[0]
		// Detect if it's numeric (old positional step count) or a name.
		if _, err := strconv.Atoi(name); err != nil {
			return lamigrate.DownToName(name)
		}
		// Numeric: treat as legacy positional step count.
		n, parseErr := strconv.Atoi(name)
		if parseErr != nil || n <= 0 {
			return lamigrate.DownTarget{}, fmt.Errorf("step count must be a positive integer")
		}
		return lamigrate.DownSteps(n)
	}

	if len(remaining2) > 1 {
		return lamigrate.DownTarget{}, fmt.Errorf("expected at most one argument")
	}

	return lamigrate.DownAll(), nil
}

// resolveRefreshTarget parses refresh command args and returns a RefreshTarget.
func resolveRefreshTarget(args []string) (lamigrate.RefreshTarget, error) {
	stepVal, remaining, err := parseStepFlag(args)
	if err != nil {
		return lamigrate.RefreshTarget{}, err
	}

	if stepVal > 0 {
		if len(remaining) > 0 {
			return lamigrate.RefreshTarget{}, fmt.Errorf("--step and positional name are mutually exclusive")
		}
		return lamigrate.RefreshSteps(stepVal)
	}

	// Check for positional name.
	if len(remaining) == 1 {
		return lamigrate.RefreshToName(remaining[0])
	}
	if len(remaining) > 1 {
		return lamigrate.RefreshTarget{}, fmt.Errorf("expected at most one argument")
	}

	return lamigrate.RefreshAll(), nil
}

// parseBatchFlag extracts --batch N from args, returning the value and remaining args.
func parseBatchFlag(args []string) (batch int, rest []string, err error) {
	for i := 0; i < len(args); i++ {
		if args[i] == "--batch" {
			if i+1 >= len(args) {
				return 0, nil, fmt.Errorf("--batch requires a value")
			}
			n, parseErr := strconv.Atoi(args[i+1])
			if parseErr != nil || n <= 0 {
				return 0, nil, fmt.Errorf("--batch requires a positive integer")
			}
			rest = append(append([]string{}, args[:i]...), args[i+2:]...)
			return n, rest, nil
		}
		if strings.HasPrefix(args[i], "--batch=") {
			val := strings.TrimPrefix(args[i], "--batch=")
			n, parseErr := strconv.Atoi(val)
			if parseErr != nil || n <= 0 {
				return 0, nil, fmt.Errorf("--batch requires a positive integer")
			}
			rest = append(append([]string{}, args[:i]...), args[i+1:]...)
			return n, rest, nil
		}
	}
	return 0, args, nil
}

// parseStepFlag extracts --step N from args, returning the value and remaining args.
func parseStepFlag(args []string) (step int, rest []string, err error) {
	for i := 0; i < len(args); i++ {
		if args[i] == "--step" {
			if i+1 >= len(args) {
				return 0, nil, fmt.Errorf("--step requires a value")
			}
			n, parseErr := strconv.Atoi(args[i+1])
			if parseErr != nil || n <= 0 {
				return 0, nil, fmt.Errorf("--step requires a positive integer")
			}
			rest = append(append([]string{}, args[:i]...), args[i+2:]...)
			return n, rest, nil
		}
		if strings.HasPrefix(args[i], "--step=") {
			val := strings.TrimPrefix(args[i], "--step=")
			n, parseErr := strconv.Atoi(val)
			if parseErr != nil || n <= 0 {
				return 0, nil, fmt.Errorf("--step requires a positive integer")
			}
			rest = append(append([]string{}, args[:i]...), args[i+1:]...)
			return n, rest, nil
		}
	}
	return 0, args, nil
}

// printUsage writes the help text to stderr.
func printUsage() {
	fmt.Fprintf(os.Stderr, `lamigrate — Laravel-style migrations for Go + MySQL

Usage:
  lamigrate [global-flags] <command> [command-args]

Global flags (must appear BEFORE command):
  -dir      Migrations directory (default: sql/migrations)
  -dsn      MySQL DSN or set LAMIGRATE_DSN env
  -config   Path to config file (config.yaml / config.yml / .env)
  -table    Tracking table name (default: migrations)
  -pretend, --pretend
             Show SQL without executing
  -y, --yes
             Skip confirmation prompts (reset, import)
  --ignore-missing-source
             Ignore applied migrations whose source file no longer
             exists (MISSING_SOURCE), so up/down/reset are not blocked.
             Other safety checks (dirty state, checksum drift on present
             files) still apply. Use for shared-database workflows.
  --ignore-checksum-drift
             Ignore checksum mismatches between applied metadata and
             source files. A warning is printed to stderr instead of
             blocking. All other safety checks remain. Use for
             recovering from modified migration files.
  --json    Output structured JSON (experimental, version 1)
  -h, --help
             Show this help text

Commands:
  up [--step N]    Apply next N pending migrations (all if omitted)
  down [--step N] | [--batch N] | <name>
                   Rollback migrations (latest batch).
                   --step N: at most N from last batch.
                   --batch N: all of batch N (must be latest).
                   <name>: named migration + everything newer in latest batch.
  refresh [--step N] | <name>
                   Rollback + re-apply migrations (requires confirmation).
                   --step N: last N migrations (globally).
                   <name>: rollback all, re-apply up to name.
  reset            Rollback ALL migrations (requires confirmation)
  status           Show applied vs pending
  repair <op> <migration> [--yes] [--reason ...]
                     Repair migration metadata (show | mark-applied |
                     mark-rolled-back | remove-failed | forget). Requires
                     --yes and --reason for mutations.
  migration create <name>
                     Create a Laravel-like .up.sql/.down.sql pair (no DSN)
  make <name>       Compatibility alias for migration create
  make:migration <name>
                     Laravel-style alias for migration create
  import            Import legacy numbered files as already applied
                       (requires confirmation)
  version           Print version and exit

Examples:
  lamigrate -dsn "user:pass@tcp(localhost:3306)/mydb" up
  lamigrate -dsn "..." --step 2 up
  lamigrate -dsn "..." --pretend down
  lamigrate -dsn "..." status
  lamigrate -dir sql/migrations migration create create_users_table
  lamigrate -dsn "..." -y import
  lamigrate -dsn "..." repair show 20260101120000_create_users
  lamigrate -dsn "..." -y repair remove-failed 20260101120000_create_users \
             --reason "file removed from branch"
  lamigrate --json version
  lamigrate -config config.yaml up
`)
}

// splitArgs separates the global flag prefix from the command and its arguments.
// It never mutates its input and leaves validation to parseGlobalFlags.
func splitArgs(args []string) (globalFlags []string, cmdName string, cmdArgs []string) {
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if !strings.HasPrefix(arg, "-") {
			return globalFlags, arg, append([]string(nil), args[i+1:]...)
		}

		globalFlags = append(globalFlags, arg)
		if arg == "-pretend" || arg == "--pretend" ||
			arg == "-y" || arg == "--yes" ||
			arg == "-h" || arg == "--help" ||
			arg == "--json" ||
			arg == "--ignore-missing-source" ||
			arg == "--ignore-checksum-drift" ||
			strings.Contains(arg, "=") {
			continue
		}
		if i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
			globalFlags = append(globalFlags, args[i+1])
			i++
		}
	}
	return globalFlags, "", nil
}

func parseGlobalFlags(args []string) (dir, dsn, configPath, table *string, pretend, yes, help, jsonOut, ignoreMissing, ignoreDrift *bool, err error) {
	dirVal := "sql/migrations"
	dsnVal := ""
	configVal := ""
	tableVal := "migrations"
	pretendVal := false
	yesVal := false
	helpVal := false
	jsonVal := false
	ignoreMissingVal := false
	ignoreDriftVal := false

	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "-dir":
			if i+1 >= len(args) || strings.HasPrefix(args[i+1], "-") {
				return nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, fmt.Errorf("global flag -dir requires a value")
			}
			i++
			dirVal = args[i]
		case arg == "-dsn":
			if i+1 >= len(args) || strings.HasPrefix(args[i+1], "-") {
				return nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, fmt.Errorf("global flag -dsn requires a value")
			}
			i++
			dsnVal = args[i]
		case arg == "-config" || arg == "--config":
			if i+1 >= len(args) || strings.HasPrefix(args[i+1], "-") {
				return nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, fmt.Errorf("global flag -config requires a value")
			}
			i++
			configVal = args[i]
		case arg == "-table":
			if i+1 >= len(args) || strings.HasPrefix(args[i+1], "-") {
				return nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, fmt.Errorf("global flag -table requires a value")
			}
			i++
			tableVal = args[i]
		case arg == "-pretend" || arg == "--pretend":
			pretendVal = true
		case arg == "-y" || arg == "--yes":
			yesVal = true
		case arg == "-h" || arg == "--help":
			helpVal = true
		case arg == "--json":
			jsonVal = true
		case arg == "--ignore-missing-source":
			ignoreMissingVal = true
		case arg == "--ignore-checksum-drift":
			ignoreDriftVal = true
		case strings.HasPrefix(arg, "-dir="):
			dirVal = strings.TrimPrefix(arg, "-dir=")
		case strings.HasPrefix(arg, "-dsn="):
			dsnVal = strings.TrimPrefix(arg, "-dsn=")
		case strings.HasPrefix(arg, "-config="):
			configVal = strings.TrimPrefix(arg, "-config=")
		case strings.HasPrefix(arg, "--config="):
			configVal = strings.TrimPrefix(arg, "--config=")
		case strings.HasPrefix(arg, "-table="):
			tableVal = strings.TrimPrefix(arg, "-table=")
		default:
			return nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, fmt.Errorf("unknown global flag: %s", arg)
		}
	}
	if strings.TrimSpace(dirVal) == "" {
		return nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, fmt.Errorf("global flag -dir requires a non-empty value")
	}
	if strings.TrimSpace(tableVal) == "" {
		return nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, fmt.Errorf("global flag -table requires a non-empty value")
	}
	return &dirVal, &dsnVal, &configVal, &tableVal, &pretendVal, &yesVal, &helpVal, &jsonVal, &ignoreMissingVal, &ignoreDriftVal, nil
}

func parseN(args []string) ([]int, error) {
	if len(args) == 0 {
		return nil, nil
	}
	if len(args) != 1 {
		return nil, fmt.Errorf("expected at most one positive step count")
	}
	n, err := strconv.Atoi(args[0])
	if err != nil || n <= 0 {
		return nil, fmt.Errorf("step count must be a positive integer")
	}
	return []int{n}, nil
}

func requireNoArgs(command string, args []string) error {
	if len(args) != 0 {
		return fmt.Errorf("%s does not accept arguments", command)
	}
	return nil
}

func parseMigrationCreate(cmdName string, cmdArgs []string) (name string, matched bool, err error) {
	var nameParts []string
	switch cmdName {
	case "migration":
		matched = true
		if len(cmdArgs) == 0 || cmdArgs[0] != "create" {
			return "", true, fmt.Errorf("usage: lamigrate migration create <migration_name>")
		}
		nameParts = cmdArgs[1:]
	case "make", "make:migration":
		matched = true
		nameParts = cmdArgs
	default:
		return "", false, nil
	}

	if len(nameParts) == 0 {
		return "", true, fmt.Errorf("usage: lamigrate migration create <migration_name>")
	}
	for _, part := range nameParts {
		if strings.HasPrefix(part, "-") {
			return "", true, fmt.Errorf("migration create flags must appear before the command: %s", part)
		}
	}
	return strings.Join(nameParts, "_"), true, nil
}

func isDatabaseCommand(name string) bool {
	switch name {
	case "up", "down", "reset", "status", "import", "repair", "refresh":
		return true
	default:
		return false
	}
}

// parseReasonFlag extracts --reason <text> (or --reason=<text>) from the
// command args, returning the reason value and the remaining args with the
// flag and its value removed. Only the last occurrence wins.
func parseReasonFlag(args []string) (string, []string) {
	reason := ""
	rest := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--reason" {
			if i+1 < len(args) {
				reason = args[i+1]
				i++
			}
			continue
		}
		if strings.HasPrefix(arg, "--reason=") {
			reason = strings.TrimPrefix(arg, "--reason=")
			continue
		}
		rest = append(rest, arg)
	}
	return reason, rest
}

// runRepair executes the repair subcommand:
//
//	lamigrate repair show <migration>
//	lamigrate repair mark-applied <migration> [--yes] [--reason ...]
//	lamigrate repair mark-rolled-back <migration> [--yes] [--reason ...]
//	lamigrate repair remove-failed <migration> [--yes] [--reason ...]
//	lamigrate repair forget <migration> [--yes] [--reason ...]
//
// "show" is read-only; the mutation operations require confirmation and a
// reason. Confirmation may be supplied either as the global flag before the
// command (-y / --yes) or as a command-level flag after the migration name.
func runRepair(ctx context.Context, m *lamigrate.Migrator, args []string, jsonOut, yes bool, reason string) error {
	operation, migration, yes, err := parseRepairArgs(args, yes)
	if err != nil {
		return err
	}

	request := lamigrate.RepairRequest{
		Operation: operation,
		Migration: migration,
		Yes:       yes,
		Reason:    reason,
	}

	if operation == "show" {
		view, err := m.PreviewRepair(ctx, request)
		if err != nil {
			return err
		}
		renderRepairPlanView(os.Stdout, view, jsonOut)
		return nil
	}

	result, err := m.Repair(ctx, request)
	if err != nil {
		return err
	}
	renderResult(os.Stdout, result, "repair", jsonOut)
	return nil
}

// parseRepairArgs validates and parses the repair subcommand arguments.
// The first two positional args are <operation> and <migration>. Command-level
// --yes / -y after the migration name are accepted in addition to the global
// -y / --yes before the command. --reason is handled by parseReasonFlag and is
// not expected here. Returns the operation, migration, the effective yes flag,
// and an error for malformed input.
func parseRepairArgs(args []string, yes bool) (operation, migration string, effectiveYes bool, err error) {
	if len(args) < 2 {
		return "", "", yes, fmt.Errorf("usage: lamigrate repair <operation> <migration> [--yes] [--reason ...]")
	}
	operation = args[0]
	migration = args[1]
	effectiveYes = yes
	for _, a := range args[2:] {
		switch a {
		case "--yes", "-y":
			effectiveYes = true
		default:
			if strings.HasPrefix(a, "-") {
				return "", "", yes, fmt.Errorf("unexpected flag: %s", a)
			}
			return "", "", yes, fmt.Errorf("unexpected argument: %s", a)
		}
	}
	return operation, migration, effectiveYes, nil
}

// fatal prints the error to stderr and exits with ExitExecution.
func fatal(err error) {
	fmt.Fprintf(os.Stderr, "error: %v\n", err)
	os.Exit(ExitExecution)
}

// fatalExit prints the error to stderr and exits with the given code.
func fatalExit(code int, err error) {
	fmt.Fprintf(os.Stderr, "error: %v\n", err)
	os.Exit(code)
}