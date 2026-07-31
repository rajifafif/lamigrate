//go:build integration

package lamigrate

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math/rand"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/go-sql-driver/mysql"
)

// testDSN returns the root DSN from LAMIGRATE_TEST_MYSQL_DSN or the default.
// It connects to the server without selecting a database.
func testDSN() string {
	dsn := os.Getenv("LAMIGRATE_TEST_MYSQL_DSN")
	if dsn == "" {
		dsn = "root:reviewpass@tcp(127.0.0.1:56198)/?multiStatements=true&parseTime=true"
	}
	// Strip database name so we connect to server root.
	idx := strings.Index(dsn, "/")
	if idx < 0 {
		return dsn + "/"
	}
	rest := dsn[idx+1:]
	qIdx := strings.Index(rest, "?")
	if qIdx < 0 {
		return dsn[:idx] + "/"
	}
	return dsn[:idx] + "/" + rest[qIdx:]
}

// testAdminConn opens an admin connection to the MySQL server (no database).
func testAdminConn(t *testing.T) *sql.DB {
	t.Helper()
	admin, err := sql.Open("mysql", testDSN())
	if err != nil {
		t.Fatalf("open admin connection: %v", err)
	}
	admin.SetMaxOpenConns(2)
	admin.SetConnMaxLifetime(30 * time.Second)
	if err := admin.Ping(); err != nil {
		admin.Close()
		t.Fatalf("ping MySQL server: %v", err)
	}
	return admin
}

// createTestDB creates a random test database and returns its DSN and name.
// Caller must DROP the database when done.
func createTestDB(t *testing.T, admin *sql.DB) (dbName, dbDSN string) {
	t.Helper()
	const prefix = "lamigrate_test_"
	const chars = "abcdefghijklmnopqrstuvwxyz0123456789"
	suffix := make([]byte, 12)
	for i := range suffix {
		suffix[i] = chars[rand.Intn(len(chars))]
	}
	dbName = prefix + string(suffix)

	if _, err := admin.Exec(fmt.Sprintf("CREATE DATABASE `%s`", dbName)); err != nil {
		t.Fatalf("create database %s: %v", dbName, err)
	}
	t.Cleanup(func() {
		admin.Exec(fmt.Sprintf("DROP DATABASE IF EXISTS `%s`", dbName))
	})

	// Build DSN: insert dbName before the ?
	serverDSN := testDSN()
	qIdx := strings.Index(serverDSN, "?")
	if qIdx >= 0 {
		dbDSN = serverDSN[:qIdx] + dbName + serverDSN[qIdx:]
	} else {
		dbDSN = serverDSN + dbName
	}
	return dbName, dbDSN
}

// newTestMigrator creates a Migrator pointing at the given database DSN.
func newTestMigrator(t *testing.T, dbDSN string) *Migrator {
	t.Helper()
	config, err := mysql.ParseDSN(dbDSN)
	if err != nil {
		t.Fatalf("parse DSN: %v", err)
	}
	m, err := NewMySQL(config, Options{Directory: t.TempDir()})
	if err != nil {
		t.Fatalf("NewMySQL: %v", err)
	}
	return m
}

// ---------- Session lifecycle tests ----------

func TestNewPrivateSessionCreatesConnection(t *testing.T) {
	t.Parallel()
	admin := testAdminConn(t)
	defer admin.Close()

	_, dbDSN := createTestDB(t, admin)
	m := newTestMigrator(t, dbDSN)
	ctx := context.Background()

	conn, pool, err := m.newPrivateSession(ctx)
	if err != nil {
		t.Fatalf("newPrivateSession: %v", err)
	}
	defer closeSession(conn, pool)

	// Verify the connection works by pinging through it.
	if err := conn.PingContext(ctx); err != nil {
		t.Fatalf("connection ping failed: %v", err)
	}

	// Verify pool settings: max open = 1, max idle = 1.
	if pool.Stats().MaxOpenConnections != 1 {
		t.Fatalf("MaxOpenConnections = %d, want 1", pool.Stats().MaxOpenConnections)
	}

	// Verify we got a real connection ID.
	var connID uint64
	if err := conn.QueryRowContext(ctx, "SELECT CONNECTION_ID()").Scan(&connID); err != nil {
		t.Fatalf("CONNECTION_ID: %v", err)
	}
	if connID == 0 {
		t.Fatal("CONNECTION_ID returned 0")
	}
	t.Logf("created private session with connection ID %d", connID)
}

