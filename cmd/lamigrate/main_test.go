package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

func TestParseMigrationCreate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		command string
		args    []string
		want    string
	}{
		{"migration", []string{"create", "create_users_table"}, "create_users_table"},
		{"migration", []string{"create", "create", "users", "table"}, "create_users_table"},
		{"make", []string{"create_users_table"}, "create_users_table"},
		{"make:migration", []string{"create_users_table"}, "create_users_table"},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.command, func(t *testing.T) {
			t.Parallel()
			got, matched, err := parseMigrationCreate(tt.command, tt.args)
			if err != nil {
				t.Fatalf("parseMigrationCreate() error = %v", err)
			}
			if !matched || got != tt.want {
				t.Fatalf("got=%q matched=%v want=%q", got, matched, tt.want)
			}
		})
	}
}

func TestParseMigrationCreateRejectsInvalidShape(t *testing.T) {
	t.Parallel()
	for _, args := range [][]string{nil, {"create"}, {"delete", "users"}, {"create", "create_users_table", "--pretend"}} {
		if _, matched, err := parseMigrationCreate("migration", args); !matched || err == nil {
			t.Errorf("args=%v matched=%v err=%v", args, matched, err)
		}
	}
}

func TestMakeAliasesRejectMisplacedFlags(t *testing.T) {
	t.Parallel()
	for _, command := range []string{"make", "make:migration"} {
		if _, matched, err := parseMigrationCreate(command, []string{"create_users_table", "-pretend"}); !matched || err == nil {
			t.Errorf("command=%s matched=%v err=%v", command, matched, err)
		}
	}
}

func TestParseMigrationCreateIgnoresOtherCommands(t *testing.T) {
	t.Parallel()
	if _, matched, err := parseMigrationCreate("up", nil); matched || err != nil {
		t.Fatalf("matched=%v err=%v", matched, err)
	}
}

func TestSplitArgsMissingValueDoesNotPanic(t *testing.T) {
	t.Parallel()
	flags, command, args := splitArgs([]string{"-dir", "foo"})
	if command != "" || len(args) != 0 || len(flags) != 2 {
		t.Fatalf("flags=%v command=%q args=%v", flags, command, args)
	}
}

func TestParseGlobalFlagsRejectsUnknownAndMissing(t *testing.T) {
	t.Parallel()
	for _, args := range [][]string{{"-dri", "/tmp"}, {"-dir"}, {"-dir="}, {"-dir", "-pretend"}} {
		if _, _, _, _, _, _, _, _, _, _, err := parseGlobalFlags(args); err == nil {
			t.Errorf("parseGlobalFlags(%v) unexpectedly succeeded", args)
		}
	}
}

func TestParseGlobalFlagsAcceptsPretendSpellings(t *testing.T) {
	t.Parallel()
	for _, flag := range []string{"-pretend", "--pretend"} {
		_, _, _, _, pretend, _, _, _, _, _, err := parseGlobalFlags([]string{flag})
		if err != nil || !*pretend {
			t.Fatalf("flag=%q pretend=%v err=%v", flag, pretend, err)
		}
		flags, command, _ := splitArgs([]string{flag, "status"})
		if len(flags) != 1 || command != "status" {
			t.Fatalf("flag=%q flags=%v command=%q", flag, flags, command)
		}
	}
}

func TestParseNRejectsMalformedAndBroadeningValues(t *testing.T) {
	t.Parallel()
	for _, args := range [][]string{{"0"}, {"-1"}, {"bad"}, {"1", "2"}} {
		if _, err := parseN(args); err == nil {
			t.Errorf("parseN(%v) unexpectedly succeeded", args)
		}
	}
}

func TestMigrationCreateSubprocessWithoutDSN(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("covered on Unix CI")
	}
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(t.TempDir(), "lamigrate")
	build := exec.Command("go", "build", "-o", bin, "./cmd/lamigrate")
	build.Dir = root
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build CLI: %v\n%s", err, output)
	}

	for _, command := range [][]string{
		{"migration", "create", "create_users_table"},
		{"make", "add_email_to_users_table"},
		{"make:migration", "backfill_user_slugs"},
	} {
		dir := filepath.Join(t.TempDir(), "migrations")
		args := append([]string{"-dir", dir}, command...)
		cmd := exec.Command(bin, args...)
		cmd.Env = append(os.Environ(), "LAMIGRATE_DSN=")
		output, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("%v failed: %v\n%s", command, err, output)
		}
		files, err := filepath.Glob(filepath.Join(dir, "*.sql"))
		if err != nil || len(files) != 2 {
			t.Fatalf("%v files=%v err=%v", command, files, err)
		}
		if !strings.Contains(string(output), "Template:") {
			t.Fatalf("%v output missing template: %s", command, output)
		}
	}
}

