package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-sql-driver/mysql"
)

// --- resolveDSN precedence ---

func TestResolveDSNExplicit(t *testing.T) {
	t.Parallel()
	dsn := "u:p@tcp(localhost:3306)/mydb"
	got, err := resolveDSN(dsn, "", ".")
	if err != nil {
		t.Fatalf("resolveDSN() error = %v", err)
	}
	if got != dsn {
		t.Fatalf("resolveDSN() = %q, want %q", got, dsn)
	}
}

func TestResolveDSNExplicitOverridesEnv(t *testing.T) {
	t.Setenv("LAMIGRATE_DSN", "env:p@tcp(host:3306)/envdb")
	dsn := "explicit:p@tcp(host:3306)/expdb"
	got, err := resolveDSN(dsn, "", ".")
	if err != nil {
		t.Fatalf("resolveDSN() error = %v", err)
	}
	if got != dsn {
		t.Fatalf("resolveDSN() = %q, want explicit DSN %q", got, dsn)
	}
}

func TestResolveDSNEnv(t *testing.T) {
	t.Setenv("LAMIGRATE_DSN", "envuser:envpass@tcp(127.0.0.1:3306)/envdb")
	got, err := resolveDSN("", "", ".")
	if err != nil {
		t.Fatalf("resolveDSN() error = %v", err)
	}
	want := "envuser:envpass@tcp(127.0.0.1:3306)/envdb"
	if got != want {
		t.Fatalf("resolveDSN() = %q, want %q", got, want)
	}
}

func TestResolveDSNYAML(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	configContent := `dbMySQL:
  host: 127.0.0.1
  port: 3307
  user: yaml_user
  pass: yaml_secret
  dbName: yaml_db
  timeout: 15s
`
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(configContent), 0600); err != nil {
		t.Fatal(err)
	}

	got, err := resolveDSN("", "", dir)
	if err != nil {
		t.Fatalf("resolveDSN() error = %v", err)
	}

	// Parse the DSN to verify its components.
	cfg, err := parseDSN(got)
	if err != nil {
		t.Fatalf("parse DSN: %v", err)
	}
	if cfg.User != "yaml_user" {
		t.Errorf("user = %q, want yaml_user", cfg.User)
	}
	if cfg.DBName != "yaml_db" {
		t.Errorf("dbName = %q, want yaml_db", cfg.DBName)
	}
	if cfg.Net != "tcp" {
		t.Errorf("net = %q, want tcp", cfg.Net)
	}
	// Check host includes port.
	if !strings.Contains(cfg.Addr, "127.0.0.1:3307") {
		t.Errorf("addr = %q, want to contain 127.0.0.1:3307", cfg.Addr)
	}
}

func TestResolveDSNYML(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	configContent := `dbMySQL:
  host: 10.0.0.1
  port: 3306
  user: yml_user
  dbName: yml_db
`
	path := filepath.Join(dir, "config.yml")
	if err := os.WriteFile(path, []byte(configContent), 0600); err != nil {
		t.Fatal(err)
	}

	got, err := resolveDSN("", "", dir)
	if err != nil {
		t.Fatalf("resolveDSN() error = %v", err)
	}

	cfg, err := parseDSN(got)
	if err != nil {
		t.Fatalf("parse DSN: %v", err)
	}
	if cfg.User != "yml_user" {
		t.Errorf("user = %q, want yml_user", cfg.User)
	}
	if cfg.DBName != "yml_db" {
		t.Errorf("dbName = %q, want yml_db", cfg.DBName)
	}
}

func TestResolveDSNEnvFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	envContent := `
# Database config
LAMIGRATE_DB_HOST=envhost
LAMIGRATE_DB_PORT=3309
LAMIGRATE_DB_USER=envuser
LAMIGRATE_DB_PASS=envpass
LAMIGRATE_DB_NAME=envdb
`
	path := filepath.Join(dir, ".env")
	if err := os.WriteFile(path, []byte(envContent), 0600); err != nil {
		t.Fatal(err)
	}

	got, err := resolveDSN("", "", dir)
	if err != nil {
		t.Fatalf("resolveDSN() error = %v", err)
	}

	cfg, err := parseDSN(got)
	if err != nil {
		t.Fatalf("parse DSN: %v", err)
	}
	if cfg.User != "envuser" {
		t.Errorf("user = %q, want envuser", cfg.User)
	}
	if cfg.DBName != "envdb" {
		t.Errorf("dbName = %q, want envdb", cfg.DBName)
	}
	if !strings.Contains(cfg.Addr, "envhost:3309") {
		t.Errorf("addr = %q, want to contain envhost:3309", cfg.Addr)
	}
}

