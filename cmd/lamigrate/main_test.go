package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
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
		if _, _, _, _, err := parseGlobalFlags(args); err == nil {
			t.Errorf("parseGlobalFlags(%v) unexpectedly succeeded", args)
		}
	}
}

func TestParseGlobalFlagsAcceptsPretendSpellings(t *testing.T) {
	t.Parallel()
	for _, flag := range []string{"-pretend", "--pretend"} {
		_, _, _, pretend, err := parseGlobalFlags([]string{flag})
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
