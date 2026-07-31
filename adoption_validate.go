package lamigrate

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// prototypeRow represents a single row from the original 4-column tracking table.
type prototypeRow struct {
	ID        uint64
	Migration string
	Batch     uint64
	AppliedAt time.Time
}

// sourceMapping maps a prototype row to its source migration files.
type sourceMapping struct {
	PrototypeID uint64
	Migration   string
	Batch       uint64
	AppliedAt   time.Time
	SourceKind  string
	SourceName  string
	UpPath      string
	DownPath    string
	UpChecksum  [32]byte
	DownChecksum [32]byte
}

// detectPrototypeShape checks whether the given table has the exact 4-column
// prototype shape: id, migration, batch, applied_at with the correct types
// and a UNIQUE KEY on migration.
func detectPrototypeShape(ctx context.Context, conn *sql.Conn, database, tableName string) (bool, error) {
	// Prototype column definitions in order.
	type protoCol struct {
		name          string
		columnType    string // data_type from information_schema
		extra         string // expected "auto_increment" for id, "" otherwise
		isNullable    string // "NO" for all prototype columns
		defaultExpr   string // DEFAULT expression; "" means no default
	}
	protoCols := []protoCol{
		{name: "id", columnType: "int", extra: "auto_increment", isNullable: "NO", defaultExpr: ""},
		{name: "migration", columnType: "varchar", extra: "", isNullable: "NO", defaultExpr: ""},
		{name: "batch", columnType: "int", extra: "", isNullable: "NO", defaultExpr: ""},
		{name: "applied_at", columnType: "timestamp", extra: "", isNullable: "NO", defaultExpr: "CURRENT_TIMESTAMP"},
	}
	// MySQL 8 may report DEFAULT_GENERATED as extra for timestamp defaults.
	acceptedAppliedAtExtras := map[string]bool{"": true, "DEFAULT_GENERATED": true}

	// Query columns.
	rows, err := conn.QueryContext(ctx,
		`SELECT column_name, data_type, extra, is_nullable, column_default
		 FROM information_schema.columns
		 WHERE table_schema = ? AND table_name = ?
		 ORDER BY ordinal_position`, database, tableName)
	if err != nil {
		return false, fmt.Errorf("lamigrate: query columns for %s.%s: %w", database, tableName, err)
	}
	defer rows.Close()

	var cols []protoCol
	for rows.Next() {
		var c protoCol
		var nullable string
		var colDefault sql.NullString
		if err := rows.Scan(&c.name, &c.columnType, &c.extra, &nullable, &colDefault); err != nil {
			return false, fmt.Errorf("lamigrate: scan column row for %s.%s: %w", database, tableName, err)
		}
		c.isNullable = nullable
		if colDefault.Valid {
			c.defaultExpr = colDefault.String
		}
		cols = append(cols, c)
	}
	if err := rows.Err(); err != nil {
		return false, fmt.Errorf("lamigrate: iterate columns for %s.%s: %w", database, tableName, err)
	}

	// Must have exactly 4 columns.
	if len(cols) != 4 {
		return false, nil
	}

	// Compare each column against the prototype.
	for i, want := range protoCols {
		got := cols[i]
		if got.name != want.name {
			return false, nil
		}
		if got.columnType != want.columnType {
			return false, nil
		}
		if got.extra != want.extra {
			// For the applied_at timestamp column, MySQL 8 may report
			// DEFAULT_GENERATED instead of empty string.
			if i == 3 && acceptedAppliedAtExtras[got.extra] {
				// ok
			} else {
				return false, nil
			}
		}
		if got.isNullable != want.isNullable {
			return false, nil
		}
		// Compare DEFAULT expression. Normalize trailing semicolons and whitespace.
		gotDefault := strings.TrimSpace(strings.TrimSuffix(got.defaultExpr, ";"))
		wantDefault := strings.TrimSpace(strings.TrimSuffix(want.defaultExpr, ";"))
		if strings.ToUpper(gotDefault) != strings.ToUpper(wantDefault) {
			return false, nil
		}
	}

	// Check for UNIQUE KEY uk_migration on the migration column.
	idxRows, err := conn.QueryContext(ctx,
		`SELECT index_name, column_name, non_unique
		 FROM information_schema.statistics
		 WHERE table_schema = ? AND table_name = ?
		   AND index_name = ?
		 ORDER BY seq_in_index`, database, tableName, "uk_migration")
	if err != nil {
		return false, fmt.Errorf("lamigrate: query indexes for %s.%s: %w", database, tableName, err)
	}
	defer idxRows.Close()

	foundUnique := false
	for idxRows.Next() {
		var idxName, colName string
		var nonUnique int
		if err := idxRows.Scan(&idxName, &colName, &nonUnique); err != nil {
			return false, fmt.Errorf("lamigrate: scan index row for %s.%s: %w", database, tableName, err)
		}
		if colName == "migration" && nonUnique == 0 {
			foundUnique = true
		}
	}
	if err := idxRows.Err(); err != nil {
		return false, fmt.Errorf("lamigrate: iterate indexes for %s.%s: %w", database, tableName, err)
	}

	if !foundUnique {
		return false, nil
	}

	return true, nil
}

