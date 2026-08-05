// Package lamigrate provides Laravel-style database migrations for Go + MySQL.
//
// File naming:  YYYYMMDDHHMMSS_description.up.sql / .down.sql
// Tracking:     migrations table with batch numbers (exactly like Laravel)
// Commands:     up, down, reset, status, migration create, import
// Flag:         -pretend / --pretend shows SQL without executing
//
// Usage as library:
//
//	m, err := lamigrate.New("sql/migrations", dsn)
//	m.Up(ctx)              // apply all pending
//	m.Up(ctx, 3)           // apply next 3
//	m.Down(ctx, lamigrate.DownAll())  // rollback last batch
//	m.Down(ctx, lamigrate.DownSteps(2))  // rollback last 2
//	m.Reset(ctx)           // rollback everything
//	m.Status(ctx)          // []MigrationStatus
//	lamigrate.CreateMigration("sql/migrations", "create_users_table")
//	m.ImportLegacy(ctx)    // seed 000001-style files as applied
package lamigrate

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	_ "github.com/go-sql-driver/mysql"
)

// MigrationStatus represents one migration's state for status display.
type MigrationStatus struct {
	Name      string
	Filename  string
	Applied   bool
	Batch     int
	AppliedAt string
}

// Migrate is the main entry point.
type Migrate struct {
	db        *sql.DB
	dir       string
	tableName string
}

// New creates a Migrate instance.
// dir is the path to the migrations directory.
// dsn is the MySQL DSN, e.g. "user:pass@tcp(host:3306)/dbname?multiStatements=true".
func New(dir, dsn string) (*Migrate, error) {
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return nil, fmt.Errorf("lamigrate: open db: %w", err)
	}
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("lamigrate: ping db: %w", err)
	}
	m := &Migrate{
		db:        db,
		dir:       dir,
		tableName: "migrations",
	}
	if err := m.ensureTable(context.Background()); err != nil {
		db.Close()
		return nil, err
	}
	return m, nil
}

// Close closes the underlying database connection.
func (m *Migrate) Close() error {
	return m.db.Close()
}

// Table sets the migrations table name. Default is "migrations".
func (m *Migrate) Table(name string) *Migrate {
	m.tableName = name
	return m
}

// Up applies pending migrations.
// If n > 0, apply at most n migrations. Otherwise apply all.
func (m *Migrate) Up(ctx context.Context, n ...int) error {
	pending, err := m.pending(ctx)
	if err != nil {
		return err
	}
	if len(pending) == 0 {
		fmt.Println("Nothing to migrate.")
		return nil
	}
	limit := len(pending)
	if len(n) > 0 && n[0] > 0 && n[0] < limit {
		limit = n[0]
	}
	toApply := pending[:limit]

	batch, err := m.nextBatch(ctx)
	if err != nil {
		return err
	}

	applied := 0
	for _, f := range toApply {
		sql, err := readSQL(f.UpPath)
		if err != nil {
			return fmt.Errorf("lamigrate: read %s: %w", f.UpPath, err)
		}
		if _, err := m.db.ExecContext(ctx, sql); err != nil {
			return fmt.Errorf("lamigrate: execute %s: %w", f.Filename, err)
		}
		if err := m.recordMigration(ctx, f.Name, batch); err != nil {
			return err
		}
		fmt.Printf("Migrated:  %s\n", f.Filename)
		applied++
	}
	fmt.Printf("Migrated %d migration(s) (batch %d).\n", applied, batch)
	return nil
}

// Down rolls back migrations from the last batch.
// If n > 0, rollback at most n migrations. Otherwise rollback all in last batch.
func (m *Migrate) Down(ctx context.Context, n ...int) error {
	applied, err := m.appliedInLastBatch(ctx)
	if err != nil {
		return err
	}
	if len(applied) == 0 {
		fmt.Println("Nothing to rollback.")
		return nil
	}
	limit := len(applied)
	if len(n) > 0 && n[0] > 0 && n[0] < limit {
		limit = n[0]
	}
	toRollback := applied[:limit]

	rolled := 0
	for _, name := range toRollback {
		f, err := m.findDownFile(name)
		if err != nil {
			return fmt.Errorf("lamigrate: find down file for %s: %w", name, err)
		}
		sql, err := readSQL(f)
		if err != nil {
			return fmt.Errorf("lamigrate: read %s: %w", f, err)
		}
		if _, err := m.db.ExecContext(ctx, sql); err != nil {
			return fmt.Errorf("lamigrate: execute rollback %s: %w", name, err)
		}
		if err := m.removeMigration(ctx, name); err != nil {
			return err
		}
		fmt.Printf("Rolled back: %s\n", name)
		rolled++
	}
	fmt.Printf("Rolled back %d migration(s).\n", rolled)
	return nil
}