func TestUnknownFlagSubprocessCreatesNothing(t *testing.T) {
	if os.Getenv("LAMIGRATE_TEST_BAD_FLAG") == "1" {
		os.Args = []string{"lamigrate", "-dri", t.TempDir(), "migration", "create", "create_users_table"}
		main()
		return
	}
	cmd := exec.Command(os.Args[0], "-test.run=TestUnknownFlagSubprocessCreatesNothing")
	cmd.Env = append(os.Environ(), "LAMIGRATE_TEST_BAD_FLAG=1")
	if err := cmd.Run(); err == nil {
		t.Fatal("unknown flag subprocess unexpectedly succeeded")
	}
}

func TestUnknownCommandDoesNotRequireDSN(t *testing.T) {
	if os.Getenv("LAMIGRATE_TEST_UNKNOWN_COMMAND") == "1" {
		os.Args = []string{"lamigrate", "unknown"}
		main()
		return
	}
	cmd := exec.Command(os.Args[0], "-test.run=TestUnknownCommandDoesNotRequireDSN")
	cmd.Env = append(os.Environ(), "LAMIGRATE_TEST_UNKNOWN_COMMAND=1", "LAMIGRATE_DSN=")
	output, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatal("unknown command subprocess unexpectedly succeeded")
	}
	if !strings.Contains(string(output), "unknown command: unknown") {
		t.Fatalf("unexpected output: %s", output)
	}
}

// --- LM-012 tests ---

func TestVersionSubprocess(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("covered on Unix CI")
	}
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(t.TempDir(), "lamigrate")
	build := exec.Command("go", "build", "-o", bin, "./cmd/lamigrate")
	build.Dir = root
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build CLI: %v\n%s", err, output)
	}

	cmd := exec.Command(bin, "version")
	cmd.Env = append(os.Environ(), "LAMIGRATE_DSN=")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("version command failed: %v\n%s", err, output)
	}
	got := strings.TrimSpace(string(output))
	if got != "0.3.0-experimental" {
		t.Fatalf("version output = %q, want %q", got, "0.3.0-experimental")
	}
}

func TestHelpFlagSubprocess(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("covered on Unix CI")
	}
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(t.TempDir(), "lamigrate")
	build := exec.Command("go", "build", "-o", bin, "./cmd/lamigrate")
	build.Dir = root
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build CLI: %v\n%s", err, output)
	}

	for _, flag := range []string{"-h", "--help"} {
		cmd := exec.Command(bin, flag)
		cmd.Env = append(os.Environ(), "LAMIGRATE_DSN=")
		output, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("%s failed: %v\n%s", flag, err, output)
		}
		if !strings.Contains(string(output), "lamigrate —") {
			t.Fatalf("%s output missing usage header: %s", flag, output)
		}
		if !strings.Contains(string(output), "version") {
			t.Fatalf("%s output missing version command: %s", flag, output)
		}
	}
}

func TestUnknownCommandExitCode2(t *testing.T) {
	if os.Getenv("LAMIGRATE_TEST_EXIT_CODE") == "1" {
		os.Args = []string{"lamigrate", "bogus"}
		main()
		return
	}
	cmd := exec.Command(os.Args[0], "-test.run=TestUnknownCommandExitCode2")
	cmd.Env = append(os.Environ(), "LAMIGRATE_TEST_EXIT_CODE=1", "LAMIGRATE_DSN=")
	err := cmd.Run()
	if err == nil {
		t.Fatal("expected non-zero exit code for unknown command")
	}
	exitErr, ok := err.(*exec.ExitError)
	if !ok {
		t.Fatalf("expected ExitError, got %T: %v", err, err)
	}
	got := exitErr.ExitCode()
	if got != ExitUsage {
		t.Fatalf("exit code = %d, want %d (ExitUsage)", got, ExitUsage)
	}
}