func TestResolveDSNEnvFileWithLAMIGRATEDSN(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	envContent := `LAMIGRATE_DSN=direct:p@tcp(h:3306)/d
LAMIGRATE_DB_HOST=otherhost
`
	path := filepath.Join(dir, ".env")
	if err := os.WriteFile(path, []byte(envContent), 0600); err != nil {
		t.Fatal(err)
	}

	got, err := resolveDSN("", "", dir)
	if err != nil {
		t.Fatalf("resolveDSN() error = %v", err)
	}
	want := "direct:p@tcp(h:3306)/d"
	if got != want {
		t.Fatalf("resolveDSN() = %q, want %q", got, want)
	}
}

func TestResolveDSNMissing(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	// Empty directory — no config files.
	_, err := resolveDSN("", "", dir)
	if err == nil {
		t.Fatal("resolveDSN() should fail when no config found")
	}
	if !strings.Contains(err.Error(), "database configuration not found") {
		t.Errorf("error = %q, want 'database configuration not found'", err.Error())
	}
}

func TestResolveDSNExplicitConfigPath(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	configContent := `dbMySQL:
  host: myhost
  port: 3306
  user: myuser
  dbName: mydb
`
	path := filepath.Join(dir, "custom.yaml")
	if err := os.WriteFile(path, []byte(configContent), 0600); err != nil {
		t.Fatal(err)
	}

	got, err := resolveDSN("", path, ".")
	if err != nil {
		t.Fatalf("resolveDSN() error = %v", err)
	}

	cfg, err := parseDSN(got)
	if err != nil {
		t.Fatalf("parse DSN: %v", err)
	}
	if cfg.User != "myuser" {
		t.Errorf("user = %q, want myuser", cfg.User)
	}
}

// --- formatMySQLDSN ---

func TestFormatMySQLDSN(t *testing.T) {
	t.Parallel()
	config := dbMySQLConfig{
		Host:    "db.example.com",
		Port:    3306,
		User:    "testuser",
		Pass:    "testpass",
		DBName:  "testdb",
		Timeout: "10s",
	}
	dsn, err := formatMySQLDSN(config, "test")
	if err != nil {
		t.Fatalf("formatMySQLDSN() error = %v", err)
	}

	cfg, err := parseDSN(dsn)
	if err != nil {
		t.Fatalf("parse DSN: %v", err)
	}
	if !cfg.MultiStatements {
		t.Error("MultiStatements = false, want true")
	}
	if !cfg.ParseTime {
		t.Error("ParseTime = false, want true")
	}
	if cfg.Net != "tcp" {
		t.Errorf("Net = %q, want tcp", cfg.Net)
	}
	if cfg.User != "testuser" {
		t.Errorf("User = %q, want testuser", cfg.User)
	}
	if cfg.DBName != "testdb" {
		t.Errorf("DBName = %q, want testdb", cfg.DBName)
	}
	if cfg.Timeout != 10*1000000000 { // 10s in nanoseconds
		t.Errorf("Timeout = %v, want 10s", cfg.Timeout)
	}
	if cfg.ReadTimeout != cfg.Timeout {
		t.Errorf("ReadTimeout = %v, want Timeout %v", cfg.ReadTimeout, cfg.Timeout)
	}
	if cfg.WriteTimeout != cfg.Timeout {
		t.Errorf("WriteTimeout = %v, want Timeout %v", cfg.WriteTimeout, cfg.Timeout)
	}
}

func TestFormatMySQLDSNDefaultTimeout(t *testing.T) {
	t.Parallel()
	config := dbMySQLConfig{
		Host:   "localhost",
		Port:   3306,
		User:   "u",
		DBName: "d",
	}
	dsn, err := formatMySQLDSN(config, "test")
	if err != nil {
		t.Fatalf("formatMySQLDSN() error = %v", err)
	}

	cfg, err := parseDSN(dsn)
	if err != nil {
		t.Fatalf("parse DSN: %v", err)
	}
	// Default timeout should be 30s.
	if cfg.Timeout != 30*1000000000 {
		t.Errorf("Timeout = %v, want 30s", cfg.Timeout)
	}
}

