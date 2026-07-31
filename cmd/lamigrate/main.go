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

const version = "0.1.0-experimental"

func main() {
	globalFlags, cmdName, cmdArgs := splitArgs(os.Args[1:])

	seedDir, globalFlags, err := extractSeedDir(globalFlags)
	if err != nil {
		fatal(err)
	}
	dir, dsn, configPath, table, pretend, yes, help, jsonOut, err := parseGlobalFlags(globalFlags)
	if err != nil {
		fatal(err)
	}

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

	seedRequest, err := parseSeedRequest(cmdName, cmdArgs, seedDir)
	if err != nil {
		fatalExit(ExitUsage, err)
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
		Directory: *dir,
		TableName: *table,
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
		limit, err := resolveLimit(cmdArgs)
		if err != nil {
			handleCommandError(cmdName, *jsonOut, fmt.Errorf("down: %w", err))
		}
		if *pretend {
			plan, err := m.PreviewDown(ctx, limit)
			if err != nil {
				handleCommandError(cmdName, *jsonOut, err)
			}
			renderPlanView(os.Stdout, plan, cmdName, *jsonOut)
		} else {
			result, err := m.Down(ctx, limit)
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

	case "status":
		if err := requireNoArgs("status", cmdArgs); err != nil {
			handleCommandError(cmdName, *jsonOut, err)
		}
		report, err := m.Status(ctx)
		if err != nil {
			handleCommandError(cmdName, *jsonOut, err)
		}
		renderStatus(os.Stdout, report, *jsonOut)

	case "seed", "db:seed":
		if *pretend {
			plan, err := m.PreviewSeed(ctx, seedRequest)
			if err != nil {
				handleCommandError(cmdName, *jsonOut, err)
			}
			renderSeedPlan(os.Stdout, plan, cmdName, *jsonOut)
		} else {
			result, err := m.Seed(ctx, seedRequest)
			if err != nil {
				handleCommandError(cmdName, *jsonOut, err)
			}
			renderSeedResult(os.Stdout, result, cmdName, *jsonOut)
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
  -seed-dir SQL seed directory (default: sql/seeders)
  -dsn      MySQL DSN or set LAMIGRATE_DSN env
  -config   Path to config file (config.yaml / config.yml / .env)
  -table    Tracking table name (default: migrations)
  -pretend, --pretend
             Show SQL without executing
  -y, --yes
             Skip confirmation prompts (reset, import)
  --json    Output structured JSON (experimental, version 1)
  -h, --help
             Show this help text

Commands:
  up [--step N]    Apply next N pending migrations (all if omitted)
  down [--step N]  Rollback N from last batch (all in batch if omitted)
  reset            Rollback ALL migrations (requires confirmation)
  status           Show applied vs pending
  seed [--class Name]
                   Execute SQL seeders (alias: db:seed)
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

func parseGlobalFlags(args []string) (dir, dsn, configPath, table *string, pretend, yes, help, jsonOut *bool, err error) {
	dirVal := "sql/migrations"
	dsnVal := ""
	configVal := ""
	tableVal := "migrations"
	pretendVal := false
	yesVal := false
	helpVal := false
	jsonVal := false

	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "-dir":
			if i+1 >= len(args) || strings.HasPrefix(args[i+1], "-") {
				return nil, nil, nil, nil, nil, nil, nil, nil, fmt.Errorf("global flag -dir requires a value")
			}
			i++
			dirVal = args[i]
		case arg == "-dsn":
			if i+1 >= len(args) || strings.HasPrefix(args[i+1], "-") {
				return nil, nil, nil, nil, nil, nil, nil, nil, fmt.Errorf("global flag -dsn requires a value")
			}
			i++
			dsnVal = args[i]
		case arg == "-config" || arg == "--config":
			if i+1 >= len(args) || strings.HasPrefix(args[i+1], "-") {
				return nil, nil, nil, nil, nil, nil, nil, nil, fmt.Errorf("global flag -config requires a value")
			}
			i++
			configVal = args[i]
		case arg == "-table":
			if i+1 >= len(args) || strings.HasPrefix(args[i+1], "-") {
				return nil, nil, nil, nil, nil, nil, nil, nil, fmt.Errorf("global flag -table requires a value")
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
			return nil, nil, nil, nil, nil, nil, nil, nil, fmt.Errorf("unknown global flag: %s", arg)
		}
	}
	if strings.TrimSpace(dirVal) == "" {
		return nil, nil, nil, nil, nil, nil, nil, nil, fmt.Errorf("global flag -dir requires a non-empty value")
	}
	if strings.TrimSpace(tableVal) == "" {
		return nil, nil, nil, nil, nil, nil, nil, nil, fmt.Errorf("global flag -table requires a non-empty value")
	}
	return &dirVal, &dsnVal, &configVal, &tableVal, &pretendVal, &yesVal, &helpVal, &jsonVal, nil
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
	case "up", "down", "reset", "status", "import", "seed", "db:seed":
		return true
	default:
		return false
	}
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