func TestVersionIsNotDatabaseCommand(t *testing.T) {
	t.Parallel()
	if isDatabaseCommand("version") {
		t.Fatal("version should not be a database command")
	}
}

func TestHelpAndYesAreBooleanGlobalFlags(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		flag string
		next string
	}{
		{"-h", "status"},
		{"--help", "status"},
		{"-y", "reset"},
		{"--yes", "reset"},
		{"--json", "status"},
	} {
		flags, command, args := splitArgs([]string{tc.flag, tc.next})
		if len(flags) != 1 || command != tc.next || len(args) != 0 {
			t.Errorf("splitArgs([%s %s]): flags=%v command=%q args=%v", tc.flag, tc.next, flags, command, args)
		}
	}
}

func TestParseGlobalFlagsYesAndHelp(t *testing.T) {
	t.Parallel()
	_, _, _, _, _, yes, help, _, _, _, err := parseGlobalFlags([]string{"-y"})
	if err != nil || !*yes || *help {
		t.Fatalf("-y: yes=%v help=%v err=%v", yes, help, err)
	}

	_, _, _, _, _, yes, help, _, _, _, err = parseGlobalFlags([]string{"--yes"})
	if err != nil || !*yes || *help {
		t.Fatalf("--yes: yes=%v help=%v err=%v", yes, help, err)
	}

	_, _, _, _, _, yes, help, _, _, _, err = parseGlobalFlags([]string{"-h"})
	if err != nil || *yes || !*help {
		t.Fatalf("-h: yes=%v help=%v err=%v", yes, help, err)
	}

	_, _, _, _, _, yes, help, _, _, _, err = parseGlobalFlags([]string{"--help"})
	if err != nil || *yes || !*help {
		t.Fatalf("--help: yes=%v help=%v err=%v", yes, help, err)
	}
}

func TestResetWithoutYesPromptsAndAborts(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("covered on Unix CI")
	}
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(t.TempDir(), "lamigrate")
	build := exec.Command("go", "build", "-o", bin, "./cmd/lamigrate")
	build.Dir = root
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build CLI: %v\n%s", err, output)
	}

	cmd := exec.Command(bin, "-dsn", "u:p@tcp(127.0.0.1:3306)/db", "reset")
	cmd.Env = append(os.Environ(), "LAMIGRATE_DSN=")
	output, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatal("reset without -y should have failed")
	}
	if !strings.Contains(string(output), "aborted") {
		t.Fatalf("expected 'aborted' in output: %s", output)
	}
}

func TestResetWithYesSkipsConfirmation(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("covered on Unix CI")
	}
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(t.TempDir(), "lamigrate")
	build := exec.Command("go", "build", "-o", bin, "./cmd/lamigrate")
	build.Dir = root
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build CLI: %v\n%s", err, output)
	}

	cmd := exec.Command(bin, "-dsn", "u:p@tcp(127.0.0.1:3306)/db", "-y", "reset")
	cmd.Env = append(os.Environ(), "LAMIGRATE_DSN=")
	output, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatal("reset with -y and bad DSN should have failed")
	}
	if strings.Contains(string(output), "aborted") {
		t.Fatalf("should not have prompted/aborted with -y: %s", output)
	}
}

func TestImportWithoutYesPromptsAndAborts(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("covered on Unix CI")
	}
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(t.TempDir(), "lamigrate")
	build := exec.Command("go", "build", "-o", bin, "./cmd/lamigrate")
	build.Dir = root
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build CLI: %v\n%s", err, output)
	}

	cmd := exec.Command(bin, "-dsn", "u:p@tcp(127.0.0.1:3306)/db", "import")
	cmd.Env = append(os.Environ(), "LAMIGRATE_DSN=")
	output, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatal("import without -y should have failed")
	}
	if !strings.Contains(string(output), "aborted") {
		t.Fatalf("expected 'aborted' in output: %s", output)
	}
}