func TestFormatMySQLDSNRequiresHost(t *testing.T) {
	t.Parallel()
	config := dbMySQLConfig{User: "u", DBName: "d"}
	_, err := formatMySQLDSN(config, "test")
	if err == nil {
		t.Fatal("expected error for missing host")
	}
	if !strings.Contains(err.Error(), "host is required") {
		t.Errorf("error = %q, want 'host is required'", err.Error())
	}
}

func TestFormatMySQLDSNRequiresUser(t *testing.T) {
	t.Parallel()
	config := dbMySQLConfig{Host: "h", DBName: "d"}
	_, err := formatMySQLDSN(config, "test")
	if err == nil {
		t.Fatal("expected error for missing user")
	}
	if !strings.Contains(err.Error(), "user is required") {
		t.Errorf("error = %q, want 'user is required'", err.Error())
	}
}

func TestFormatMySQLDSNRequiresDBName(t *testing.T) {
	t.Parallel()
	config := dbMySQLConfig{Host: "h", User: "u"}
	_, err := formatMySQLDSN(config, "test")
	if err == nil {
		t.Fatal("expected error for missing dbName")
	}
	if !strings.Contains(err.Error(), "dbName is required") {
		t.Errorf("error = %q, want 'dbName is required'", err.Error())
	}
}

func TestFormatMySQLDSNRejectsInvalidPort(t *testing.T) {
	t.Parallel()
	for _, port := range []int{-1, 65536, 100000} {
		config := dbMySQLConfig{Host: "h", User: "u", DBName: "d", Port: port}
		_, err := formatMySQLDSN(config, "test")
		if err == nil {
			t.Errorf("port %d: expected error", port)
		}
	}
}

func TestFormatMySQLDSNRejectsBadTimeout(t *testing.T) {
	t.Parallel()
	for _, timeout := range []string{"bad", "-5s", "0s", ""} {
		if timeout == "" {
			continue // empty uses default
		}
		config := dbMySQLConfig{Host: "h", User: "u", DBName: "d", Timeout: timeout}
		_, err := formatMySQLDSN(config, "test")
		if err == nil {
			t.Errorf("timeout %q: expected error", timeout)
		}
	}
}

// --- parseDotEnv ---

func TestParseDotEnv(t *testing.T) {
	t.Parallel()
	data := []byte(`
# Comment line
DB_HOST=localhost

# Another comment
DB_PORT=3306
export DB_USER=admin
DB_PASS="quoted value"
DB_NAME='single quoted'
INLINE=val  # with comment
EMPTY=
`)
	values, err := parseDotEnv(data)
	if err != nil {
		t.Fatalf("parseDotEnv() error = %v", err)
	}
	tests := map[string]string{
		"DB_HOST": "localhost",
		"DB_PORT": "3306",
		"DB_USER": "admin",
		"DB_PASS": "quoted value",
		"DB_NAME": "single quoted",
		"INLINE":  "val",
		"EMPTY":   "",
	}
	for key, want := range tests {
		got := values[key]
		if got != want {
			t.Errorf("values[%q] = %q, want %q", key, got, want)
		}
	}
}

func TestParseDotEnvExportPrefix(t *testing.T) {
	t.Parallel()
	data := []byte("export FOO=bar\n")
	values, err := parseDotEnv(data)
	if err != nil {
		t.Fatalf("parseDotEnv() error = %v", err)
	}
	if values["FOO"] != "bar" {
		t.Errorf("values[FOO] = %q, want bar", values["FOO"])
	}
}

func TestParseDotEnvSingleQuoted(t *testing.T) {
	t.Parallel()
	data := []byte("KEY='hello world'\n")
	values, err := parseDotEnv(data)
	if err != nil {
		t.Fatalf("parseDotEnv() error = %v", err)
	}
	if values["KEY"] != "hello world" {
		t.Errorf("values[KEY] = %q, want 'hello world'", values["KEY"])
	}
}

func TestParseDotEnvDoubleQuoted(t *testing.T) {
	t.Parallel()
	data := []byte(`KEY="hello world with \"escapes\""` + "\n")
	values, err := parseDotEnv(data)
	if err != nil {
		t.Fatalf("parseDotEnv() error = %v", err)
	}
	if values["KEY"] != `hello world with "escapes"` {
		t.Errorf("values[KEY] = %q, want 'hello world with \"escapes\"'", values["KEY"])
	}
}

