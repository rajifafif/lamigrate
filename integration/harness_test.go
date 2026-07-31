//go:build integration

package integration

import (
	"database/sql"
	"fmt"
	"math/rand"
	"os"
	"strings"
	"testing"
	"time"

	_ "github.com/go-sql-driver/mysql"
)

const (
	// testDBPrefix is the required prefix for all test databases.
	// The harness refuses to operate on any database without this prefix.
	testDBPrefix = "lamigrate_test_"

	// defaultDSN is used when LAMIGRATE_TEST_MYSQL_DSN is not set.
	defaultDSN = "root:reviewpass@tcp(127.0.0.1:56198)/?multiStatements=true&parseTime=true"
)

// TestDB represents an isolated test database with lifecycle helpers.
type TestDB struct {
	t     *testing.T
	db    *sql.DB
	DSN   string // full DSN pointing at the test database
	Name  string // database name
	admin *sql.DB // connection to server root (no database selected)
}

// testEnvDSN returns the root DSN from LAMIGRATE_TEST_MYSQL_DSN or the default.
// The returned DSN connects to the server without selecting a database so we
// can CREATE DATABASE / DROP DATABASE freely.
func testEnvDSN() string {
	dsn := os.Getenv("LAMIGRATE_TEST_MYSQL_DSN")
	if dsn == "" {
		dsn = defaultDSN
	}
	// Strip any dbname from the DSN so we connect to the server root.
	dsn = stripDBName(dsn)
	return dsn
}

// stripDBName removes the database name from a MySQL DSN but keeps the slash
// separator so the driver doesn't reject it: /dbname? becomes /?.
// Handles both /dbname and /dbname?params forms.
func stripDBName(dsn string) string {
	idx := strings.Index(dsn, "/")
	if idx < 0 {
		return dsn + "/"
	}
	rest := dsn[idx+1:]
	qIdx := strings.Index(rest, "?")
	if qIdx < 0 {
		// /dbname only — replace with /
		return dsn[:idx] + "/"
	}
	// /dbname?params → /?params
	return dsn[:idx] + "/" + rest[qIdx:]
}

// newTestDB creates an isolated test database with a random name and returns
// a TestDB handle. The caller MUST defer tb.Close() to clean up.
func newTestDB(t *testing.T) *TestDB {
	t.Helper()

	dsn := testEnvDSN()

	admin, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatalf("harness: open admin connection: %v", err)
	}
	admin.SetMaxOpenConns(2)
	admin.SetConnMaxLifetime(30 * time.Second)

	if err := admin.Ping(); err != nil {
		admin.Close()
		t.Fatalf("harness: ping MySQL server: %v", err)
	}

	dbName := testDBPrefix + randomSuffix(12)

	// Create the isolated database
	if _, err := admin.Exec(fmt.Sprintf("CREATE DATABASE `%s`", dbName)); err != nil {
		admin.Close()
		t.Fatalf("harness: create database %s: %v", dbName, err)
	}

	// Build DSN pointing at the new database
	dbDSN := buildDSN(dsn, dbName)

	db, err := sql.Open("mysql", dbDSN)
	if err != nil {
		admin.Exec(fmt.Sprintf("DROP DATABASE IF EXISTS `%s`", dbName))
		admin.Close()
		t.Fatalf("harness: open test database %s: %v", dbName, err)
	}
	db.SetMaxOpenConns(5)
	db.SetConnMaxLifetime(30 * time.Second)

	if err := db.Ping(); err != nil {
		db.Close()
		admin.Exec(fmt.Sprintf("DROP DATABASE IF EXISTS `%s`", dbName))
		admin.Close()
		t.Fatalf("harness: ping test database %s: %v", dbName, err)
	}

	t.Logf("harness: created test database %s", dbName)

	tb := &TestDB{
		t:     t,
		db:    db,
		DSN:   dbDSN,
		Name:  dbName,
		admin: admin,
	}

	t.Cleanup(func() { tb.Close() })
	return tb
}

// Close drops the test database and closes all connections.
// Safe to call multiple times; idempotent.
func (tb *TestDB) Close() {
	if tb == nil {
		return
	}
	if tb.db != nil {
		tb.db.Close()
		tb.db = nil
	}
	if tb.admin != nil && tb.Name != "" {
		// Ensure no lingering connections before DROP
		tb.admin.Exec(fmt.Sprintf("DROP DATABASE IF EXISTS `%s`", tb.Name))
		tb.admin.Close()
		tb.admin = nil
		tb.t.Logf("harness: dropped test database %s", tb.Name)
	}
}

// DB returns the raw *sql.DB for the isolated test database.
func (tb *TestDB) DB() *sql.DB {
	return tb.db
}

// Exec runs a SQL statement against the test database.
func (tb *TestDB) Exec(query string, args ...any) (sql.Result, error) {
	return tb.db.Exec(query, args...)
}

// QueryRow runs a query and returns a single row.
func (tb *TestDB) QueryRow(query string, args ...any) *sql.Row {
	return tb.db.QueryRow(query, args...)
}

// requireTestDBName asserts the connected database name starts with the
// test prefix. Used as a safety guard inside harness tests.
func (tb *TestDB) requireTestDBName() {
	tb.t.Helper()
	var currentDB string
	if err := tb.db.QueryRow("SELECT DATABASE()").Scan(&currentDB); err != nil {
		tb.t.Fatalf("harness: SELECT DATABASE() failed: %v", err)
	}
	if !strings.HasPrefix(currentDB, testDBPrefix) {
		tb.t.Fatalf("harness: REFUSED — database %q does not have the required %q prefix", currentDB, testDBPrefix)
	}
}

// buildDSN takes a server-level DSN (ends with /? or /) and inserts the
// database name between the slash and the query string.
func buildDSN(serverDSN, dbName string) string {
	qIdx := strings.Index(serverDSN, "?")
	slashIdx := strings.LastIndex(serverDSN, "/")
	if qIdx < 0 {
		// No query params: just append /dbname
		if slashIdx >= 0 {
			return serverDSN[:slashIdx+1] + dbName
		}
		return serverDSN + "/" + dbName
	}
	// serverDSN looks like root:pass@tcp(host:port)/?params
	// Insert dbName before the ?
	return serverDSN[:qIdx] + dbName + serverDSN[qIdx:]
}

// randomSuffix returns a random lowercase hex string of the given length.
func randomSuffix(n int) string {
	const chars = "abcdefghijklmnopqrstuvwxyz0123456789"
	b := make([]byte, n)
	for i := range b {
		b[i] = chars[rand.Intn(len(chars))]
	}
	return string(b)
}