// Reset rolls back all migrations.
func (m *Migrate) Reset(ctx context.Context) error {
	applied, err := m.allApplied(ctx)
	if err != nil {
		return err
	}
	if len(applied) == 0 {
		fmt.Println("Nothing to rollback.")
		return nil
	}

	// Reverse for rollback order
	for i, j := 0, len(applied)-1; i < j; i, j = i+1, j-1 {
		applied[i], applied[j] = applied[j], applied[i]
	}

	rolled := 0
	for _, name := range applied {
		f, err := m.findDownFile(name)
		if err != nil {
			return fmt.Errorf("lamigrate: find down file for %s: %w", name, err)
		}
		sql, err := readSQL(f)
		if err != nil {
			return fmt.Errorf("lamigrate: read %s: %w", f, err)
		}
		if _, err := m.db.ExecContext(ctx, sql); err != nil {
			return fmt.Errorf("lamigrate: execute rollback %s: %w", name, err)
		}
		if err := m.removeMigration(ctx, name); err != nil {
			return err
		}
		fmt.Printf("Rolled back: %s\n", name)
		rolled++
	}
	fmt.Printf("Rolled back %d migration(s).\n", rolled)
	return nil
}

// Status returns the list of migrations with their applied state.
func (m *Migrate) Status(ctx context.Context) ([]MigrationStatus, error) {
	allFiles, err := scanMigrations(m.dir)
	if err != nil {
		return nil, err
	}
	applied, err := m.allAppliedMap(ctx)
	if err != nil {
		return nil, err
	}

	var result []MigrationStatus
	for _, f := range allFiles {
		ms := MigrationStatus{
			Name:     f.Name,
			Filename: f.Filename,
		}
		if info, ok := applied[f.Name]; ok {
			ms.Applied = true
			ms.Batch = info.Batch
			ms.AppliedAt = info.AppliedAt
		}
		result = append(result, ms)
	}
	return result, nil
}

// Make creates a new migration file pair with the current timestamp.
// Returns the base path (without .up.sql / .down.sql).
func (m *Migrate) Make(name string) (string, error) {
	return makeMigrationFiles(m.dir, name)
}

// ImportLegacy reads numbered migration files (000001_name.sql) and marks
// them as already applied in batch 0. Does NOT execute any SQL.
// Use this once when migrating from golang-migrate.
func (m *Migrate) ImportLegacy(ctx context.Context) error {
	return m.importLegacy(ctx)
}

// PretendUp prints the SQL that Up would execute, without running it.
func (m *Migrate) PretendUp(ctx context.Context, n ...int) error {
	pending, err := m.pending(ctx)
	if err != nil {
		return err
	}
	if len(pending) == 0 {
		fmt.Println("Nothing to migrate.")
		return nil
	}
	limit := len(pending)
	if len(n) > 0 && n[0] > 0 && n[0] < limit {
		limit = n[0]
	}
	toApply := pending[:limit]

	for _, f := range toApply {
		sql, err := readSQL(f.UpPath)
		if err != nil {
			return fmt.Errorf("lamigrate: read %s: %w", f.UpPath, err)
		}
		fmt.Printf("-- Migration: %s\n", f.Filename)
		fmt.Println(sql)
		fmt.Println("-- done")
		fmt.Println()
	}
	fmt.Printf("Pretend: %d migration(s) would be applied.\n", len(toApply))
	return nil
}

// PretendDown prints the SQL that Down would execute, without running it.
func (m *Migrate) PretendDown(ctx context.Context, n ...int) error {
	applied, err := m.appliedInLastBatch(ctx)
	if err != nil {
		return err
	}
	if len(applied) == 0 {
		fmt.Println("Nothing to rollback.")
		return nil
	}
	limit := len(applied)
	if len(n) > 0 && n[0] > 0 && n[0] < limit {
		limit = n[0]
	}
	toRollback := applied[:limit]

	for _, name := range toRollback {
		f, err := m.findDownFile(name)
		if err != nil {
			return fmt.Errorf("lamigrate: find down file for %s: %w", name, err)
		}
		sql, err := readSQL(f)
		if err != nil {
			return fmt.Errorf("lamigrate: read %s: %w", f, err)
		}
		fmt.Printf("-- Rollback: %s\n", name)
		fmt.Println(sql)
		fmt.Println("-- done")
		fmt.Println()
	}
	fmt.Printf("Pretend: %d migration(s) would be rolled back.\n", len(toRollback))
	return nil
}

// --- internal helpers ---