// readPrototypeRows reads all rows from the prototype table ordered by id ASC.
func readPrototypeRows(ctx context.Context, conn *sql.Conn, tableName string) ([]prototypeRow, error) {
	rows, err := conn.QueryContext(ctx,
		"SELECT id, migration, batch, applied_at FROM `"+tableName+"` ORDER BY id ASC")
	if err != nil {
		return nil, fmt.Errorf("lamigrate: query prototype rows from %s: %w", tableName, err)
	}
	defer rows.Close()

	var result []prototypeRow
	for rows.Next() {
		var r prototypeRow
		if err := rows.Scan(&r.ID, &r.Migration, &r.Batch, &r.AppliedAt); err != nil {
			return nil, fmt.Errorf("lamigrate: scan prototype row from %s: %w", tableName, err)
		}
		result = append(result, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("lamigrate: iterate prototype rows from %s: %w", tableName, err)
	}
	return result, nil
}

// validatePrototypeRows checks that the prototype rows are valid:
// at least one row, MAX(id) < uint64 max, and no duplicate migration values.
func validatePrototypeRows(rows []prototypeRow) error {
	if len(rows) == 0 {
		return fmt.Errorf("%w: prototype table has no rows", ErrInvalidConfig)
	}

	const uint64Max = 18446744073709551615
	seen := make(map[string]bool, len(rows))
	for _, r := range rows {
		if r.ID >= uint64Max {
			return fmt.Errorf(
				"%w: prototype row id %d exceeds uint64 range",
				ErrInvalidConfig, r.ID,
			)
		}
		if seen[r.Migration] {
			return fmt.Errorf(
				"%w: duplicate migration %q in prototype table",
				ErrInvalidConfig, r.Migration,
			)
		}
		seen[r.Migration] = true
	}
	return nil
}

var (
	// timestampMigrationID matches the lamigrate timestamp pattern: 14 digits
	// followed by underscore + snake_case name.
	timestampMigrationID = regexp.MustCompile(`^[0-9]{14}_[a-z][a-z0-9_]*$`)
	// numericMigrationID matches a pure numeric legacy version string.
	numericMigrationID = regexp.MustCompile(`^[0-9]+$`)
)

// mapSourceFiles maps each prototype row to its source migration files.
// For batch > 0 it expects timestamp-format migrations in directory.
// For batch = 0 it expects numeric legacy versions in legacyDir.
func mapSourceFiles(rows []prototypeRow, directory, legacyDir string) ([]sourceMapping, error) {
	result := make([]sourceMapping, 0, len(rows))

	// Track source names to detect duplicates.
	seen := make(map[string]bool)

	for _, row := range rows {
		sm := sourceMapping{
			PrototypeID: row.ID,
			Migration:   row.Migration,
			Batch:       row.Batch,
			AppliedAt:   row.AppliedAt,
		}

		if row.Batch > 0 {
			// Timestamp migration in directory.
			sm.SourceKind = "timestamp"

			if !timestampMigrationID.MatchString(row.Migration) {
				return nil, fmt.Errorf(
					"%w: migration %q (id %d) does not match timestamp pattern",
					ErrInvalidConfig, row.Migration, row.ID,
				)
			}

			sm.SourceName = row.Migration
			sm.UpPath = filepath.Join(directory, row.Migration+".up.sql")
			sm.DownPath = filepath.Join(directory, row.Migration+".down.sql")

			if _, err := os.Stat(sm.UpPath); err != nil {
				return nil, fmt.Errorf(
					"%w: migration %q (id %d) missing up file %s",
					ErrInvalidConfig, row.Migration, row.ID, sm.UpPath,
				)
			}
			if _, err := os.Stat(sm.DownPath); err != nil {
				return nil, fmt.Errorf(
					"%w: migration %q (id %d) missing down file %s",
					ErrInvalidConfig, row.Migration, row.ID, sm.DownPath,
				)
			}
		} else {
			// Legacy numeric version in legacyDir.
			sm.SourceKind = "golang_migrate"

			if !numericMigrationID.MatchString(row.Migration) {
				return nil, fmt.Errorf(
					"%w: migration %q (id %d) does not match numeric pattern",
					ErrInvalidConfig, row.Migration, row.ID,
				)
			}

			// Scan legacyDir for matching files.
			upPath, downPath, sourceName, err := findLegacyPair(row.Migration, legacyDir)
			if err != nil {
				return nil, fmt.Errorf(
					"%w: migration %q (id %d): %v",
					ErrInvalidConfig, row.Migration, row.ID, err,
				)
			}

			sm.SourceName = sourceName
			sm.UpPath = upPath
			sm.DownPath = downPath
		}

		// Reject duplicate source names.
		if seen[sm.SourceName] {
			return nil, fmt.Errorf(
				"%w: duplicate source %q (id %d)",
				ErrInvalidConfig, sm.SourceName, row.ID,
			)
		}
		seen[sm.SourceName] = true

		// Compute checksums.
		upCS, err := checksumFile(sm.UpPath)
		if err != nil {
			return nil, fmt.Errorf(
				"lamigrate: checksum up file %s for migration %q (id %d): %w",
				sm.UpPath, row.Migration, row.ID, err,
			)
		}
		sm.UpChecksum = upCS

		downCS, err := checksumFile(sm.DownPath)
		if err != nil {
			return nil, fmt.Errorf(
				"lamigrate: checksum down file %s for migration %q (id %d): %w",
				sm.DownPath, row.Migration, row.ID, err,
			)
		}
		sm.DownChecksum = downCS

		result = append(result, sm)
	}

	// Sort by ascending prototype id.
	sort.Slice(result, func(i, j int) bool {
		return result[i].PrototypeID < result[j].PrototypeID
	})

	return result, nil
}

// findLegacyPair searches legacyDir for up and down files matching the given
// numeric version. It returns the up path, down path, and the source name
// (the up filename without the .sql extension). Returns an error if no
// matching files exist, or if there are ambiguous matches.
func findLegacyPair(version, legacyDir string) (upPath, downPath, sourceName string, err error) {
	entries, err := os.ReadDir(legacyDir)
	if err != nil {
		return "", "", "", fmt.Errorf("read legacy dir %s: %w", legacyDir, err)
	}

	type candidate struct {
		ver       uint64
		verStr    string
		desc      string
		ext       string // "up" or "down"
		filename  string
	}

	// Group by version → description → entries.
	type descEntry struct {
		up   *candidate
		down *candidate
	}
	byVersion := make(map[uint64]map[string]*descEntry)

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		m := legacyImportFilePattern.FindStringSubmatch(name)
		if m == nil {
			continue
		}
		ver, _ := strconv.ParseUint(m[1], 10, 64)
		if ver == 0 || ver != mustParseUint(version) {
			continue
		}
		desc := m[2]
		ext := m[3]

		if byVersion[ver] == nil {
			byVersion[ver] = make(map[string]*descEntry)
		}
		if byVersion[ver][desc] == nil {
			byVersion[ver][desc] = &descEntry{}
		}
		de := byVersion[ver][desc]
		c := &candidate{
			ver:      ver,
			verStr:   m[1],
			desc:     desc,
			ext:      ext,
			filename: name,
		}
		if ext == "up" {
			if de.up != nil {
				return "", "", "", fmt.Errorf(
					"duplicate up file for version %d desc %q: %s and %s",
					ver, desc, de.up.filename, name,
				)
			}
			de.up = c
		} else {
			if de.down != nil {
				return "", "", "", fmt.Errorf(
					"duplicate down file for version %d desc %q: %s and %s",
					ver, desc, de.down.filename, name,
				)
			}
			de.down = c
		}
	}

	ver := mustParseUint(version)
	descs, ok := byVersion[ver]
	if !ok || len(descs) == 0 {
		return "", "", "", fmt.Errorf("no legacy files found for version %s in %s", version, legacyDir)
	}

	// Exactly one description allowed per version.
	if len(descs) != 1 {
		var names []string
		for d := range descs {
			names = append(names, d)
		}
		return "", "", "", fmt.Errorf(
			"version %s has multiple descriptions: %v", version, names,
		)
	}

	var desc string
	for d := range descs {
		desc = d
	}
	de := descs[desc]

	if de.up == nil {
		return "", "", "", fmt.Errorf("version %s desc %q missing up file", version, desc)
	}
	if de.down == nil {
		return "", "", "", fmt.Errorf("version %s desc %q missing down file", version, desc)
	}

	upPath = filepath.Join(legacyDir, de.up.filename)
	downPath = filepath.Join(legacyDir, de.down.filename)
	sourceName = de.up.verStr + "_" + desc

	return upPath, downPath, sourceName, nil
}