func TestImportWithYesSkipsConfirmation(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("covered on Unix CI")
	}
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(t.TempDir(), "lamigrate")
	build := exec.Command("go", "build", "-o", bin, "./cmd/lamigrate")
	build.Dir = root
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build CLI: %v\n%s", err, output)
	}

	cmd := exec.Command(bin, "-dsn", "u:p@tcp(127.0.0.1:3306)/db", "-y", "import")
	cmd.Env = append(os.Environ(), "LAMIGRATE_DSN=")
	output, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatal("import with -y and bad DSN should have failed")
	}
	if strings.Contains(string(output), "aborted") {
		t.Fatalf("should not have prompted/aborted with -y: %s", output)
	}
}

func TestVersionSubprocessExitCode0(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("covered on Unix CI")
	}
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(t.TempDir(), "lamigrate")
	build := exec.Command("go", "build", "-o", bin, "./cmd/lamigrate")
	build.Dir = root
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build CLI: %v\n%s", err, output)
	}

	cmd := exec.Command(bin, "version")
	cmd.Env = append(os.Environ(), "LAMIGRATE_DSN=")
	err = cmd.Run()
	if err != nil {
		exitErr, ok := err.(*exec.ExitError)
		if ok {
			t.Fatalf("version exit code = %d, want 0", exitErr.ExitCode())
		}
		t.Fatalf("version failed: %v", err)
	}
}

func TestUnknownFlagExitCode(t *testing.T) {
	if os.Getenv("LAMIGRATE_TEST_BAD_FLAG_EXIT") == "1" {
		os.Args = []string{"lamigrate", "-unknown-flag"}
		main()
		return
	}
	cmd := exec.Command(os.Args[0], "-test.run=TestUnknownFlagExitCode")
	cmd.Env = append(os.Environ(), "LAMIGRATE_TEST_BAD_FLAG_EXIT=1", "LAMIGRATE_DSN=")
	err := cmd.Run()
	if err == nil {
		t.Fatal("expected non-zero exit code for unknown flag")
	}
	exitErr, ok := err.(*exec.ExitError)
	if !ok {
		t.Fatalf("expected ExitError, got %T: %v", err, err)
	}
	got := exitErr.ExitCode()
	if got != ExitExecution {
		t.Fatalf("exit code = %d, want %d (ExitExecution for unknown flag)", got, ExitExecution)
	}
}

func TestExitCodeConstants(t *testing.T) {
	t.Parallel()
	if ExitSuccess != 0 {
		t.Errorf("ExitSuccess = %d, want 0", ExitSuccess)
	}
	if ExitExecution != 1 {
		t.Errorf("ExitExecution = %d, want 1", ExitExecution)
	}
	if ExitUsage != 2 {
		t.Errorf("ExitUsage = %d, want 2", ExitUsage)
	}
	if ExitLockTimeout != 3 {
		t.Errorf("ExitLockTimeout = %d, want 3", ExitLockTimeout)
	}
	if ExitDirtyState != 4 {
		t.Errorf("ExitDirtyState = %d, want 4", ExitDirtyState)
	}
}

func TestVersionRequiresNoArgs(t *testing.T) {
	if os.Getenv("LAMIGRATE_TEST_VERSION_ARGS") == "1" {
		os.Args = []string{"lamigrate", "version", "extra"}
		main()
		return
	}
	cmd := exec.Command(os.Args[0], "-test.run=TestVersionRequiresNoArgs")
	cmd.Env = append(os.Environ(), "LAMIGRATE_TEST_VERSION_ARGS=1", "LAMIGRATE_DSN=")
	output, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatal("version with args should have failed")
	}
	if !strings.Contains(string(output), "does not accept arguments") {
		t.Fatalf("expected 'does not accept arguments' in output: %s", output)
	}
}

func TestNoArgsPrintsUsageAndExits2(t *testing.T) {
	if os.Getenv("LAMIGRATE_TEST_NO_ARGS") == "1" {
		os.Args = []string{"lamigrate"}
		main()
		return
	}
	cmd := exec.Command(os.Args[0], "-test.run=TestNoArgsPrintsUsageAndExits2")
	cmd.Env = append(os.Environ(), "LAMIGRATE_TEST_NO_ARGS=1", "LAMIGRATE_DSN=")
	err := cmd.Run()
	if err == nil {
		t.Fatal("no-args should have failed")
	}
	exitErr, ok := err.(*exec.ExitError)
	if !ok {
		t.Fatalf("expected ExitError, got %T: %v", err, err)
	}
	if exitErr.ExitCode() != ExitUsage {
		t.Fatalf("exit code = %d, want %d (ExitUsage)", exitErr.ExitCode(), ExitUsage)
	}
}