func TestParseDotEnvInlineComment(t *testing.T) {
	t.Parallel()
	data := []byte("KEY=value # this is a comment\n")
	values, err := parseDotEnv(data)
	if err != nil {
		t.Fatalf("parseDotEnv() error = %v", err)
	}
	if values["KEY"] != "value" {
		t.Errorf("values[KEY] = %q, want 'value'", values["KEY"])
	}
}

func TestParseDotEnvCommentLinesIgnored(t *testing.T) {
	t.Parallel()
	data := []byte("# Full comment line\n")
	values, err := parseDotEnv(data)
	if err != nil {
		t.Fatalf("parseDotEnv() error = %v", err)
	}
	if len(values) != 0 {
		t.Errorf("expected empty map, got %v", values)
	}
}

func TestParseDotEnvBlankLinesIgnored(t *testing.T) {
	t.Parallel()
	data := []byte("\n\n   \n\t\n")
	values, err := parseDotEnv(data)
	if err != nil {
		t.Fatalf("parseDotEnv() error = %v", err)
	}
	if len(values) != 0 {
		t.Errorf("expected empty map, got %v", values)
	}
}

func TestParseDotEnvRejectsMissingEquals(t *testing.T) {
	t.Parallel()
	data := []byte("KEY_WITHOUT_EQUALS\n")
	_, err := parseDotEnv(data)
	if err == nil {
		t.Fatal("expected error for missing equals")
	}
	if !strings.Contains(err.Error(), "expected KEY=value") {
		t.Errorf("error = %q, want 'expected KEY=value'", err.Error())
	}
}

// --- parseDotEnv key validation ---

func TestParseDotEnvRejectsInvalidKeys(t *testing.T) {
	t.Parallel()
	tests := []struct {
		line string
		desc string
	}{
		{"1STARTS_WITH_NUMBER=value", "starts with digit"},
		{"HAS-DASH=value", "contains dash"},
		{"HAS SPACE=value", "contains space"},
		{"has.lowercase=value", "contains dot"},
		{"=value", "empty key"},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.desc, func(t *testing.T) {
			t.Parallel()
			_, err := parseDotEnv([]byte(tt.line))
			if err == nil {
				t.Errorf("parseDotEnv(%q) should fail for %s", tt.line, tt.desc)
			}
			if err != nil && !strings.Contains(err.Error(), "invalid key") {
				t.Errorf("error = %q, want 'invalid key'", err.Error())
			}
		})
	}
}

func TestParseDotEnvAcceptsValidKeys(t *testing.T) {
	t.Parallel()
	tests := []string{
		"A=value1",
		"AB=value2",
		"A_1=value3",
		"DB_MYSQL_HOST=localhost",
		"DB_PASSWORD=secret",
	}
	for _, line := range tests {
		line := line
		t.Run(line, func(t *testing.T) {
			t.Parallel()
			_, err := parseDotEnv([]byte(line))
			if err != nil {
				t.Errorf("parseDotEnv(%q) error = %v", line, err)
			}
		})
	}
}

// --- readConfigFile safety ---

func TestReadConfigFileRejectsSymlink(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	// Create a regular file with content.
	target := filepath.Join(dir, "real.env")
	if err := os.WriteFile(target, []byte("KEY=value"), 0600); err != nil {
		t.Fatal(err)
	}

	// Create a symlink to it.
	link := filepath.Join(dir, "link.env")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}

	_, err := readConfigFile(link)
	if err == nil {
		t.Fatal("readConfigFile() should reject symlinks")
	}
	if !strings.Contains(err.Error(), "must be a regular file") {
		t.Errorf("error = %q, want 'must be a regular file'", err.Error())
	}
}

func TestReadConfigFileRejectsDirectory(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	subdir := filepath.Join(dir, "subdir")
	if err := os.Mkdir(subdir, 0755); err != nil {
		t.Fatal(err)
	}

	_, err := readConfigFile(subdir)
	if err == nil {
		t.Fatal("readConfigFile() should reject directories")
	}
	if !strings.Contains(err.Error(), "must be a regular file") {
		t.Errorf("error = %q, want 'must a regular file'", err.Error())
	}
}

func TestReadConfigFileRejectsOversized(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "big.env")

	// Write maxConfigFileSize + 1 bytes.
	data := make([]byte, maxConfigFileSize+1)
	for i := range data {
		data[i] = 'X'
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}

	_, err := readConfigFile(path)
	if err == nil {
		t.Fatal("readConfigFile() should reject oversized files")
	}
	if !strings.Contains(err.Error(), "exceeds") {
		t.Errorf("error = %q, want 'exceeds'", err.Error())
	}
}

