package main

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/rajifafif/lamigrate"
)

func main() {
	globalFlags, cmdName, cmdArgs := splitArgs(os.Args[1:])

	dir, dsn, table, pretend, err := parseGlobalFlags(globalFlags)
	if err != nil {
		fatal(err)
	}
	if cmdName == "" {
		printUsage()
		os.Exit(1)
	}

	if name, matched, err := parseMigrationCreate(cmdName, cmdArgs); matched {
		if err != nil {
			fatal(err)
		}
		created, err := lamigrate.CreateMigration(*dir, name)
		if err != nil {
			fatal(err)
		}
		fmt.Printf("Created:  %s\n", created.UpPath)
		fmt.Printf("Created:  %s\n", created.DownPath)
		fmt.Printf("Template: %s\n", created.Template)
		return
	}

	if !isDatabaseCommand(cmdName) {
		fatal(fmt.Errorf("unknown command: %s", cmdName))
	}
	if *dsn == "" {
		*dsn = os.Getenv("LAMIGRATE_DSN")
	}
	if *dsn == "" {
		fmt.Fprintln(os.Stderr, "error: -dsn flag or LAMIGRATE_DSN env required")
		printUsage()
		os.Exit(1)
	}

	ctx := context.Background()

	switch cmdName {
	case "up":
		n, err := parseN(cmdArgs)
		if err != nil {
			fatal(fmt.Errorf("up: %w", err))
		}
		m, err := lamigrate.New(*dir, *dsn)
		if err != nil {
			fatal(err)
		}
		defer m.Close()
		m.Table(*table)

		if *pretend {
			err = m.PretendUp(ctx, n...)
		} else {
			err = m.Up(ctx, n...)
		}
		if err != nil {
			fatal(err)
		}

	case "down":
		n, err := parseN(cmdArgs)
		if err != nil {
			fatal(fmt.Errorf("down: %w", err))
		}
		m, err := lamigrate.New(*dir, *dsn)
		if err != nil {
			fatal(err)
		}
		defer m.Close()
		m.Table(*table)

		if *pretend {
			err = m.PretendDown(ctx, n...)
		} else {
			err = m.Down(ctx, n...)
		}
		if err != nil {
			fatal(err)
		}

	case "reset":
		if err := requireNoArgs("reset", cmdArgs); err != nil {
			fatal(err)
		}
		m, err := lamigrate.New(*dir, *dsn)
		if err != nil {
			fatal(err)
		}
		defer m.Close()
		m.Table(*table)

		if *pretend {
			err = m.PretendDown(ctx)
		} else {
			err = m.Reset(ctx)
		}
		if err != nil {
			fatal(err)
		}

	case "status":
		if err := requireNoArgs("status", cmdArgs); err != nil {
			fatal(err)
		}
		m, err := lamigrate.New(*dir, *dsn)
		if err != nil {
			fatal(err)
		}
		defer m.Close()
		m.Table(*table)

		statuses, err := m.Status(ctx)
		if err != nil {
			fatal(err)
		}

		fmt.Println()
		fmt.Printf("  %-55s %-10s %-8s %s\n", "Migration", "Status", "Batch", "Applied At")
		fmt.Printf("  %-55s %-10s %-8s %s\n",
			strings.Repeat("-", 55), strings.Repeat("-", 10),
			strings.Repeat("-", 8), strings.Repeat("-", 19))

		for _, s := range statuses {
			statusStr := "PENDING"
			batchStr := ""
			appliedStr := ""
			if s.Applied {
				statusStr = "APPLIED"
				batchStr = fmt.Sprintf("%d", s.Batch)
				appliedStr = s.AppliedAt
			}
			fmt.Printf("  %-55s %-10s %-8s %s\n", s.Filename, statusStr, batchStr, appliedStr)
		}
		fmt.Println()

	case "import":
		if err := requireNoArgs("import", cmdArgs); err != nil {
			fatal(err)
		}
		m, err := lamigrate.New(*dir, *dsn)
		if err != nil {
			fatal(err)
		}
		defer m.Close()
		m.Table(*table)

		if err := m.ImportLegacy(ctx); err != nil {
			fatal(err)
		}
	}
}

