package lamigrate

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// migrationFile represents one parsed migration file.
type migrationFile struct {
	Name      string // e.g. "20260730094235_create_users"
 Filename  string // e.g. "20260730094235_create_users.up.sql"
 Timestamp int64  // e.g. 20260730094235
	UpPath    string
	DownPath  string
}

var upPattern = regexp.MustCompile(`^(\d{14})_(.+)\.up\.sql$`)
var legacyPattern = regexp.MustCompile(`^(\d{6})_(.+)\.(up|down)\.sql$`)

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

		ts, _ := strconv.ParseInt(m[1], 10, 64)
		baseName := m[1] + "_" + m[2]

		if _, exists := seen[baseName]; !exists {
			seen[baseName] = &migrationFile{
				Name:      baseName,
				Filename:  name,
				Timestamp: ts,
				UpPath:    filepath.Join(dir, name),
				DownPath:  filepath.Join(dir, m[1]+"_"+m[2]+".down.sql"),
			}
		}
	}

	var files []migrationFile
	for _, f := range seen {
		files = append(files, *f)
	}
	sort.Slice(files, func(i, j int) bool {
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
		return files[i].Timestamp < files[j].Timestamp
	})
	return files, nil
}

// makeMigrationFiles creates a new migration pair with the current timestamp.
func makeMigrationFiles(dir, name string) (string, error) {
	name = strings.TrimSpace(name)
	name = strings.ReplaceAll(name, " ", "_")
	name = strings.ToLower(name)

	// Strip any extension the user might have added
	name = strings.TrimSuffix(name, ".sql")
	name = strings.TrimSuffix(name, ".up")
	name = strings.TrimSuffix(name, ".down")

	ts := time.Now().Format("20060102150405")
	base := ts + "_" + name

	upPath := filepath.Join(dir, base+".up.sql")
	downPath := filepath.Join(dir, base+".down.sql")

	if err := os.WriteFile(upPath, []byte("-- Migration: "+name+"\n--\n-- Write your up SQL here.\n\n"), 0644); err != nil {
		return "", fmt.Errorf("lamigrate: create %s: %w", upPath, err)
	}
	if err := os.WriteFile(downPath, []byte("-- Rollback: "+name+"\n--\n-- Write your down SQL here.\n\n"), 0644); err != nil {
		return "", fmt.Errorf("lamigrate: create %s: %w", downPath, err)
	}

	return base, nil
}

func readFile(path string) ([]byte, error) {
	return os.ReadFile(path)
}