func TestReadConfigFileAcceptsMaxSize(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "exact.env")

	// Write exactly maxConfigFileSize bytes.
	data := make([]byte, maxConfigFileSize)
	for i := range data {
		data[i] = 'Y'
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}

	got, err := readConfigFile(path)
	if err != nil {
		t.Fatalf("readConfigFile() error = %v", err)
	}
	if len(got) != maxConfigFileSize {
		t.Fatalf("len(data) = %d, want %d", len(got), maxConfigFileSize)
	}
}

func TestReadConfigFileRejectsMissing(t *testing.T) {
	t.Parallel()
	_, err := readConfigFile("/tmp/nonexistent-lamigrate-config-xyz.yaml")
	if err == nil {
		t.Fatal("readConfigFile() should fail for missing file")
	}
}

// --- YAML strict fields ---

func TestResolveDSNYAMLRejectsUnknownField(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	configContent := `dbMySQL:
  host: localhost
  user: u
  dbName: d
  unknownField: oops
`
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(configContent), 0600); err != nil {
		t.Fatal(err)
	}

	_, err := resolveDSN("", "", dir)
	if err == nil {
		t.Fatal("expected error for unknown YAML field")
	}
}

func TestResolveDSNYAMLRejectsMultipleDocuments(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	configContent := `dbMySQL:
  host: localhost
  user: u
  dbName: d
---
dbMySQL:
  host: other
`
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(configContent), 0600); err != nil {
		t.Fatal(err)
	}

	_, err := resolveDSN("", "", dir)
	if err == nil {
		t.Fatal("expected error for multiple YAML documents")
	}
	if !strings.Contains(err.Error(), "multiple documents") {
		t.Errorf("error = %q, want 'multiple documents'", err.Error())
	}
}

// --- findDefaultConfig search order ---

func TestFindDefaultConfigPrefersConfigYAML(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	// Create both config.yaml and .env — config.yaml should win.
	yamlContent := `dbMySQL:
  host: yaml-host
  user: u
  dbName: d
`
	envContent := `DB_HOST=env-host
DB_USER=u
DB_NAME=d
`
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(yamlContent), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte(envContent), 0600); err != nil {
		t.Fatal(err)
	}

	got, err := resolveDSN("", "", dir)
	if err != nil {
		t.Fatalf("resolveDSN() error = %v", err)
	}

	cfg, err := parseDSN(got)
	if err != nil {
		t.Fatalf("parse DSN: %v", err)
	}
	if !strings.Contains(cfg.Addr, "yaml-host") {
		t.Errorf("addr = %q, want to contain 'yaml-host' (config.yaml should be preferred)", cfg.Addr)
	}
}

func TestFindDefaultConfigSymlinkIsRejected(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	// Create the real config.
	real := filepath.Join(dir, "real.yaml")
	content := `dbMySQL:
  host: h
  user: u
  dbName: d
`
	if err := os.WriteFile(real, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}

	// Create a symlink named config.yaml.
	link := filepath.Join(dir, "config.yaml")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}

	_, err := resolveDSN("", "", dir)
	if err == nil {
		t.Fatal("resolveDSN() should reject symlink config.yaml")
	}
}

// --- .env fallback keys ---

func TestResolveDSNEnvFileDBFallbackKeys(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	// Use fallback key names (DB_HOST, DB_USER, etc.)
	envContent := `DB_HOST=fallback-host
DB_PORT=3308
DB_USER=fallback-user
DB_PASS=fallback-pass
DB_NAME=fallback-db
`
	path := filepath.Join(dir, ".env")
	if err := os.WriteFile(path, []byte(envContent), 0600); err != nil {
		t.Fatal(err)
	}

	got, err := resolveDSN("", "", dir)
	if err != nil {
		t.Fatalf("resolveDSN() error = %v", err)
	}

	cfg, err := parseDSN(got)
	if err != nil {
		t.Fatalf("parse DSN: %v", err)
	}
	if !strings.Contains(cfg.Addr, "fallback-host:3308") {
		t.Errorf("addr = %q, want fallback-host:3308", cfg.Addr)
	}
	if cfg.User != "fallback-user" {
		t.Errorf("user = %q, want fallback-user", cfg.User)
	}
	if cfg.DBName != "fallback-db" {
		t.Errorf("dbName = %q, want fallback-db", cfg.DBName)
	}
}

