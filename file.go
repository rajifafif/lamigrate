package lamigrate

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"
)

const maxMigrationDescriptionLength = 200

// migrationFile represents one parsed migration file.
type migrationFile struct {
	Name         string // e.g. "20260730094235_create_users"
	Filename     string // e.g. "20260730094235_create_users.up.sql"
	Timestamp    int64  // e.g. 20260730094235
	UpPath       string
	DownPath     string
	UpChecksum   [32]byte
	DownChecksum [32]byte
}

// CreatedMigration describes a migration pair generated on disk.
type CreatedMigration struct {
	Name     string
	Base     string
	UpPath   string
	DownPath string
	Template string
}

var (
	upPattern             = regexp.MustCompile(`^(\d{14})_(.+)\.up\.sql$`)
	legacyPattern         = regexp.MustCompile(`^(\d{6})_(.+)\.(up|down)\.sql$`)
	validMigrationName    = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)
	validSQLIdentifier    = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)
	createTableName       = regexp.MustCompile(`^create_([a-z][a-z0-9_]*)_table$`)
	addColumnToTableName  = regexp.MustCompile(`^add_([a-z][a-z0-9_]*)_to_([a-z][a-z0-9_]*)_table$`)
	dropColumnFromTable   = regexp.MustCompile(`^drop_([a-z][a-z0-9_]*)_from_([a-z][a-z0-9_]*)_table$`)
	migrationNameReplacer = strings.NewReplacer("-", "_", " ", "_")
)

// scanMigrations finds all timestamp-based .up.sql files in dir, sorted by timestamp.
func scanMigrations(dir string) ([]migrationFile, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("lamigrate: read dir %s: %w", dir, err)
	}

	seen := make(map[string]*migrationFile)

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()

		// Only match new-style: 20260730094235_name.up.sql
		m := upPattern.FindStringSubmatch(name)
		if m == nil {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return nil, fmt.Errorf("lamigrate: inspect migration file %s: %w", filepath.Join(dir, name), err)
		}
		if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("lamigrate: migration file %s must be a regular file", filepath.Join(dir, name))
		}

		ts, _ := strconv.ParseInt(m[1], 10, 64)
		baseName := m[1] + "_" + m[2]
		downPath := filepath.Join(dir, baseName+".down.sql")
		downInfo, err := os.Lstat(downPath)
		if err != nil {
			return nil, fmt.Errorf("lamigrate: migration %s requires down file: %w", baseName, err)
		}
		if !downInfo.Mode().IsRegular() || downInfo.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("lamigrate: down file %s must be a regular file", downPath)
		}

		if _, exists := seen[baseName]; !exists {
			mf := &migrationFile{
				Name:      baseName,
				Filename:  name,
				Timestamp: ts,
				UpPath:    filepath.Join(dir, name),
				DownPath:  downPath,
			}
			// Compute checksums for both files in the pair.
			upCS, err := checksumFile(mf.UpPath)
			if err != nil {
				return nil, fmt.Errorf("lamigrate: checksum %s: %w", mf.UpPath, err)
			}
			downCS, err := checksumFile(downPath)
			if err != nil {
				return nil, fmt.Errorf("lamigrate: checksum %s: %w", downPath, err)
			}
			mf.UpChecksum = upCS
			mf.DownChecksum = downCS
			seen[baseName] = mf
		}
	}

	var files []migrationFile
	for _, f := range seen {
		files = append(files, *f)
	}
	sort.Slice(files, func(i, j int) bool {
		if files[i].Timestamp == files[j].Timestamp {
			return files[i].Name < files[j].Name
		}
		return files[i].Timestamp < files[j].Timestamp
	})
	return files, nil
}

