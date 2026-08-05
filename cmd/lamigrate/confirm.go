package main

import (
	"bufio"
	"fmt"
	"os"
	"strings"
)

// ConfirmReset prompts for confirmation before resetting all migrations.
// If yes is true, the prompt is skipped.
func ConfirmReset(yes bool) {
	if yes {
		return
	}
	confirmOrAbort("This will rollback ALL migrations. Continue?")
}

// ConfirmImport prompts for confirmation before importing legacy migrations.
// If yes is true, the prompt is skipped.
func ConfirmImport(yes bool) {
	if yes {
		return
	}
	confirmOrAbort("This will import legacy migrations as already applied. Continue?")
}

// ConfirmAdoptPrototype prompts for confirmation before adopting a prototype table.
// If yes is true, the prompt is skipped.
func ConfirmAdoptPrototype(yes bool) {
	if yes {
		return
	}
	confirmOrAbort("This will adopt the prototype table. Continue?")
}

// ConfirmRepair prompts for confirmation before modifying migration metadata.
// If yes is true, the prompt is skipped.
func ConfirmRepair(yes bool) {
	if yes {
		return
	}
	confirmOrAbort("This will modify migration metadata. Continue?")
}

// ConfirmRefresh prompts for confirmation before refreshing all migrations.
// If yes is true, the prompt is skipped.
func ConfirmRefresh(yes bool) {
	if yes {
		return
	}
	confirmOrAbort("This will rollback all migrations and re-apply. Continue?")
}

// confirmOrAbort reads a y/n answer from stdin. On any answer other
// than y/Y/yes/YES (case-insensitive, trimmed) the process exits with
// ExitUsage. Non-interactive (piped/CI) stdin requires --yes or aborts.
func confirmOrAbort(prompt string) {
	if !isTerminal(os.Stdin) {
		fmt.Fprintln(os.Stderr, "aborted: non-interactive mode requires --yes for destructive operations")
		os.Exit(ExitUsage)
	}
	fmt.Fprintf(os.Stderr, "%s [y/N] ", prompt)
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Scan()
	answer := strings.TrimSpace(strings.ToLower(scanner.Text()))
	switch answer {
	case "y", "yes":
		// continue
	default:
		fmt.Fprintln(os.Stderr, "aborted.")
		os.Exit(ExitUsage)
	}
}

// isTerminal reports whether f is connected to an interactive terminal.
func isTerminal(f *os.File) bool {
	fi, err := f.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}