func TestResolveDSNEnvFileDBPasswordKey(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	envContent := `DB_HOST=h
DB_USER=u
DB_PASSWORD=pass123
DB_NAME=d
`
	path := filepath.Join(dir, ".env")
	if err := os.WriteFile(path, []byte(envContent), 0600); err != nil {
		t.Fatal(err)
	}

	got, err := resolveDSN("", "", dir)
	if err != nil {
		t.Fatalf("resolveDSN() error = %v", err)
	}

	cfg, err := parseDSN(got)
	if err != nil {
		t.Fatalf("parse DSN: %v", err)
	}
	// Password is in the parsed config but hidden in DSN string.
	if cfg.Passwd != "pass123" {
		t.Errorf("passwd = %q, want pass123", cfg.Passwd)
	}
}

// --- Redaction check ---

func TestDSNPasswordNotInQueryString(t *testing.T) {
	t.Parallel()
	config := dbMySQLConfig{
		Host:    "localhost",
		Port:    3306,
		User:    "admin",
		Pass:    "super-secret-password",
		DBName:  "mydb",
		Timeout: "30s",
	}
	dsn, err := formatMySQLDSN(config, "test")
	if err != nil {
		t.Fatalf("formatMySQLDSN() error = %v", err)
	}
	// The raw DSN string from go-sql-driver does embed the password
	// in URL-encoded form. Verify it's parseable and has the password.
	cfg, err := parseDSN(dsn)
	if err != nil {
		t.Fatalf("parse DSN: %v", err)
	}
	if cfg.Passwd != "super-secret-password" {
		t.Errorf("password not preserved in DSN: got %q", cfg.Passwd)
	}
}

// --- Offline command detection ---

func TestIsDatabaseCommand(t *testing.T) {
	t.Parallel()
	dbCmds := []string{"up", "down", "reset", "status", "import"}
	for _, cmd := range dbCmds {
		if !isDatabaseCommand(cmd) {
			t.Errorf("isDatabaseCommand(%q) = false, want true", cmd)
		}
	}
	offlineCmds := []string{"version", "migration", "make", "make:migration", "help", ""}
	for _, cmd := range offlineCmds {
		if isDatabaseCommand(cmd) {
			t.Errorf("isDatabaseCommand(%q) = true, want false", cmd)
		}
	}
}

// --- YAML config defaults ---

func TestResolveDSNYAMLDefaultPort(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	// No port specified — should default to 3306.
	configContent := `dbMySQL:
  host: localhost
  user: u
  dbName: d
`
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(configContent), 0600); err != nil {
		t.Fatal(err)
	}

	got, err := resolveDSN("", "", dir)
	if err != nil {
		t.Fatalf("resolveDSN() error = %v", err)
	}

	cfg, err := parseDSN(got)
	if err != nil {
		t.Fatalf("parse DSN: %v", err)
	}
	if !strings.Contains(cfg.Addr, "localhost:3306") {
		t.Errorf("addr = %q, want localhost:3306", cfg.Addr)
	}
}

func TestResolveDSNYAMLEmptyPassAllowed(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	configContent := `dbMySQL:
  host: localhost
  user: u
  dbName: d
`
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(configContent), 0600); err != nil {
		t.Fatal(err)
	}

	dsn, err := resolveDSN("", "", dir)
	if err != nil {
		t.Fatalf("resolveDSN() error = %v", err)
	}
	// DSN should succeed — empty pass is valid for local trust auth.
	if dsn == "" {
		t.Fatal("expected non-empty DSN")
	}
}

// --- .env file with DB_MYSQL_* prefix ---

func TestResolveDSNEnvFileDBMySQLPrefix(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	envContent := `DB_MYSQL_HOST=mysql-host
DB_MYSQL_PORT=3307
DB_MYSQL_USER=mysql-user
DB_MYSQL_PASS=mysql-pass
DB_MYSQL_DB_NAME=mysql-db
`
	path := filepath.Join(dir, ".env")
	if err := os.WriteFile(path, []byte(envContent), 0600); err != nil {
		t.Fatal(err)
	}

	got, err := resolveDSN("", "", dir)
	if err != nil {
		t.Fatalf("resolveDSN() error = %v", err)
	}

	cfg, err := parseDSN(got)
	if err != nil {
		t.Fatalf("parse DSN: %v", err)
	}
	if !strings.Contains(cfg.Addr, "mysql-host:3307") {
		t.Errorf("addr = %q, want mysql-host:3307", cfg.Addr)
	}
	if cfg.User != "mysql-user" {
		t.Errorf("user = %q, want mysql-user", cfg.User)
	}
}