// mustParseUint parses a decimal string to uint64. Panics on error.
func mustParseUint(s string) uint64 {
	v, err := strconv.ParseUint(s, 10, 64)
	if err != nil {
		panic(fmt.Sprintf("lamigrate: mustParseUint(%q): %v", s, err))
	}
	return v
}

// validateBackupTableName checks that the backup table name is valid: must
// match [a-z][a-z0-9_]*, must not exceed 64 chars, must differ from the
// tracking table, and must not be "lamigrate_control".
func validateBackupTableName(name, trackingTable string) error {
	if len(name) == 0 {
		return fmt.Errorf("%w: backup table name must not be empty", ErrInvalidConfig)
	}
	if len(name) > 64 {
		return fmt.Errorf(
			"%w: backup table name %q exceeds 64 characters",
			ErrInvalidConfig, name,
		)
	}
	if !validTrackingTable.MatchString(name) {
		return fmt.Errorf(
			"%w: backup table name %q must match [a-z_][a-z0-9_]*",
			ErrInvalidConfig, name,
		)
	}
	if name == trackingTable {
		return fmt.Errorf(
			"%w: backup table name %q must differ from tracking table %q",
			ErrInvalidConfig, name, trackingTable,
		)
	}
	if name == controlTableName {
		return fmt.Errorf(
			"%w: backup table name %q must not be %q",
			ErrInvalidConfig, name, controlTableName,
		)
	}
	return nil
}