func (m *Migrate) pending(ctx context.Context) ([]migrationFile, error) {
	allFiles, err := scanMigrations(m.dir)
	if err != nil {
		return nil, err
	}
	applied, err := m.allAppliedSet(ctx)
	if err != nil {
		return nil, err
	}
	var pending []migrationFile
	for _, f := range allFiles {
		if !applied[f.Name] {
			pending = append(pending, f)
		}
	}
	return pending, nil
}

func (m *Migrate) appliedInLastBatch(ctx context.Context) ([]string, error) {
	query := fmt.Sprintf(
		"SELECT migration FROM %s WHERE batch = (SELECT COALESCE(MAX(batch), 0) FROM %s WHERE batch > 0) AND batch > 0 ORDER BY id DESC",
		m.tableName, m.tableName,
	)
	rows, err := m.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("lamigrate: query last batch: %w", err)
	}
	defer rows.Close()

	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		names = append(names, name)
	}
	return names, rows.Err()
}

func (m *Migrate) allApplied(ctx context.Context) ([]string, error) {
	query := fmt.Sprintf("SELECT migration FROM %s ORDER BY id ASC", m.tableName)
	rows, err := m.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("lamigrate: query all applied: %w", err)
	}
	defer rows.Close()

	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		names = append(names, name)
	}
	return names, rows.Err()
}

type appliedInfo struct {
	Batch     int
	AppliedAt string
}

func (m *Migrate) allAppliedMap(ctx context.Context) (map[string]appliedInfo, error) {
	query := fmt.Sprintf("SELECT migration, batch, applied_at FROM %s ORDER BY id ASC", m.tableName)
	rows, err := m.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("lamigrate: query applied: %w", err)
	}
	defer rows.Close()

	result := make(map[string]appliedInfo)
	for rows.Next() {
		var name, appliedAt string
		var batch int
		if err := rows.Scan(&name, &batch, &appliedAt); err != nil {
			return nil, err
		}
		result[name] = appliedInfo{Batch: batch, AppliedAt: appliedAt}
	}
	return result, rows.Err()
}

func (m *Migrate) allAppliedSet(ctx context.Context) (map[string]bool, error) {
	query := fmt.Sprintf("SELECT migration FROM %s", m.tableName)
	rows, err := m.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("lamigrate: query applied set: %w", err)
	}
	defer rows.Close()

	result := make(map[string]bool)
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		result[name] = true
	}
	return result, rows.Err()
}

func (m *Migrate) nextBatch(ctx context.Context) (int, error) {
	query := fmt.Sprintf("SELECT COALESCE(MAX(batch), 0) + 1 FROM %s", m.tableName)
	var batch int
	if err := m.db.QueryRowContext(ctx, query).Scan(&batch); err != nil {
		return 0, fmt.Errorf("lamigrate: get next batch: %w", err)
	}
	return batch, nil
}

func (m *Migrate) recordMigration(ctx context.Context, name string, batch int) error {
	query := fmt.Sprintf("INSERT INTO %s (migration, batch) VALUES (?, ?)", m.tableName)
	if _, err := m.db.ExecContext(ctx, query, name, batch); err != nil {
		return fmt.Errorf("lamigrate: record migration %s: %w", name, err)
	}
	return nil
}

func (m *Migrate) removeMigration(ctx context.Context, name string) error {
	query := fmt.Sprintf("DELETE FROM %s WHERE migration = ?", m.tableName)
	if _, err := m.db.ExecContext(ctx, query, name); err != nil {
		return fmt.Errorf("lamigrate: remove migration %s: %w", name, err)
	}
	return nil
}

func (m *Migrate) findDownFile(name string) (string, error) {
	// Check timestamped files first
	files, err := scanMigrations(m.dir)
	if err != nil {
		return "", err
	}
	for _, f := range files {
		if f.Name == name {
			return f.DownPath, nil
		}
	}
	// Fall back to legacy numbered files
	legacy, err := scanLegacyMigrations(m.dir)
	if err != nil {
		return "", err
	}
	for _, f := range legacy {
		if f.Name == name && f.DownPath != "" {
			return f.DownPath, nil
		}
	}
	return "", fmt.Errorf("down file not found for migration: %s", name)
}

func (m *Migrate) ensureTable(ctx context.Context) error {
	query := fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s (
		id         BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
		migration  VARCHAR(255) NOT NULL,
		batch      INT UNSIGNED NOT NULL,
		applied_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
		UNIQUE KEY uk_%s_migration (migration)
	) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_general_ci`, m.tableName, m.tableName)
	if _, err := m.db.ExecContext(ctx, query); err != nil {
		return fmt.Errorf("lamigrate: ensure migrations table: %w", err)
	}
	return nil
}

func readSQL(path string) (string, error) {
	data, err := readFile(path)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}