func TestSplitArgsYesDoesNotConsumeCommand(t *testing.T) {
	t.Parallel()
	flags, command, args := splitArgs([]string{"-y", "reset"})
	if len(flags) != 1 || flags[0] != "-y" {
		t.Fatalf("flags = %v, want [-y]", flags)
	}
	if command != "reset" {
		t.Fatalf("command = %q, want %q", command, "reset")
	}
	if len(args) != 0 {
		t.Fatalf("args = %v, want []", args)
	}
}

func TestVersionString(t *testing.T) {
	t.Parallel()
	if version != "0.3.0-experimental" {
		t.Errorf("version = %q, want %q", version, "0.3.0-experimental")
	}
}

func TestExitCodesAreContiguous(t *testing.T) {
	t.Parallel()
	codes := []int{ExitSuccess, ExitExecution, ExitUsage, ExitLockTimeout, ExitDirtyState}
	for i, c := range codes {
		if c != i {
			t.Errorf("exit code index %d = %d, expected %d", i, c, i)
		}
	}
	_ = strconv.Itoa(ExitSuccess) // ensure import used
}

// ============================================================
// LM-030 new tests
// ============================================================

// runJSONCmd runs a CLI command and returns only stdout (stderr is discarded).
func runJSONCmd(t *testing.T, bin string, args ...string) []byte {
	t.Helper()
	cmd := exec.Command(bin, args...)
	cmd.Env = append(os.Environ(), "LAMIGRATE_DSN=")
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &bytes.Buffer{}
	_ = cmd.Run()
	return stdout.Bytes()
}

func TestJSONOutput(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("covered on Unix CI")
	}
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(t.TempDir(), "lamigrate")
	build := exec.Command("go", "build", "-o", bin, "./cmd/lamigrate")
	build.Dir = root
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build CLI: %v\n%s", err, output)
	}

	output := runJSONCmd(t, bin, "--json", "version")
	var jout JSONOutput
	if err := json.Unmarshal(output, &jout); err != nil {
		t.Fatalf("invalid JSON output: %v\nraw: %s", err, output)
	}
	if jout.Version != 1 {
		t.Fatalf("version = %d, want 1", jout.Version)
	}
	if jout.Command != "version" {
		t.Fatalf("command = %q, want %q", jout.Command, "version")
	}
	if jout.Error != nil {
		t.Fatalf("unexpected error: %+v", jout.Error)
	}
}

func TestHumanOutput(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("covered on Unix CI")
	}
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(t.TempDir(), "lamigrate")
	build := exec.Command("go", "build", "-o", bin, "./cmd/lamigrate")
	build.Dir = root
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build CLI: %v\n%s", err, output)
	}

	cmd := exec.Command(bin, "version")
	cmd.Env = append(os.Environ(), "LAMIGRATE_DSN=")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("version failed: %v\n%s", err, output)
	}

	got := strings.TrimSpace(string(output))
	if got != "0.3.0-experimental" {
		t.Fatalf("version output = %q, want %q", got, "0.3.0-experimental")
	}
	if strings.HasPrefix(got, "{") {
		t.Fatalf("human output should not be JSON: %s", got)
	}
}

