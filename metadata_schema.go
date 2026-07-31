package lamigrate

// metadata_schema.go — DDL definitions for the v1 metadata tables.
//
// These constants define the exact schema for:
//   - lamigrate_control: the internal control table storing metadata schema
//     version and batch allocation state (architecture §9).
//   - migrations: the configurable migration-state table (architecture §9).
//
// Both DDLs use CREATE TABLE IF NOT EXISTS and are safe to execute
// multiple times during bootstrap. The schemas are permanent for v1;
// changes require a schema-version migration.

// controlTableDDL is the CREATE TABLE statement for the internal
// lamigrate_control table. This table stores one row per validated
// migration-state table, preserving the metadata schema version and
// a durable next-batch counter (architecture §9).
//
// The tracking_table column uses ASCII charset for portable storage
// of lowercase identifiers. The PRIMARY KEY constraint ensures exactly
// one row per tracked migration-state table.
const controlTableDDL = `CREATE TABLE IF NOT EXISTS ` + "`lamigrate_control`" + ` (
    tracking_table    VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    schema_version    INT UNSIGNED NOT NULL,
    next_batch        BIGINT UNSIGNED NOT NULL,
    updated_at        DATETIME(6) NOT NULL,
    PRIMARY KEY (tracking_table)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci`

// stateTableDDL is the CREATE TABLE statement for the configurable
// migration-state table. The default name is "migrations" but the
// actual name is provided at runtime via the tableName parameter.
//
// This function returns the DDL with the given table name interpolated.
// The name MUST have been validated against validTrackingTable before
// this function is called.
//
// CHECK constraint names include the table name to ensure uniqueness
// within a database, since MySQL enforces global constraint-name
// uniqueness.
func stateTableDDL(tableName string) string {
	return "CREATE TABLE IF NOT EXISTS `" + tableName + "` (" +
		"`id`                BIGINT UNSIGNED NOT NULL AUTO_INCREMENT, " +
		"`migration`         VARCHAR(255) CHARACTER SET ascii COLLATE ascii_bin NOT NULL, " +
		"`source_kind`       VARCHAR(24) CHARACTER SET ascii COLLATE ascii_bin NOT NULL, " +
		"`source_version`    BIGINT UNSIGNED NULL, " +
		"`source_name`       VARCHAR(255) CHARACTER SET ascii COLLATE ascii_bin NOT NULL, " +
		"`up_checksum`       BINARY(32) NOT NULL, " +
		"`down_checksum`     BINARY(32) NULL, " +
		"`batch`             BIGINT UNSIGNED NOT NULL, " +
		"`state`             VARCHAR(24) CHARACTER SET ascii COLLATE ascii_bin NOT NULL, " +
		"`is_baseline`       BOOLEAN NOT NULL DEFAULT FALSE, " +
		"`runner_id`         CHAR(36) CHARACTER SET ascii COLLATE ascii_bin NOT NULL, " +
		"`started_at`        DATETIME(6) NOT NULL, " +
		"`applied_at`        DATETIME(6) NULL, " +
		"`updated_at`        DATETIME(6) NOT NULL, " +
		"PRIMARY KEY (id), " +
		"UNIQUE KEY uk_migration (migration), " +
		"KEY idx_batch_state (batch, state), " +
		"CONSTRAINT `" + tableName + "_chk_state` CHECK (" +
		"state IN ('applying', 'applied', 'apply_failed', 'rolling_back', 'rollback_failed')" +
		"), " +
		"CONSTRAINT `" + tableName + "_chk_source` CHECK (" +
		"source_kind IN ('timestamp', 'golang_migrate')" +
		"), " +
		"CONSTRAINT `" + tableName + "_chk_fields` CHECK (" +
		"(source_kind = 'timestamp' AND source_version IS NULL AND is_baseline = FALSE AND batch > 0) " +
		"OR " +
		"(source_kind = 'golang_migrate' AND source_version IS NOT NULL AND is_baseline = TRUE AND batch = 0 AND state = 'applied')" +
		"), " +
		"CONSTRAINT `" + tableName + "_chk_times` CHECK (" +
		"(state IN ('applying', 'apply_failed') AND applied_at IS NULL) " +
		"OR " +
		"(state IN ('applied', 'rolling_back', 'rollback_failed') AND applied_at IS NOT NULL)" +
		")" +
		") ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci"
}