// scanLegacyMigrations finds numbered files (000001_name.up.sql) for import.
func scanLegacyMigrations(dir string) ([]migrationFile, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("lamigrate: read dir %s: %w", dir, err)
	}

	seen := make(map[string]*migrationFile)

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()

		m := legacyPattern.FindStringSubmatch(name)
		if m == nil {
			continue
		}
		// Skip if this is also a new-style timestamp (14 digits)
		if len(m[1]) == 14 {
			continue
		}

		num, _ := strconv.Atoi(m[1])
		baseName := m[1] + "_" + m[2]
		ext := m[3]

		f, exists := seen[baseName]
		if !exists {
			f = &migrationFile{
				Name:      baseName,
				Filename:  name,
				Timestamp: int64(num), // use sequence number as sort key
			}
			seen[baseName] = f
		}
		if ext == "up" {
			f.UpPath = filepath.Join(dir, name)
		} else {
			f.DownPath = filepath.Join(dir, name)
		}
	}

	var files []migrationFile
	for _, f := range seen {
		files = append(files, *f)
	}
	sort.Slice(files, func(i, j int) bool {
		if files[i].Timestamp == files[j].Timestamp {
			return files[i].Name < files[j].Name
		}
		return files[i].Timestamp < files[j].Timestamp
	})
	return files, nil
}

// CreateMigration creates a Laravel-like timestamped .up.sql/.down.sql pair.
// It is an offline operation and never opens a database connection.
func CreateMigration(dir, name string) (CreatedMigration, error) {
	return createMigrationAt(dir, name, time.Now().UTC())
}

// makeMigrationFiles preserves the original internal API used by Migrate.Make.
func makeMigrationFiles(dir, name string) (string, error) {
	created, err := CreateMigration(dir, name)
	if err != nil {
		return "", err
	}
	return created.Base, nil
}

func createMigrationAt(dir, name string, now time.Time) (CreatedMigration, error) {
	name, err := normalizeMigrationName(name)
	if err != nil {
		return CreatedMigration{}, err
	}
	if err := ensureMigrationDirectory(dir); err != nil {
		return CreatedMigration{}, err
	}

	timestamp := now.UTC().Format("20060102150405")
	if err := ensureTimestampAvailable(dir, timestamp); err != nil {
		return CreatedMigration{}, err
	}

	lockPath := filepath.Join(dir, ".lamigrate-create-"+timestamp+".lock")
	lock, err := os.OpenFile(lockPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return CreatedMigration{}, fmt.Errorf("lamigrate: timestamp %s is being used; retry in one second", timestamp)
		}
		return CreatedMigration{}, fmt.Errorf("lamigrate: create migration lock: %w", err)
	}
	if err := lock.Close(); err != nil {
		_ = os.Remove(lockPath)
		return CreatedMigration{}, fmt.Errorf("lamigrate: close migration lock: %w", err)
	}
	defer os.Remove(lockPath)

	// Recheck under the creation lock so concurrent creators cannot share a timestamp.
	if err := ensureTimestampAvailable(dir, timestamp); err != nil {
		return CreatedMigration{}, err
	}

	base := timestamp + "_" + name
	upPath := filepath.Join(dir, base+".up.sql")
	downPath := filepath.Join(dir, base+".down.sql")
	template, upSQL, downSQL := migrationTemplates(name)

	if err := publishMigrationPair(dir, base, upPath, downPath, []byte(upSQL), []byte(downSQL)); err != nil {
		return CreatedMigration{}, err
	}

	return CreatedMigration{
		Name:     name,
		Base:     base,
		UpPath:   upPath,
		DownPath: downPath,
		Template: template,
	}, nil
}