func TestSecretRedaction(t *testing.T) {
	t.Parallel()

	tests := []struct {
		input string
		want  string
	}{
		{"user:secret@tcp(localhost:3306)/db", "user:***@tcp(localhost:3306)/db"},
		{"u:p@tcp(127.0.0.1:3306)/mydb?parseTime=true", "u:***@tcp(127.0.0.1:3306)/mydb?parseTime=true"},
		{"user:@tcp(host:3306)/db", "user:***@tcp(host:3306)/db"},
		{"no-at-sign", "no-at-sign"},
		{"user@host", "user@host"},
		{"", ""},
	}

	for _, tt := range tests {
		got := RedactDSN(tt.input)
		if got != tt.want {
			t.Errorf("RedactDSN(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestConfirmResetRequiresYes(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("covered on Unix CI")
	}
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(t.TempDir(), "lamigrate")
	build := exec.Command("go", "build", "-o", bin, "./cmd/lamigrate")
	build.Dir = root
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build CLI: %v\n%s", err, output)
	}

	cmd := exec.Command(bin, "-dsn", "u:p@tcp(127.0.0.1:3306)/db", "reset")
	cmd.Env = append(os.Environ(), "LAMIGRATE_DSN=")
	output, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatal("reset without --yes should have failed")
	}
	if !strings.Contains(string(output), "aborted") {
		t.Fatalf("expected 'aborted' in output: %s", output)
	}

	cmd = exec.Command(bin, "-dsn", "u:p@tcp(127.0.0.1:3306)/db", "-y", "reset")
	cmd.Env = append(os.Environ(), "LAMIGRATE_DSN=")
	output, err = cmd.CombinedOutput()
	if err == nil {
		t.Fatal("reset with --yes and bad DSN should have failed")
	}
	if strings.Contains(string(output), "aborted") {
		t.Fatalf("should not have prompted/aborted with --yes: %s", output)
	}
}

func TestConfirmImportRequiresYes(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("covered on Unix CI")
	}
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(t.TempDir(), "lamigrate")
	build := exec.Command("go", "build", "-o", bin, "./cmd/lamigrate")
	build.Dir = root
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build CLI: %v\n%s", err, output)
	}

	cmd := exec.Command(bin, "-dsn", "u:p@tcp(127.0.0.1:3306)/db", "import")
	cmd.Env = append(os.Environ(), "LAMIGRATE_DSN=")
	output, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatal("import without --yes should have failed")
	}
	if !strings.Contains(string(output), "aborted") {
		t.Fatalf("expected 'aborted' in output: %s", output)
	}

	cmd = exec.Command(bin, "-dsn", "u:p@tcp(127.0.0.1:3306)/db", "-y", "import")
	cmd.Env = append(os.Environ(), "LAMIGRATE_DSN=")
	output, err = cmd.CombinedOutput()
	if err == nil {
		t.Fatal("import with --yes and bad DSN should have failed")
	}
	if strings.Contains(string(output), "aborted") {
		t.Fatalf("should not have prompted/aborted with --yes: %s", output)
	}
}

func TestStepFlagParsing(t *testing.T) {
	t.Parallel()

	step, rest, err := parseStepFlag([]string{"--step", "3"})
	if err != nil {
		t.Fatalf("parseStepFlag --step 3: %v", err)
	}
	if step != 3 || len(rest) != 0 {
		t.Fatalf("step=%d rest=%v, want step=3 rest=[]", step, rest)
	}

	step, rest, err = parseStepFlag([]string{"--step=3"})
	if err != nil {
		t.Fatalf("parseStepFlag --step=3: %v", err)
	}
	if step != 3 || len(rest) != 0 {
		t.Fatalf("step=%d rest=%v, want step=3 rest=[]", step, rest)
	}

	step, rest, err = parseStepFlag([]string{"1"})
	if err != nil {
		t.Fatalf("parseStepFlag no --step: %v", err)
	}
	if step != 0 || len(rest) != 1 {
		t.Fatalf("step=%d rest=%v, want step=0 rest=[1]", step, rest)
	}

	_, _, err = parseStepFlag([]string{"--step"})
	if err == nil {
		t.Fatal("parseStepFlag --step without value should fail")
	}

	_, _, err = parseStepFlag([]string{"--step", "0"})
	if err == nil {
		t.Fatal("parseStepFlag --step 0 should fail")
	}

	_, _, err = parseStepFlag([]string{"--step", "bad"})
	if err == nil {
		t.Fatal("parseStepFlag --step bad should fail")
	}

	limit, err := resolveLimit([]string{"--step", "5"})
	if err != nil {
		t.Fatalf("resolveLimit --step 5: %v", err)
	}
	if limit.IsZero() {
		t.Fatal("resolveLimit --step 5 returned zero StepLimit")
	}

	limit, err = resolveLimit([]string{"2"})
	if err != nil {
		t.Fatalf("resolveLimit 2: %v", err)
	}
	if limit.IsZero() {
		t.Fatal("resolveLimit 2 returned zero StepLimit")
	}

	limit, err = resolveLimit(nil)
	if err != nil {
		t.Fatalf("resolveLimit nil: %v", err)
	}
	if limit.IsZero() {
		t.Fatal("resolveLimit nil returned zero StepLimit")
	}
}