func printUsage() {
	fmt.Fprintf(os.Stderr, `lamigrate — Laravel-style migrations for Go + MySQL

Usage:
  lamigrate [global-flags] <command> [command-args]

Global flags (must appear BEFORE command):
  -dir      Migrations directory (default: sql/migrations)
  -dsn      MySQL DSN or set LAMIGRATE_DSN env
  -table    Tracking table name (default: migrations)
  -pretend, --pretend
             Show SQL without executing

Commands:
  up [N]            Apply next N pending migrations (all if omitted)
  down [N]          Rollback N from last batch (all in batch if omitted)
  reset             Rollback ALL migrations
  status            Show applied vs pending
  migration create <name>
                     Create a Laravel-like .up.sql/.down.sql pair (no DSN)
  make <name>       Compatibility alias for migration create
  make:migration <name>
                     Laravel-style alias for migration create
  import            Import legacy numbered files as already applied

Examples:
  lamigrate -dsn "user:pass@tcp(localhost:3306)/mydb" up
  lamigrate -dsn "..." -pretend down
  lamigrate -dsn "..." status
  lamigrate -dir sql/migrations migration create create_users_table
  lamigrate -dsn "..." import
`)
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
	case "up", "down", "reset", "status", "import":
		return true
	default:
		return false
	}
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
		if arg == "-pretend" || arg == "--pretend" || strings.Contains(arg, "=") {
			continue
		}
		if i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
			globalFlags = append(globalFlags, args[i+1])
			i++
		}
	}
	return globalFlags, "", nil
}

func parseGlobalFlags(args []string) (dir, dsn, table *string, pretend *bool, err error) {
	dirVal := "sql/migrations"
	dsnVal := ""
	tableVal := "migrations"
	pretendVal := false

	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "-dir":
			if i+1 >= len(args) || strings.HasPrefix(args[i+1], "-") {
				return nil, nil, nil, nil, fmt.Errorf("global flag -dir requires a value")
			}
			i++
			dirVal = args[i]
		case arg == "-dsn":
			if i+1 >= len(args) || strings.HasPrefix(args[i+1], "-") {
				return nil, nil, nil, nil, fmt.Errorf("global flag -dsn requires a value")
			}
			i++
			dsnVal = args[i]
		case arg == "-table":
			if i+1 >= len(args) || strings.HasPrefix(args[i+1], "-") {
				return nil, nil, nil, nil, fmt.Errorf("global flag -table requires a value")
			}
			i++
			tableVal = args[i]
		case arg == "-pretend" || arg == "--pretend":
			pretendVal = true
		case strings.HasPrefix(arg, "-dir="):
			dirVal = strings.TrimPrefix(arg, "-dir=")
		case strings.HasPrefix(arg, "-dsn="):
			dsnVal = strings.TrimPrefix(arg, "-dsn=")
		case strings.HasPrefix(arg, "-table="):
			tableVal = strings.TrimPrefix(arg, "-table=")
		default:
			return nil, nil, nil, nil, fmt.Errorf("unknown global flag: %s", arg)
		}
	}
	if strings.TrimSpace(dirVal) == "" {
		return nil, nil, nil, nil, fmt.Errorf("global flag -dir requires a non-empty value")
	}
	if strings.TrimSpace(tableVal) == "" {
		return nil, nil, nil, nil, fmt.Errorf("global flag -table requires a non-empty value")
	}
	return &dirVal, &dsnVal, &tableVal, &pretendVal, nil
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

func fatal(err error) {
	fmt.Fprintf(os.Stderr, "error: %v\n", err)
	os.Exit(1)
}