func TestCloseSessionTerminates(t *testing.T) {
	t.Parallel()
	admin := testAdminConn(t)
	defer admin.Close()

	_, dbDSN := createTestDB(t, admin)
	m := newTestMigrator(t, dbDSN)
	ctx := context.Background()

	conn, pool, err := m.newPrivateSession(ctx)
	if err != nil {
		t.Fatalf("newPrivateSession: %v", err)
	}

	// Capture the connection ID before closing.
	var connID uint64
	if err := conn.QueryRowContext(ctx, "SELECT CONNECTION_ID()").Scan(&connID); err != nil {
		t.Fatalf("CONNECTION_ID: %v", err)
	}
	t.Logf("closing session with connection ID %d", connID)

	// Close the session.
	if err := closeSession(conn, pool); err != nil {
		t.Fatalf("closeSession: %v", err)
	}

	// After close, the connection should be unusable.
	if err := conn.PingContext(ctx); err == nil {
		t.Fatal("expected error pinging closed connection, got nil")
	}

	// After close, the pool should be unusable.
	if err := pool.Ping(); err == nil {
		t.Fatal("expected error pinging closed pool, got nil")
	}

	t.Log("session physically terminated after closeSession")
}

func TestNewPrivateSessionIsIndependent(t *testing.T) {
	t.Parallel()
	admin := testAdminConn(t)
	defer admin.Close()

	_, dbDSN := createTestDB(t, admin)
	m := newTestMigrator(t, dbDSN)
	ctx := context.Background()

	// Create two sessions and verify they have different connection IDs.
	conn1, pool1, err := m.newPrivateSession(ctx)
	if err != nil {
		t.Fatalf("newPrivateSession (1): %v", err)
	}
	defer closeSession(conn1, pool1)

	conn2, pool2, err := m.newPrivateSession(ctx)
	if err != nil {
		t.Fatalf("newPrivateSession (2): %v", err)
	}
	defer closeSession(conn2, pool2)

	var id1, id2 uint64
	if err := conn1.QueryRowContext(ctx, "SELECT CONNECTION_ID()").Scan(&id1); err != nil {
		t.Fatalf("CONNECTION_ID (1): %v", err)
	}
	if err := conn2.QueryRowContext(ctx, "SELECT CONNECTION_ID()").Scan(&id2); err != nil {
		t.Fatalf("CONNECTION_ID (2): %v", err)
	}

	if id1 == id2 {
		t.Fatalf("expected different connection IDs, both got %d", id1)
	}
	t.Logf("session 1 connID=%d, session 2 connID=%d — independent", id1, id2)
}

func TestNewPrivateSessionWithNilConfig(t *testing.T) {
	t.Parallel()
	m := &Migrator{config: nil}
	ctx := context.Background()

	conn, pool, err := m.newPrivateSession(ctx)
	if err == nil {
		closeSession(conn, pool)
		t.Fatal("expected error for nil config, got nil")
	}
	if !errors.Is(err, ErrUnsupportedDriver) {
		t.Fatalf("expected ErrUnsupportedDriver, got: %v", err)
	}
}

// ---------- Capability probe tests ----------

func TestCapabilityProbesPass(t *testing.T) {
	t.Parallel()
	admin := testAdminConn(t)
	defer admin.Close()

	_, dbDSN := createTestDB(t, admin)
	m := newTestMigrator(t, dbDSN)
	ctx := context.Background()

	conn, pool, err := m.newPrivateSession(ctx)
	if err != nil {
		t.Fatalf("newPrivateSession: %v", err)
	}
	defer closeSession(conn, pool)

	caps, err := m.runCapabilityProbes(ctx, conn)
	if err != nil {
		t.Fatalf("runCapabilityProbes: %v", err)
	}

	if caps.DatabaseName == "" {
		t.Fatal("DatabaseName is empty")
	}
	if caps.ConnectionID == 0 {
		t.Fatal("ConnectionID is 0")
	}

	// Validate the captured database name matches the one we connected to.
	if !strings.HasPrefix(caps.DatabaseName, "lamigrate_test_") {
		t.Fatalf("unexpected database name: %q", caps.DatabaseName)
	}

	t.Logf("capability probes passed: db=%q connID=%d", caps.DatabaseName, caps.ConnectionID)
}