func TestAllCommandsJSON(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("covered on Unix CI")
	}
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(t.TempDir(), "lamigrate")
	build := exec.Command("go", "build", "-o", bin, "./cmd/lamigrate")
	build.Dir = root
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build CLI: %v\n%s", err, output)
	}

	var jout JSONOutput

	// version --json: valid JSON, version=1
	output := runJSONCmd(t, bin, "--json", "version")
	if err := json.Unmarshal(output, &jout); err != nil {
		t.Fatalf("version --json: invalid JSON: %v\n%s", err, output)
	}
	if jout.Version != 1 {
		t.Fatalf("version --json: version=%d, want 1", jout.Version)
	}

	// up --json with bad DSN: valid JSON error
	output = runJSONCmd(t, bin, "--json", "-dsn", "bad", "up")
	if err := json.Unmarshal(output, &jout); err != nil {
		t.Fatalf("up --json bad DSN: invalid JSON: %v\n%s", err, output)
	}
	if jout.Error == nil {
		t.Fatalf("up --json bad DSN: expected error in JSON output")
	}

	// down --json with bad DSN: valid JSON error
	output = runJSONCmd(t, bin, "--json", "-dsn", "bad", "down")
	if err := json.Unmarshal(output, &jout); err != nil {
		t.Fatalf("down --json bad DSN: invalid JSON: %v\n%s", err, output)
	}
	if jout.Error == nil {
		t.Fatalf("down --json bad DSN: expected error in JSON output")
	}

	// reset --json with bad DSN (-y): valid JSON error
	output = runJSONCmd(t, bin, "--json", "-dsn", "bad", "-y", "reset")
	if err := json.Unmarshal(output, &jout); err != nil {
		t.Fatalf("reset --json bad DSN: invalid JSON: %v\n%s", err, output)
	}
	if jout.Error == nil {
		t.Fatalf("reset --json bad DSN: expected error in JSON output")
	}

	// status --json with bad DSN: valid JSON error
	output = runJSONCmd(t, bin, "--json", "-dsn", "bad", "status")
	if err := json.Unmarshal(output, &jout); err != nil {
		t.Fatalf("status --json bad DSN: invalid JSON: %v\n%s", err, output)
	}
	if jout.Error == nil {
		t.Fatalf("status --json bad DSN: expected error in JSON output")
	}

	// make --json: valid JSON with created paths
	dir := filepath.Join(t.TempDir(), "migrations")
	output = runJSONCmd(t, bin, "--json", "-dir", dir, "make", "create_users_table")
	if err := json.Unmarshal(output, &jout); err != nil {
		t.Fatalf("make --json: invalid JSON: %v\n%s", err, output)
	}
	if jout.Version != 1 {
		t.Fatalf("make --json: version=%d, want 1", jout.Version)
	}
	if jout.Command != "make" {
		t.Fatalf("make --json: command=%q, want %q", jout.Command, "make")
	}

	// import --json with bad DSN (-y): valid JSON error
	output = runJSONCmd(t, bin, "--json", "-dsn", "bad", "-y", "import")
	if err := json.Unmarshal(output, &jout); err != nil {
		t.Fatalf("import --json bad DSN: invalid JSON: %v\n%s", err, output)
	}
	if jout.Error == nil {
		t.Fatalf("import --json bad DSN: expected error in JSON output")
	}
}

func TestParseGlobalFlagsAcceptsJSON(t *testing.T) {
	t.Parallel()
	_, _, _, _, _, _, _, jsonOut, _, _, err := parseGlobalFlags([]string{"--json"})
	if err != nil || !*jsonOut {
		t.Fatalf("--json: jsonOut=%v err=%v", jsonOut, err)
	}
}

func TestParseGlobalFlagsAcceptsIgnoreMissingSource(t *testing.T) {
	t.Parallel()
	_, _, _, _, _, _, _, _, ignoreMissing, _, err := parseGlobalFlags([]string{"--ignore-missing-source"})
	if err != nil || !*ignoreMissing {
		t.Fatalf("--ignore-missing-source: ignoreMissing=%v err=%v", ignoreMissing, err)
	}
}

