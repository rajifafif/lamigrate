package lamigrate

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
)

// metadata_validate.go — Semantic validation for metadata tables and rows.
//
// All validation is defensive: even if MySQL CHECK constraints are
// reported as enforced, every row is validated against cross-field
// invariants on read (architecture §9).
//
// validateControlRow: schema_version must be 1, next_batch > max(batch).
// validateStateRow: cross-field invariants for source_kind, state, timestamps.
// validateTableShape: information_schema inspection for column types, keys,
//   constraints, engine, charset; rejects extra columns, triggers, foreign keys.

// validateControlRow checks semantic invariants on a control row:
//   - schema_version must be exactly 1
//   - next_batch must be greater than the maximum positive batch value
//
// Returns nil if valid, or a descriptive error wrapping ErrUnsupportedMetadata.
func validateControlRow(schemaVersion uint64, nextBatch uint64, maxPositiveBatch uint64) error {
	if schemaVersion != 1 {
		return fmt.Errorf(
			"%w: control row has schema_version=%d, want 1",
			ErrUnsupportedMetadata, schemaVersion,
		)
	}
	if nextBatch <= maxPositiveBatch {
		return fmt.Errorf(
			"%w: control row next_batch=%d must be > max positive batch=%d",
			ErrUnsupportedMetadata, nextBatch, maxPositiveBatch,
		)
	}
	return nil
}

// validateStateRow checks cross-field invariants for a single
// migration-state row (architecture §9). These checks are performed
// on every read even when MySQL CHECK constraints are enforced.
//
// Valid source_kind values: "timestamp", "golang_migrate"
// Valid state values: "applying", "applied", "apply_failed",
//
//	"rolling_back", "rollback_failed"
//
// Cross-field rules:
//   - timestamp rows: source_version must be nil, is_baseline=false, batch>0
//   - golang_migrate rows: source_version must be non-nil, is_baseline=true,
//     batch=0, state="applied"
//   - state in (applying, apply_failed) => applied_at must be nil
//   - state in (applied, rolling_back, rollback_failed) => applied_at must be non-nil
func validateStateRow(sourceKind string, sourceVersion *uint64, isBaseline bool, batch uint64, state string, appliedAt *time.Time) error {
	// Validate source_kind.
	if sourceKind != "timestamp" && sourceKind != "golang_migrate" {
		return fmt.Errorf(
			"%w: invalid source_kind %q",
			ErrUnsupportedMetadata, sourceKind,
		)
	}

	// Validate state.
	switch state {
	case "applying", "applied", "apply_failed", "rolling_back", "rollback_failed":
		// valid
	default:
		return fmt.Errorf(
			"%w: invalid state %q",
			ErrUnsupportedMetadata, state,
		)
	}

	// Check source_fields constraints.
	switch sourceKind {
	case "timestamp":
		if sourceVersion != nil {
			return fmt.Errorf(
				"%w: timestamp migration must have NULL source_version",
				ErrUnsupportedMetadata,
			)
		}
		if isBaseline {
			return fmt.Errorf(
				"%w: timestamp migration must have is_baseline=FALSE",
				ErrUnsupportedMetadata,
			)
		}
		if batch == 0 {
			return fmt.Errorf(
				"%w: timestamp migration must have batch > 0",
				ErrUnsupportedMetadata,
			)
		}
	case "golang_migrate":
		if sourceVersion == nil {
			return fmt.Errorf(
				"%w: golang_migrate migration must have non-NULL source_version",
				ErrUnsupportedMetadata,
			)
		}
		if !isBaseline {
			return fmt.Errorf(
				"%w: golang_migrate migration must have is_baseline=TRUE",
				ErrUnsupportedMetadata,
			)
		}
		if batch != 0 {
			return fmt.Errorf(
				"%w: golang_migrate migration must have batch=0",
				ErrUnsupportedMetadata,
			)
		}
		if state != "applied" {
			return fmt.Errorf(
				"%w: golang_migrate migration must have state='applied'",
				ErrUnsupportedMetadata,
			)
		}
	}

	// Check state_times constraints.
	switch state {
	case "applying", "apply_failed":
		if appliedAt != nil {
			return fmt.Errorf(
				"%w: state=%q must have applied_at=NULL",
				ErrUnsupportedMetadata, state,
			)
		}
	case "applied", "rolling_back", "rollback_failed":
		if appliedAt == nil {
			return fmt.Errorf(
				"%w: state=%q must have applied_at NOT NULL",
				ErrUnsupportedMetadata, state,
			)
		}
	}

	return nil
}

// tableColumnInfo holds column metadata from information_schema.
type tableColumnInfo struct {
	Name         string
	DataType     string // MySQL data_type: varchar, bigint, int, binary, datetime, tinyint, char
	ColumnType   string // MySQL column_type: varchar(64), bigint unsigned, etc.
	IsNullable   string // "YES" or "NO"
	CharMaxLength *int64
	CharacterSet  sql.NullString
	Collation     sql.NullString
}