func TestCapabilityProbesFailOnBadConfig(t *testing.T) {
	t.Parallel()

	// Use a config that points to a non-existent MySQL server.
	badCfg := &mysql.Config{
		User:   "nonexistent",
		Passwd: "nonexistent",
		Net:    "tcp",
		Addr:   "192.0.2.1:12345", // TEST-NET, guaranteed non-routable
		DBName: "nodb",
	}
	badCfg.MultiStatements = true
	badCfg.ParseTime = true

	m, err := NewMySQL(badCfg, Options{Directory: t.TempDir()})
	if err != nil {
		t.Fatalf("NewMySQL: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	conn, pool, err := m.newPrivateSession(ctx)
	if err != nil {
		// Connection itself failed — this is acceptable for bad config.
		t.Logf("newPrivateSession failed as expected for bad config: %v", err)
		return
	}
	defer closeSession(conn, pool)

	// If connection somehow succeeded, probes should fail.
	_, err = m.runCapabilityProbes(ctx, conn)
	if err == nil {
		t.Fatal("expected error from capability probes on bad config, got nil")
	}
	if !errors.Is(err, ErrUnsupportedDriver) {
		t.Fatalf("expected ErrUnsupportedDriver, got: %v", err)
	}
	t.Logf("capability probes correctly rejected bad config: %v", err)
}

// ---------- Config clone tests ----------

func TestCloneMySQLConfig(t *testing.T) {
	t.Parallel()

	cfg := &mysql.Config{
		User:             "testuser",
		Passwd:           "secret",
		Net:              "tcp",
		Addr:             "127.0.0.1:3306",
		DBName:           "mydb",
		Params:           map[string]string{"charset": "utf8mb4"},
		Loc:              time.UTC,
		MaxAllowedPacket: 4194304,
	}

	m, err := NewMySQL(cfg, Options{Directory: t.TempDir()})
	if err != nil {
		t.Fatalf("NewMySQL: %v", err)
	}

	// Mutate the original config after construction.
	cfg.User = "changed_user"
	cfg.Passwd = "changed_pass"
	cfg.DBName = "other_db"
	cfg.Params["charset"] = "latin1"
	cfg.Params["new_param"] = "added"

	// The migrator should still have a reference to the cloned config
	// that is independent from the mutated original.
	connector, err := mysql.NewConnector(m.config)
	if err != nil {
		t.Fatalf("NewConnector from stored config: %v", err)
	}
	_ = connector

	// Direct assertion: the stored config must not have been mutated.
	if m.config.User != "testuser" {
		t.Fatalf("stored config User = %q, want %q (original should be preserved)", m.config.User, "testuser")
	}
	if m.config.DBName != "mydb" {
		t.Fatalf("stored config DBName = %q, want %q (original should be preserved)", m.config.DBName, "mydb")
	}
	if _, ok := m.config.Params["new_param"]; ok {
		t.Fatal("stored config should not have 'new_param' from mutated original")
	}

	t.Log("config clone isolation verified — mutations to original do not affect stored config")
}

func TestCloneMySQLConfigRoundTrip(t *testing.T) {
	t.Parallel()

	cfg := &mysql.Config{
		User:             "cloneuser",
		Passwd:           "clonepass",
		Net:              "tcp",
		Addr:             "10.0.0.1:3306",
		DBName:           "clonedb",
		Params:           map[string]string{"charset": "utf8mb4", "collation": "utf8mb4_0900_ai_ci"},
		MultiStatements:  false,
		ParseTime:        false,
		Loc:              time.UTC,
		MaxAllowedPacket: 8388608,
	}

	m, err := NewMySQL(cfg, Options{Directory: t.TempDir()})
	if err != nil {
		t.Fatalf("NewMySQL: %v", err)
	}

	// After NewMySQL, the stored config must have MultiStatements and ParseTime
	// forced to true (per architecture §7).
	if !m.config.MultiStatements {
		t.Fatal("stored config MultiStatements should be true (enforced by NewMySQL)")
	}
	if !m.config.ParseTime {
		t.Fatal("stored config ParseTime should be true (enforced by NewMySQL)")
	}

	// The original should not be mutated.
	if cfg.MultiStatements {
		t.Fatal("original config should NOT have MultiStatements=true (NewMySQL should not mutate original)")
	}
	if cfg.ParseTime {
		t.Fatal("original config should NOT have ParseTime=true (NewMySQL should not mutate original)")
	}

	t.Log("config round-trip verified: forced settings applied to clone, original unchanged")
}

func TestCloseSessionNilSafe(t *testing.T) {
	t.Parallel()
	// closeSession should handle nil arguments without panicking.
	if err := closeSession(nil, nil); err != nil {
		t.Fatalf("closeSession(nil, nil) error = %v", err)
	}
	t.Log("closeSession is nil-safe")
}