func normalizeMigrationName(name string) (string, error) {
	name = strings.TrimSpace(strings.ToLower(name))
	name = strings.TrimSuffix(name, ".sql")
	name = strings.TrimSuffix(name, ".up")
	name = strings.TrimSuffix(name, ".down")
	name = migrationNameReplacer.Replace(name)
	for strings.Contains(name, "__") {
		name = strings.ReplaceAll(name, "__", "_")
	}

	if name == "" {
		return "", fmt.Errorf("lamigrate: migration name is required")
	}
	if len(name) > maxMigrationDescriptionLength {
		return "", fmt.Errorf("lamigrate: migration name exceeds %d characters", maxMigrationDescriptionLength)
	}
	if !validMigrationName.MatchString(name) {
		return "", fmt.Errorf("lamigrate: invalid migration name %q; use lowercase letters, digits, and underscores", name)
	}
	return name, nil
}

func ensureMigrationDirectory(dir string) error {
	if strings.TrimSpace(dir) == "" {
		return fmt.Errorf("lamigrate: migrations directory is required")
	}
	if err := rejectSymlinkComponents(dir); err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("lamigrate: create migrations directory %s: %w", dir, err)
	}
	if err := rejectSymlinkComponents(dir); err != nil {
		return err
	}
	info, err := os.Lstat(dir)
	if err != nil {
		return fmt.Errorf("lamigrate: inspect migrations directory %s: %w", dir, err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return fmt.Errorf("lamigrate: migrations path %s must be a real directory", dir)
	}
	return nil
}

func rejectSymlinkComponents(path string) error {
	abs, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("lamigrate: resolve migrations path %s: %w", path, err)
	}
	volume := filepath.VolumeName(abs)
	current := volume + string(os.PathSeparator)
	remaining := strings.TrimPrefix(abs, current)
	for _, component := range strings.Split(remaining, string(os.PathSeparator)) {
		if component == "" {
			continue
		}
		current = filepath.Join(current, component)
		info, err := os.Lstat(current)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return fmt.Errorf("lamigrate: inspect path component %s: %w", current, err)
		}
		if info.Mode()&os.ModeSymlink != 0 && !trustedSystemSymlink(current) {
			return fmt.Errorf("lamigrate: migrations path must not contain symlink %s", current)
		}
	}
	return nil
}

func trustedSystemSymlink(path string) bool {
	if runtime.GOOS != "darwin" {
		return false
	}
	// macOS exposes these root-managed compatibility aliases by default.
	return path == "/var" || path == "/tmp"
}

func ensureTimestampAvailable(dir, timestamp string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("lamigrate: read migrations directory %s: %w", dir, err)
	}
	prefix := timestamp + "_"
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), prefix) && strings.HasSuffix(entry.Name(), ".sql") {
			return fmt.Errorf("lamigrate: timestamp %s already has a migration; retry in one second", timestamp)
		}
	}
	return nil
}

// publishMigrationPair stages both files invisibly, publishes the down file
// first, and publishes the discoverable up file last. A crash can therefore
// leave at most a harmless down-only orphan, never a runnable up-only migration.
func publishMigrationPair(dir, base, upPath, downPath string, upData, downData []byte) (err error) {
	upTemp, err := writeTempFile(dir, ".lamigrate-"+base+"-up-*", upData)
	if err != nil {
		return fmt.Errorf("lamigrate: stage %s: %w", upPath, err)
	}
	defer os.Remove(upTemp)

	downTemp, err := writeTempFile(dir, ".lamigrate-"+base+"-down-*", downData)
	if err != nil {
		return fmt.Errorf("lamigrate: stage %s: %w", downPath, err)
	}
	defer os.Remove(downTemp)

	if err = os.Link(downTemp, downPath); err != nil {
		return fmt.Errorf("lamigrate: publish %s: %w", downPath, err)
	}
	if err = syncDirectory(dir); err != nil {
		// Keep the down-only file on uncertainty. Discovery ignores it, and
		// removing it without a confirmed directory sync could be less safe.
		return fmt.Errorf("lamigrate: sync published down file: %w", err)
	}

	if err = os.Link(upTemp, upPath); err != nil {
		// The synced down-only file is deliberately retained.
		return fmt.Errorf("lamigrate: publish %s: %w", upPath, err)
	}
	if err = syncDirectory(dir); err != nil {
		// Never remove the durable down file. Best-effort removal and sync of
		// only the up link leaves either a complete pair or a down-only orphan.
		if removeErr := os.Remove(upPath); removeErr == nil {
			_ = syncDirectory(dir)
		}
		return fmt.Errorf("lamigrate: sync published migration pair: %w", err)
	}
	return nil
}

