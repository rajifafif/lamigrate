package lamigrate

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// SeedRequest identifies SQL seed files to run. When Class is empty, every
// regular .sql file in Directory is run in lexicographic order. When Class is
// set, only <Class>.sql is run, matching Laravel's --class convention.
//
// Seed files are intentionally not tracked in migration metadata: like
// `php artisan db:seed`, each invocation executes the selected seeders. Seed
// SQL should therefore be idempotent when it may be run more than once.
type SeedRequest struct {
	Directory string
	Class     string
}

// SeedResult describes a completed seed operation.
type SeedResult struct {
	Command string
	Seeded  []SeederResult
	Errors  []SeederError
}

// SeederResult describes one SQL seed file that was executed.
type SeederResult struct {
	Name     string
	Path     string
	Duration time.Duration
}

// SeederError describes a seed file that failed.
type SeederError struct {
	Name  string
	Error error
}

// SeedPlanView is a read-only view of the seed files selected for execution.
type SeedPlanView struct {
	Command   string
	Directory string
	Class     string
	Seeders   []string
	DryRun    bool
}

type seedFile struct {
	name string
	path string
	sql  []byte
}

var validSeederClass = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]*$`)

// Seed executes SQL seed files under the migration advisory lock. It does not
// create or alter migration metadata, and it stops after the first failed
// file. No execution history is recorded; callers own seed idempotency.
func (m *Migrator) Seed(ctx context.Context, request SeedRequest) (SeedResult, error) {
	files, _, err := m.prepareSeedFiles(request)
	if err != nil {
		return SeedResult{}, err
	}

	result := SeedResult{Command: "seed"}
	err = m.withLockSession(ctx, func(ctx context.Context, conn *sql.Conn, caps *SessionCapabilities) error {
		lockKey, err := deriveLockKey(caps.DatabaseName, m.tableName)
		if err != nil {
			return fmt.Errorf("seed: derive lock key: %w", err)
		}

		for _, file := range files {
			startedAt := time.Now().UTC()
			if err := verifyLockOwnership(ctx, conn, lockKey); err != nil {
				return fmt.Errorf("seed: pre-execution lock check for %s: %w", file.name, err)
			}

			_, sqlErr := conn.ExecContext(ctx, string(file.sql))
			postErr := inspectAndVerifyPostExecution(ctx, conn, caps.DatabaseName, lockKey)
			if sqlErr != nil || postErr != nil {
				if cleanupErr := cleanupSessionState(ctx, conn, caps.DatabaseName, lockKey); cleanupErr != nil {
					return fmt.Errorf("%w: seed %s executed but session cleanup failed: %v (original SQL error: %v)", ErrRecoveryRequired, file.name, cleanupErr, sqlErr)
				}
				if sqlErr != nil {
					err := fmt.Errorf("%w: %s: %v", ErrSQLExecution, file.name, sqlErr)
					result.Errors = append(result.Errors, SeederError{Name: file.name, Error: err})
					return err
				}
				err := fmt.Errorf("%w: %s: post-execution session check failed: %v", ErrSQLExecution, file.name, postErr)
				result.Errors = append(result.Errors, SeederError{Name: file.name, Error: err})
				return err
			}

			result.Seeded = append(result.Seeded, SeederResult{
				Name:     file.name,
				Path:     file.path,
				Duration: time.Since(startedAt),
			})
		}
		return nil
	})
	if err != nil {
		return SeedResult{}, err
	}
	return result, nil
}

// PreviewSeed validates and selects seed files while holding the migration
// advisory lock. It does not execute SQL or mutate migration metadata.
func (m *Migrator) PreviewSeed(ctx context.Context, request SeedRequest) (SeedPlanView, error) {
	files, class, err := m.prepareSeedFiles(request)
	if err != nil {
		return SeedPlanView{}, err
	}

	view := SeedPlanView{
		Command:   "seed",
		Directory: strings.TrimSpace(request.Directory),
		Class:     class,
		DryRun:    true,
		Seeders:   make([]string, 0, len(files)),
	}
	for _, file := range files {
		view.Seeders = append(view.Seeders, file.name)
	}

	if err := m.withLockSession(ctx, func(ctx context.Context, conn *sql.Conn, caps *SessionCapabilities) error {
		return nil
	}); err != nil {
		return SeedPlanView{}, err
	}
	return view, nil
}

func (m *Migrator) prepareSeedFiles(request SeedRequest) ([]seedFile, string, error) {
	directory := strings.TrimSpace(request.Directory)
	if directory == "" {
		return nil, "", fmt.Errorf("%w: seed directory must not be empty", ErrInvalidConfig)
	}
	class, err := normalizeSeederClass(request.Class)
	if err != nil {
		return nil, "", err
	}
	if err := rejectSymlinkComponents(directory); err != nil {
		return nil, "", err
	}
	info, err := os.Lstat(directory)
	if err != nil {
		return nil, "", fmt.Errorf("lamigrate: inspect seed directory %s: %w", directory, err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return nil, "", fmt.Errorf("lamigrate: seed path %s must be a real directory", directory)
	}

	entries, err := os.ReadDir(directory)
	if err != nil {
		return nil, "", fmt.Errorf("lamigrate: read seed directory %s: %w", directory, err)
	}
	selected := make([]string, 0)
	if class != "" {
		selected = append(selected, class+".sql")
	} else {
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
				continue
			}
			selected = append(selected, entry.Name())
		}
		sort.Strings(selected)
	}
	if len(selected) == 0 {
		return nil, "", fmt.Errorf("lamigrate: no .sql seed files found in %s", directory)
	}

	files := make([]seedFile, 0, len(selected))
	for _, name := range selected {
		path := filepath.Join(directory, name)
		fileInfo, err := os.Lstat(path)
		if err != nil {
			return nil, "", fmt.Errorf("lamigrate: inspect seed file %s: %w", path, err)
		}
		if !fileInfo.Mode().IsRegular() || fileInfo.Mode()&os.ModeSymlink != 0 {
			return nil, "", fmt.Errorf("lamigrate: seed file %s must be a regular file", path)
		}
		if fileInfo.Size() > m.maxFile {
			return nil, "", fmt.Errorf("lamigrate: seed file %s exceeds size limit (%d bytes > %d max)", path, fileInfo.Size(), m.maxFile)
		}
		data, err := readSeedFile(path, m.maxFile)
		if err != nil {
			return nil, "", err
		}
		if len(strings.TrimSpace(string(data))) == 0 {
			return nil, "", fmt.Errorf("lamigrate: seed file %s is empty", path)
		}
		files = append(files, seedFile{name: name, path: path, sql: data})
	}
	return files, class, nil
}

func readSeedFile(path string, maxBytes int64) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("lamigrate: open seed file %s: %w", path, err)
	}
	defer file.Close()

	data, err := io.ReadAll(io.LimitReader(file, maxBytes+1))
	if err != nil {
		return nil, fmt.Errorf("lamigrate: read seed file %s: %w", path, err)
	}
	if int64(len(data)) > maxBytes {
		return nil, fmt.Errorf("lamigrate: seed file %s exceeds size limit (%d bytes > %d max)", path, len(data), maxBytes)
	}
	return data, nil
}

func normalizeSeederClass(class string) (string, error) {
	class = strings.TrimSpace(class)
	if class == "" {
		return "", nil
	}
	class = strings.TrimSuffix(class, ".sql")
	if !validSeederClass.MatchString(class) {
		return "", fmt.Errorf("%w: seeder class %q must contain only letters, digits, and underscores", ErrInvalidConfig, class)
	}
	return class, nil
}