// controlTableName is the fixed name of the internal control table.
// It is reserved and cannot be used as a custom tracking-table name.
const controlTableName = "lamigrate_control"

// requiredControlColumns defines the column names expected in the
// lamigrate_control table, in order. validateTableShape checks these
// exist with the correct types.
var requiredControlColumns = []string{
	"tracking_table",
	"schema_version",
	"next_batch",
	"updated_at",
}

// requiredStateColumns defines the column names expected in the
// migration-state table, in order. validateTableShape checks these
// exist with the correct types.
var requiredStateColumns = []string{
	"id",
	"migration",
	"source_kind",
	"source_version",
	"source_name",
	"up_checksum",
	"down_checksum",
	"batch",
	"state",
	"is_baseline",
	"runner_id",
	"started_at",
	"applied_at",
	"updated_at",
}

// columnSpec describes the expected properties of a metadata column.
type columnSpec struct {
	Name         string
	DataType     string // base type family, e.g. "varchar", "bigint", "binary"
	Unsigned     bool   // expected unsigned flag
	Nullable     bool   // whether NULL is allowed
	Binary       bool   // for varchar: whether binary collation is required
	MaxLen       int    // expected max character length (0 = don't check)
}

// controlColumnSpecs defines the expected type properties for each
// column in lamigrate_control, validated against information_schema.
var controlColumnSpecs = map[string]columnSpec{
	"tracking_table": {Name: "tracking_table", DataType: "varchar", Unsigned: false, Nullable: false, Binary: true, MaxLen: 64},
	"schema_version": {Name: "schema_version", DataType: "int", Unsigned: true, Nullable: false},
	"next_batch":     {Name: "next_batch", DataType: "bigint", Unsigned: true, Nullable: false},
	"updated_at":     {Name: "updated_at", DataType: "datetime", Unsigned: false, Nullable: false},
}

// stateColumnSpecs defines the expected type properties for each
// column in the migration-state table, validated against information_schema.
var stateColumnSpecs = map[string]columnSpec{
	"id":             {Name: "id", DataType: "bigint", Unsigned: true, Nullable: false},
	"migration":      {Name: "migration", DataType: "varchar", Unsigned: false, Nullable: false, Binary: true, MaxLen: 255},
	"source_kind":    {Name: "source_kind", DataType: "varchar", Unsigned: false, Nullable: false, Binary: true, MaxLen: 24},
	"source_version": {Name: "source_version", DataType: "bigint", Unsigned: true, Nullable: true},
	"source_name":    {Name: "source_name", DataType: "varchar", Unsigned: false, Nullable: false, Binary: true, MaxLen: 255},
	"up_checksum":    {Name: "up_checksum", DataType: "binary", Unsigned: false, Nullable: false},
	"down_checksum":  {Name: "down_checksum", DataType: "binary", Unsigned: false, Nullable: true},
	"batch":          {Name: "batch", DataType: "bigint", Unsigned: true, Nullable: false},
	"state":          {Name: "state", DataType: "varchar", Unsigned: false, Nullable: false, Binary: true, MaxLen: 24},
	"is_baseline":    {Name: "is_baseline", DataType: "tinyint", Unsigned: false, Nullable: false},
	"runner_id":      {Name: "runner_id", DataType: "char", Unsigned: false, Nullable: false, Binary: true, MaxLen: 36},
	"started_at":     {Name: "started_at", DataType: "datetime", Unsigned: false, Nullable: false},
	"applied_at":     {Name: "applied_at", DataType: "datetime", Unsigned: false, Nullable: true},
	"updated_at":     {Name: "updated_at", DataType: "datetime", Unsigned: false, Nullable: false},
}

// allowedExtraIndexes lists non-unique indexes that are permitted
// on the state table beyond the primary key and unique key.
// validateTableShape permits these additional indexes but rejects
// any unlisted non-unique or unique keys.
var allowedExtraIndexes = map[string]bool{
	"idx_batch_state": true,
}