func writeTempFile(dir, pattern string, data []byte) (path string, err error) {
	file, err := os.CreateTemp(dir, pattern)
	if err != nil {
		return "", err
	}
	path = file.Name()
	defer func() {
		if err != nil {
			_ = file.Close()
			_ = os.Remove(path)
		}
	}()

	if err = file.Chmod(0o644); err != nil {
		return "", err
	}
	if _, err = file.Write(data); err != nil {
		return "", err
	}
	if err = file.Sync(); err != nil {
		return "", err
	}
	if err = file.Close(); err != nil {
		return "", err
	}
	return path, nil
}

func syncDirectory(dir string) error {
	handle, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer handle.Close()
	return handle.Sync()
}

func migrationTemplates(name string) (template, upSQL, downSQL string) {
	if match := createTableName.FindStringSubmatch(name); match != nil && validIdentifier(match[1]) {
		table := quoteIdentifier(match[1])
		return "create_table",
			fmt.Sprintf("-- Migration: %s\n\nCREATE TABLE %s (\n    `id` BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,\n    `created_at` TIMESTAMP NULL DEFAULT NULL,\n    `updated_at` TIMESTAMP NULL DEFAULT NULL,\n    PRIMARY KEY (`id`)\n) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;\n", name, table),
			fmt.Sprintf("-- Rollback: %s\n\nDROP TABLE IF EXISTS %s;\n", name, table)
	}

	if match := addColumnToTableName.FindStringSubmatch(name); match != nil && validIdentifier(match[1]) && validIdentifier(match[2]) {
		column := quoteIdentifier(match[1])
		table := quoteIdentifier(match[2])
		return "add_column",
			guardedTemplate(name, "up", fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s VARCHAR(255) NULL;", table, column)),
			guardedTemplate(name, "down", fmt.Sprintf("ALTER TABLE %s DROP COLUMN %s;", table, column))
	}

	if match := dropColumnFromTable.FindStringSubmatch(name); match != nil && validIdentifier(match[1]) && validIdentifier(match[2]) {
		column := quoteIdentifier(match[1])
		table := quoteIdentifier(match[2])
		return "drop_column",
			guardedTemplate(name, "up", fmt.Sprintf("ALTER TABLE %s DROP COLUMN %s;", table, column)),
			guardedTemplate(name, "down", fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s VARCHAR(255) NULL;", table, column))
	}

	return "generic",
		guardedTemplate(name, "up", "Write the forward SQL for this migration."),
		guardedTemplate(name, "down", "Write the rollback SQL for this migration.")
}

func guardedTemplate(name, direction, suggestion string) string {
	heading := "Migration"
	if direction == "down" {
		heading = "Rollback"
	}
	return fmt.Sprintf("-- %s: %s\n--\n-- TODO: Review the suggested SQL, choose exact types/options, then remove SIGNAL.\nSIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'lamigrate: unfinished %s migration %s';\n\n-- Suggested SQL:\n-- %s\n", heading, name, direction, name, suggestion)
}

func validIdentifier(identifier string) bool {
	return len(identifier) <= 64 && validSQLIdentifier.MatchString(identifier)
}

func quoteIdentifier(identifier string) string {
	return "`" + identifier + "`"
}

func readFile(path string) ([]byte, error) {
	return os.ReadFile(path)
}

// fileStat returns the size in bytes of the file at path.
func fileStat(path string) (int64, error) {
	info, err := os.Stat(path)
	if err != nil {
		return 0, err
	}
	return info.Size(), nil
}