// tableIndexInfo holds index metadata from information_schema.
type tableIndexInfo struct {
	IndexName string
	SeqInKey  int
	ColumnName string
	NonUnique  int // 0 = unique/primary, 1 = non-unique
}

// validateTableShape validates the physical table schema against the
// expected v1 shape using information_schema queries. It checks:
//   - Required columns exist with correct type families
//   - Required columns are NOT NULL where specified
//   - Binary/ascii collation for required columns
//   - Engine is InnoDB
//   - Charset is utf8mb4
//   - No extra columns beyond the required set
//   - No foreign keys
//   - No triggers
//   - No partitions
//   - No extra unique keys beyond the expected ones
//   - Allowed non-unique indexes are present
//
// tableType should be "control" or "state".
func validateTableShape(ctx context.Context, conn *sql.Conn, database, tableName, tableType string) error {
	// 1. Verify table exists.
	var count int
	err := conn.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = ? AND table_name = ?",
		database, tableName,
	).Scan(&count)
	if err != nil {
		return fmt.Errorf("query information_schema.tables: %w", err)
	}
	if count == 0 {
		return fmt.Errorf(
			"%w: table %q does not exist in database %q",
			ErrUnsupportedMetadata, tableName, database,
		)
	}

	// 2. Check engine and charset.
	var engine, tableCharset string
	err = conn.QueryRowContext(ctx,
		"SELECT engine, table_collation FROM information_schema.tables WHERE table_schema = ? AND table_name = ?",
		database, tableName,
	).Scan(&engine, &tableCharset)
	if err != nil {
		return fmt.Errorf("query table engine: %w", err)
	}
	if strings.ToUpper(engine) != "INNODB" {
		return fmt.Errorf(
			"%w: table %q engine is %q, want INNODB",
			ErrUnsupportedMetadata, tableName, engine,
		)
	}
	if !strings.HasPrefix(strings.ToLower(tableCharset), "utf8mb4") {
		return fmt.Errorf(
			"%w: table %q collation is %q, want utf8mb4*",
			ErrUnsupportedMetadata, tableName, tableCharset,
		)
	}

	// 3. Query columns.
	rows, err := conn.QueryContext(ctx,
		"SELECT column_name, data_type, column_type, is_nullable, character_maximum_length, character_set_name, collation_name "+
			"FROM information_schema.columns WHERE table_schema = ? AND table_name = ? ORDER BY ordinal_position",
		database, tableName,
	)
	if err != nil {
		return fmt.Errorf("query information_schema.columns: %w", err)
	}
	defer rows.Close()

	var columns []tableColumnInfo
	for rows.Next() {
		var col tableColumnInfo
		if err := rows.Scan(
			&col.Name, &col.DataType, &col.ColumnType,
			&col.IsNullable, &col.CharMaxLength,
			&col.CharacterSet, &col.Collation,
		); err != nil {
			return fmt.Errorf("scan column info: %w", err)
		}
		columns = append(columns, col)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate columns: %w", err)
	}

	// Determine which set of required columns and specs to use.
	var requiredCols []string
	var specs map[string]columnSpec
	switch tableType {
	case "control":
		requiredCols = requiredControlColumns
		specs = controlColumnSpecs
	case "state":
		requiredCols = requiredStateColumns
		specs = stateColumnSpecs
	default:
		return fmt.Errorf("unknown table type: %s", tableType)
	}

	// Check no extra columns.
	colNames := make(map[string]bool, len(columns))
	for _, col := range columns {
		colNames[col.Name] = true
	}
	for _, req := range requiredCols {
		if !colNames[req] {
			return fmt.Errorf(
				"%w: required column %q missing from table %q",
				ErrUnsupportedMetadata, req, tableName,
			)
		}
	}
	if len(columns) != len(requiredCols) {
		extra := make([]string, 0)
		reqSet := make(map[string]bool, len(requiredCols))
		for _, r := range requiredCols {
			reqSet[r] = true
		}
		for _, col := range columns {
			if !reqSet[col.Name] {
				extra = append(extra, col.Name)
			}
		}
		return fmt.Errorf(
			"%w: table %q has extra columns: %s",
			ErrUnsupportedMetadata, tableName, strings.Join(extra, ", "),
		)
	}

	// Validate each column's type properties.
	for _, col := range columns {
		spec, ok := specs[col.Name]
		if !ok {
			return fmt.Errorf(
				"%w: unexpected column %q in table %q",
				ErrUnsupportedMetadata, col.Name, tableName,
			)
		}

		if spec.Nullable && col.IsNullable != "YES" {
			return fmt.Errorf(
				"%w: column %q should be nullable but is NOT NULL",
				ErrUnsupportedMetadata, col.Name,
			)
		}
		if !spec.Nullable && col.IsNullable != "NO" {
			return fmt.Errorf(
				"%w: column %q should be NOT NULL but is nullable",
				ErrUnsupportedMetadata, col.Name,
			)
		}

		// Check unsigned for integer types.
		if spec.Unsigned && !strings.Contains(strings.ToLower(col.ColumnType), "unsigned") {
			return fmt.Errorf(
				"%w: column %q should be unsigned but column_type is %q",
				ErrUnsupportedMetadata, col.Name, col.ColumnType,
			)
		}

		// Check binary collation for varchar/char columns.
		if spec.Binary {
			collation := strings.ToLower(col.Collation.String)
			if !strings.Contains(collation, "bin") && !strings.Contains(collation, "ascii") {
				return fmt.Errorf(
					"%w: column %q should have binary/ascii collation but has %q",
					ErrUnsupportedMetadata, col.Name, col.Collation.String,
				)
			}
		}

		// Check max length for varchar/char.
		if spec.MaxLen > 0 && col.CharMaxLength != nil && *col.CharMaxLength != int64(spec.MaxLen) {
			return fmt.Errorf(
				"%w: column %q max_length is %d, want %d",
				ErrUnsupportedMetadata, col.Name, *col.CharMaxLength, spec.MaxLen,
			)
		}
	}

	// 4. Check for foreign keys.
	var fkCount int
	err = conn.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM information_schema.table_constraints "+
			"WHERE constraint_schema = ? AND table_name = ? AND constraint_type = 'FOREIGN KEY'",
		database, tableName,
	).Scan(&fkCount)
	if err != nil {
		return fmt.Errorf("query foreign keys: %w", err)
	}
	if fkCount > 0 {
		return fmt.Errorf(
			"%w: table %q has %d foreign keys (none allowed)",
			ErrUnsupportedMetadata, tableName, fkCount,
		)
	}

	// 5. Check for triggers.
	var trigCount int
	err = conn.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM information_schema.triggers WHERE trigger_schema = ? AND event_object_table = ?",
		database, tableName,
	).Scan(&trigCount)
	if err != nil {
		return fmt.Errorf("query triggers: %w", err)
	}
	if trigCount > 0 {
		return fmt.Errorf(
			"%w: table %q has %d triggers (none allowed)",
			ErrUnsupportedMetadata, tableName, trigCount,
		)
	}

	// 6. Check for partitions.
	var partCount int
	err = conn.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM information_schema.partitions WHERE table_schema = ? AND table_name = ? AND partition_name IS NOT NULL",
		database, tableName,
	).Scan(&partCount)
	if err != nil {
		return fmt.Errorf("query partitions: %w", err)
	}
	if partCount > 0 {
		return fmt.Errorf(
			"%w: table %q is partitioned (not allowed)",
			ErrUnsupportedMetadata, tableName,
		)
	}

	// 7. Check indexes.
	idxRows, err := conn.QueryContext(ctx,
		"SELECT index_name, seq_in_index, column_name, non_unique "+
			"FROM information_schema.statistics WHERE table_schema = ? AND table_name = ? "+
			"ORDER BY index_name, seq_in_index",
		database, tableName,
	)
	if err != nil {
		return fmt.Errorf("query indexes: %w", err)
	}
	defer idxRows.Close()

	var indexes []tableIndexInfo
	for idxRows.Next() {
		var idx tableIndexInfo
		if err := idxRows.Scan(&idx.IndexName, &idx.SeqInKey, &idx.ColumnName, &idx.NonUnique); err != nil {
			return fmt.Errorf("scan index info: %w", err)
		}
		indexes = append(indexes, idx)
	}
	if err := idxRows.Err(); err != nil {
		return fmt.Errorf("iterate indexes: %w", err)
	}

	// Build expected unique key names.
	expectedUniqueKeys := map[string]bool{
		"PRIMARY": true,
	}

	switch tableType {
	case "state":
		expectedUniqueKeys["uk_migration"] = true
	}

	// Classify indexes.
	for _, idx := range indexes {
		if idx.NonUnique == 0 {
			// Unique or primary key.
			if !expectedUniqueKeys[idx.IndexName] {
				return fmt.Errorf(
					"%w: table %q has unexpected unique/key index %q",
					ErrUnsupportedMetadata, tableName, idx.IndexName,
				)
			}
		} else {
			// Non-unique index.
			if !allowedExtraIndexes[idx.IndexName] {
				return fmt.Errorf(
					"%w: table %q has unexpected non-unique index %q",
					ErrUnsupportedMetadata, tableName, idx.IndexName,
				)
			}
		}
	}

	// Verify expected non-unique indexes exist for state table.
	if tableType == "state" {
		foundIdx := make(map[string]bool)
		for _, idx := range indexes {
			if idx.NonUnique == 1 {
				foundIdx[idx.IndexName] = true
			}
		}
		for name := range allowedExtraIndexes {
			if !foundIdx[name] {
				return fmt.Errorf(
					"%w: table %q is missing expected non-unique index %q",
					ErrUnsupportedMetadata, tableName, name,
				)
			}
		}
	}

	return nil
}
