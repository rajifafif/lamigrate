package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/lamigrate/lamigrate"
	_ "github.com/go-sql-driver/mysql"
)

func main() {
	// Scan os.Args to separate global flags from command + command args.
	// Go's flag package stops at first non-flag arg, so we need to
	// find the command boundary and parse flags only from the prefix.
	globalFlags, cmdName, cmdArgs := splitArgs(os.Args[1:])

	dir, dsn, table, pretend := parseGlobalFlags(globalFlags)

	if *dsn == "" {
		*dsn = os.Getenv("LAMIGRATE_DSN")
	}
	if *dsn == "" {
		fmt.Fprintln(os.Stderr, "error: -dsn flag or LAMIGRATE_DSN env required")
		printUsage()
		os.Exit(1)
	}
	if cmdName == "" {
		printUsage()
		os.Exit(1)
	}

	ctx := context.Background()

	switch cmdName {
	case "up":
		m, err := lamigrate.New(*dir, *dsn)
		if err != nil {
			fatal(err)
		}
		defer m.Close()
		m.Table(*table)

		n := parseN(cmdArgs, 0)
		if *pretend {
			err = m.PretendUp(ctx, n...)
		} else {
			err = m.Up(ctx, n...)
		}
		if err != nil {
			fatal(err)
		}

	case "down":
		m, err := lamigrate.New(*dir, *dsn)
		if err != nil {
			fatal(err)
		}
		defer m.Close()
		m.Table(*table)

		n := parseN(cmdArgs, 0)
		if *pretend {
			err = m.PretendDown(ctx, n...)
		} else {
			err = m.Down(ctx, n...)
		}
		if err != nil {
			fatal(err)
		}

	case "reset":
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

	case "make":
		if len(cmdArgs) < 1 {
			fmt.Fprintln(os.Stderr, "usage: lamigrate make <migration_name>")
			os.Exit(1)
		}
		name := strings.Join(cmdArgs, "_")
		m, err := lamigrate.New(*dir, *dsn)
		if err != nil {
			fatal(err)
		}
		defer m.Close()

		base, err := m.Make(name)
		if err != nil {
			fatal(err)
		}
		fmt.Printf("Created:  %s.up.sql\n", base)
		fmt.Printf("Created:  %s.down.sql\n", base)

	case "import":
		m, err := lamigrate.New(*dir, *dsn)
		if err != nil {
			fatal(err)
		}
		defer m.Close()
		m.Table(*table)

		if err := m.ImportLegacy(ctx); err != nil {
			fatal(err)
		}

	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n\n", cmdName)
		printUsage()
		os.Exit(1)
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
  -pretend  Show SQL without executing

Commands:
  up [N]            Apply next N pending migrations (all if omitted)
  down [N]          Rollback N from last batch (all in batch if omitted)
  reset             Rollback ALL migrations
  status            Show applied vs pending
  make <name>       Create new migration file pair
  import            Import legacy numbered files as already applied

Examples:
  lamigrate -dsn "user:pass@tcp(localhost:3306)/mydb" up
  lamigrate -dsn "..." -pretend down
  lamigrate -dsn "..." status
  lamigrate -dsn "..." make create_users_table
  lamigrate -dsn "..." import
`)
}

// splitArgs splits os.Args[1:] into: global flags, command name, command args.
// Global flags start with "-". The command is the first non-flag arg.
// Everything after the command is command args.
func splitArgs(args []string) (globalFlags []string, cmdName string, cmdArgs []string) {
	for i, arg := range args {
		if strings.HasPrefix(arg, "-") {
			// Check if this is a boolean flag without value (like -pretend)
			// or a key=value flag (-dsn "...") or -flag value
			if arg == "-pretend" {
				globalFlags = append(globalFlags, arg)
			} else if strings.Contains(arg, "=") {
				globalFlags = append(globalFlags, arg)
			} else {
				// Might be -flag value (consume next arg as value)
				globalFlags = append(globalFlags, arg)
				if i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
					globalFlags = append(globalFlags, args[i+1])
					// Skip next arg
					args = append(args[:i+1], args[i+2:]...)
				}
			}
		} else {
			cmdName = arg
			cmdArgs = args[i+1:]
			return
		}
	}
	return
}

func parseGlobalFlags(args []string) (dir, dsn, table *string, pretend *bool) {
	dirVal := "sql/migrations"
	dsnVal := ""
	tableVal := "migrations"
	pretendVal := false

	dir = &dirVal
	dsn = &dsnVal
	table = &tableVal
	pretend = &pretendVal

	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "-dir" && i+1 < len(args):
			i++
			*dir = args[i]
		case arg == "-dsn" && i+1 < len(args):
			i++
			*dsn = args[i]
		case arg == "-table" && i+1 < len(args):
			i++
			*table = args[i]
		case arg == "-pretend":
			*pretend = true
		// Handle -flag=value syntax
		case strings.HasPrefix(arg, "-dir="):
			*dir = strings.TrimPrefix(arg, "-dir=")
		case strings.HasPrefix(arg, "-dsn="):
			*dsn = strings.TrimPrefix(arg, "-dsn=")
		case strings.HasPrefix(arg, "-table="):
			*table = strings.TrimPrefix(arg, "-table=")
		}
	}
	return
}

func parseN(args []string, idx int) []int {
	if idx >= len(args) {
		return nil
	}
	n := 0
	fmt.Sscanf(args[idx], "%d", &n)
	if n > 0 {
		return []int{n}
	}
	return nil
}

func fatal(err error) {
	fmt.Fprintf(os.Stderr, "error: %v\n", err)
	os.Exit(1)
}