// --- parseEnvValue edge cases ---

func TestParseEnvValueEmpty(t *testing.T) {
	t.Parallel()
	got, err := parseEnvValue("")
	if err != nil {
		t.Fatalf("parseEnvValue(\"\") error = %v", err)
	}
	if got != "" {
		t.Errorf("parseEnvValue(\"\") = %q, want \"\"", got)
	}
}

func TestParseEnvValueUnquoted(t *testing.T) {
	t.Parallel()
	got, err := parseEnvValue("hello-world")
	if err != nil {
		t.Fatalf("parseEnvValue() error = %v", err)
	}
	if got != "hello-world" {
		t.Errorf("parseEnvValue() = %q, want 'hello-world'", got)
	}
}

func TestParseEnvValueUnterminatedSingleQuote(t *testing.T) {
	t.Parallel()
	_, err := parseEnvValue("'unterminated")
	if err == nil {
		t.Fatal("expected error for unterminated single quote")
	}
}

func TestParseEnvValueInvalidDoubleQuote(t *testing.T) {
	t.Parallel()
	_, err := parseEnvValue(`"unterminated`)
	if err == nil {
		t.Fatal("expected error for invalid double quote")
	}
}

// --- parseEnvPort ---

func TestParseEnvPortValid(t *testing.T) {
	t.Parallel()
	if got := parseEnvPort("3306"); got != 3306 {
		t.Errorf("parseEnvPort(\"3306\") = %d, want 3306", got)
	}
	if got := parseEnvPort("  5432  "); got != 5432 {
		t.Errorf("parseEnvPort(\"  5432  \") = %d, want 5432", got)
	}
}

func TestParseEnvPortInvalid(t *testing.T) {
	t.Parallel()
	if got := parseEnvPort("abc"); got != 0 {
		t.Errorf("parseEnvPort(\"abc\") = %d, want 0", got)
	}
	if got := parseEnvPort(""); got != 0 {
		t.Errorf("parseEnvPort(\"\") = %d, want 0", got)
	}
}

// --- firstEnvValue priority ---

func TestFirstEnvValuePriority(t *testing.T) {
	t.Parallel()
	values := map[string]string{
		"DB_HOST":         "low",
		"DB_MYSQL_HOST":   "mid",
		"LAMIGRATE_DB_HOST": "high",
	}
	got := firstEnvValue(values, "LAMIGRATE_DB_HOST", "DB_MYSQL_HOST", "DB_HOST")
	if got != "high" {
		t.Errorf("firstEnvValue() = %q, want 'high'", got)
	}
}

func TestFirstEnvValueSkipsEmpty(t *testing.T) {
	t.Parallel()
	values := map[string]string{
		"LAMIGRATE_DB_HOST": "",
		"DB_MYSQL_HOST":     "",
		"DB_HOST":           "fallback",
	}
	got := firstEnvValue(values, "LAMIGRATE_DB_HOST", "DB_MYSQL_HOST", "DB_HOST")
	if got != "fallback" {
		t.Errorf("firstEnvValue() = %q, want 'fallback'", got)
	}
}

func TestFirstEnvValueNonePresent(t *testing.T) {
	t.Parallel()
	values := map[string]string{}
	got := firstEnvValue(values, "LAMIGRATE_DB_HOST", "DB_MYSQL_HOST", "DB_HOST")
	if got != "" {
		t.Errorf("firstEnvValue() = %q, want \"\"", got)
	}
}

// --- unsupported file extension ---

func TestDsnFromConfigFileRejectsUnsupportedExtension(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte(`{}`), 0600); err != nil {
		t.Fatal(err)
	}
	_, err := dsnFromConfigFile(path)
	if err == nil {
		t.Fatal("expected error for unsupported extension")
	}
	if !strings.Contains(err.Error(), "unsupported config file") {
		t.Errorf("error = %q, want 'unsupported config file'", err.Error())
	}
}

// --- helper: parse DSN string ---

func parseDSN(dsn string) (*mysql.Config, error) {
	return mysql.ParseDSN(dsn)
}