func TestParseReasonFlag(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		args []string
		want string
		rest []string
	}{
		{
			name: "space_separated",
			args: []string{"remove-failed", "20260101120000_create_users", "--yes", "--reason", "file removed from branch"},
			want: "file removed from branch",
			rest: []string{"remove-failed", "20260101120000_create_users", "--yes"},
		},
		{
			name: "equals_form",
			args: []string{"remove-failed", "20260101120000_create_users", "--reason=manual cleanup"},
			want: "manual cleanup",
			rest: []string{"remove-failed", "20260101120000_create_users"},
		},
		{
			name: "no_reason",
			args: []string{"show", "20260101120000_create_users"},
			want: "",
			rest: []string{"show", "20260101120000_create_users"},
		},
		{
			name: "last_occurrence_wins",
			args: []string{"mark-applied", "m", "--reason", "first", "--reason", "second"},
			want: "second",
			rest: []string{"mark-applied", "m"},
		},
		{
			name: "reason_at_end_without_value",
			args: []string{"remove-failed", "m", "--reason"},
			want: "",
			rest: []string{"remove-failed", "m"},
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, rest := parseReasonFlag(tc.args)
			if got != tc.want {
				t.Errorf("parseReasonFlag(%v) reason = %q, want %q", tc.args, got, tc.want)
			}
			if !reflect.DeepEqual(rest, tc.rest) {
				t.Errorf("parseReasonFlag(%v) rest = %v, want %v", tc.args, rest, tc.rest)
			}
		})
	}
}

func TestRunRepairRequiresOperationAndMigration(t *testing.T) {
	t.Parallel()

	// Too few args: usage error.
	_, _, _, err := parseRepairArgs([]string{"remove-failed"}, false)
	if err == nil {
		t.Fatal("parseRepairArgs with 1 arg should fail")
	}
	if !strings.Contains(err.Error(), "usage: lamigrate repair") {
		t.Errorf("expected usage error, got: %v", err)
	}
}

func TestRunRepairRejectsUnknownFlagInArgs(t *testing.T) {
	t.Parallel()

	// A stray flag (not --yes/-y) after the operation/migration should be rejected.
	_, _, _, err := parseRepairArgs([]string{"show", "m", "--bogus"}, false)
	if err == nil {
		t.Fatal("parseRepairArgs with unexpected flag should fail")
	}
	if !strings.Contains(err.Error(), "unexpected flag") {
		t.Errorf("expected unexpected-flag error, got: %v", err)
	}
}

func TestRunRepairRejectsUnexpectedArgument(t *testing.T) {
	t.Parallel()

	// A stray positional after operation/migration should be rejected.
	_, _, _, err := parseRepairArgs([]string{"show", "m", "extra"}, false)
	if err == nil {
		t.Fatal("parseRepairArgs with extra positional should fail")
	}
	if !strings.Contains(err.Error(), "unexpected argument") {
		t.Errorf("expected unexpected-argument error, got: %v", err)
	}
}

func TestParseRepairArgsAcceptsCommandLevelYes(t *testing.T) {
	t.Parallel()

	// Command-level --yes / -y after the migration name must be accepted and
	// flip the yes flag on, in addition to the global before-command flag.
	op, mig, yes, err := parseRepairArgs([]string{"forget", "m", "--yes"}, false)
	if err != nil {
		t.Fatalf("parseRepairArgs: %v", err)
	}
	if op != "forget" || mig != "m" || !yes {
		t.Fatalf("got op=%q mig=%q yes=%v", op, mig, yes)
	}

	_, _, yes, err = parseRepairArgs([]string{"forget", "m", "-y"}, false)
	if err != nil || !yes {
		t.Fatalf("-y: err=%v yes=%v", err, yes)
	}

	// Global yes remains effective.
	op, mig, yes, err = parseRepairArgs([]string{"forget", "m"}, true)
	if err != nil || op != "forget" || mig != "m" || !yes {
		t.Fatalf("global yes: err=%v op=%q mig=%q yes=%v", err, op, mig, yes)
	}
}