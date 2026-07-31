package main

import (
	"reflect"
	"testing"
)

func TestExtractSeedDir(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		args      []string
		wantDir   string
		wantFlags []string
		wantErr   bool
	}{
		{[]string{"-seed-dir", "database/seeders", "-dsn", "test"}, "database/seeders", []string{"-dsn", "test"}, false},
		{[]string{"--seed-dir=database/seeders", "--json"}, "database/seeders", []string{"--json"}, false},
		{[]string{"--seed-dir"}, "", nil, true},
		{[]string{"-seed-dir="}, "", nil, true},
	} {
		gotDir, gotFlags, err := extractSeedDir(test.args)
		if test.wantErr {
			if err == nil {
				t.Errorf("extractSeedDir(%v) unexpectedly succeeded", test.args)
			}
			continue
		}
		if err != nil || gotDir != test.wantDir || !reflect.DeepEqual(gotFlags, test.wantFlags) {
			t.Errorf("extractSeedDir(%v) = %q, %v, %v; want %q, %v, nil", test.args, gotDir, gotFlags, err, test.wantDir, test.wantFlags)
		}
	}
}

func TestParseSeedRequest(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		command string
		args    []string
		class   string
		wantErr bool
	}{
		{"seed", nil, "", false},
		{"db:seed", []string{"--class", "DatabaseSeeder"}, "DatabaseSeeder", false},
		{"seed", []string{"--class=RolesSeeder"}, "RolesSeeder", false},
		{"seed", []string{"--class"}, "", true},
		{"seed", []string{"--class", "A", "--class", "B"}, "", true},
		{"seed", []string{"unexpected"}, "", true},
	} {
		request, err := parseSeedRequest(test.command, test.args, "sql/seeders")
		if test.wantErr {
			if err == nil {
				t.Errorf("parseSeedRequest(%q, %v) unexpectedly succeeded", test.command, test.args)
			}
			continue
		}
		if err != nil || request.Directory != "sql/seeders" || request.Class != test.class {
			t.Errorf("parseSeedRequest(%q, %v) = %+v, %v", test.command, test.args, request, err)
		}
	}
}

func TestSeedCommandsAreDatabaseCommands(t *testing.T) {
	t.Parallel()
	for _, command := range []string{"seed", "db:seed"} {
		if !isDatabaseCommand(command) {
			t.Errorf("isDatabaseCommand(%q) = false, want true", command)
		}
	}
}
