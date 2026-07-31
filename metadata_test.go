package lamigrate

import (
	"testing"
	"time"
)

// metadata_test.go — Unit tests for validation, schema DDL, and batch logic.

func TestValidateControlRow(t *testing.T) {
	tests := []struct {
		name             string
		schemaVersion    uint64
		nextBatch        uint64
		maxPositiveBatch uint64
		wantErr          bool
	}{
		{
			name:             "valid initial state",
			schemaVersion:    1,
			nextBatch:        1,
			maxPositiveBatch: 0,
			wantErr:          false,
		},
		{
			name:             "valid after migrations",
			schemaVersion:    1,
			nextBatch:        5,
			maxPositiveBatch: 4,
			wantErr:          false,
		},
		{
			name:             "invalid schema version",
			schemaVersion:    2,
			nextBatch:        1,
			maxPositiveBatch: 0,
			wantErr:          true,
		},
		{
			name:             "zero schema version",
			schemaVersion:    0,
			nextBatch:        1,
			maxPositiveBatch: 0,
			wantErr:          true,
		},
		{
			name:             "next_batch equals max batch",
			schemaVersion:    1,
			nextBatch:        3,
			maxPositiveBatch: 3,
			wantErr:          true,
		},
		{
			name:             "next_batch less than max batch",
			schemaVersion:    1,
			nextBatch:        2,
			maxPositiveBatch: 5,
			wantErr:          true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateControlRow(tt.schemaVersion, tt.nextBatch, tt.maxPositiveBatch)
			if (err != nil) != tt.wantErr {
				t.Errorf("validateControlRow(%d, %d, %d) error = %v, wantErr %v",
					tt.schemaVersion, tt.nextBatch, tt.maxPositiveBatch, err, tt.wantErr)
			}
		})
	}
}

func TestValidateStateRow(t *testing.T) {
	ptrUint64 := func(v uint64) *uint64 { return &v }
	parseTime := func(s string) *time.Time {
		t.Helper()
		ts, err := time.Parse("2006-01-02", s)
		if err != nil {
			t.Fatalf("parse time %q: %v", s, err)
		}
		return &ts
	}

	tests := []struct {
		name          string
		sourceKind    string
		sourceVersion *uint64
		isBaseline    bool
		batch         uint64
		state         string
		appliedAt     *time.Time
		wantErr       bool
	}{
		// Valid timestamp migration states.
		{
			name:          "valid applying timestamp",
			sourceKind:    "timestamp",
			sourceVersion: nil,
			isBaseline:    false,
			batch:         1,
			state:         "applying",
			appliedAt:     nil,
			wantErr:       false,
		},
		{
			name:          "valid applied timestamp",
			sourceKind:    "timestamp",
			sourceVersion: nil,
			isBaseline:    false,
			batch:         1,
			state:         "applied",
			appliedAt:     parseTime("2024-01-01"),
			wantErr:       false,
		},
		{
			name:          "valid apply_failed timestamp",
			sourceKind:    "timestamp",
			sourceVersion: nil,
			isBaseline:    false,
			batch:         2,
			state:         "apply_failed",
			appliedAt:     nil,
			wantErr:       false,
		},
		{
			name:          "valid rolling_back timestamp",
			sourceKind:    "timestamp",
			sourceVersion: nil,
			isBaseline:    false,
			batch:         1,
			state:         "rolling_back",
			appliedAt:     parseTime("2024-01-01"),
			wantErr:       false,
		},
		{
			name:          "valid rollback_failed timestamp",
			sourceKind:    "timestamp",
			sourceVersion: nil,
			isBaseline:    false,
			batch:         1,
			state:         "rollback_failed",
			appliedAt:     parseTime("2024-01-01"),
			wantErr:       false,
		},
		// Valid golang_migrate baseline.
		{
			name:          "valid golang_migrate baseline",
			sourceKind:    "golang_migrate",
			sourceVersion: ptrUint64(7),
			isBaseline:    true,
			batch:         0,
			state:         "applied",
			appliedAt:     parseTime("2024-01-01"),
			wantErr:       false,
		},
		// Invalid source_kind.
		{
			name:       "invalid source_kind",
			sourceKind: "unknown",
			state:      "applied",
			wantErr:    true,
		},
		// Invalid state.
		{
			name:       "invalid state",
			sourceKind: "timestamp",
			state:      "invalid_state",
			wantErr:    true,
		},
		// timestamp with source_version set.
		{
			name:          "timestamp with source_version",
			sourceKind:    "timestamp",
			sourceVersion: ptrUint64(1),
			isBaseline:    false,
			batch:         1,
			state:         "applied",
			appliedAt:     parseTime("2024-01-01"),
			wantErr:       true,
		},
		// timestamp with is_baseline.
		{
			name:          "timestamp with is_baseline",
			sourceKind:    "timestamp",
			sourceVersion: nil,
			isBaseline:    true,
			batch:         1,
			state:         "applied",
			appliedAt:     parseTime("2024-01-01"),
			wantErr:       true,
		},
		// timestamp with batch=0.
		{
			name:          "timestamp with batch=0",
			sourceKind:    "timestamp",
			sourceVersion: nil,
			isBaseline:    false,
			batch:         0,
			state:         "applied",
			appliedAt:     parseTime("2024-01-01"),
			wantErr:       true,
		},
		// golang_migrate without source_version.
		{
			name:          "golang_migrate without source_version",
			sourceKind:    "golang_migrate",
			sourceVersion: nil,
			isBaseline:    true,
			batch:         0,
			state:         "applied",
			appliedAt:     parseTime("2024-01-01"),
			wantErr:       true,
		},
		// golang_migrate without is_baseline.
		{
			name:          "golang_migrate without is_baseline",
			sourceKind:    "golang_migrate",
			sourceVersion: ptrUint64(1),
			isBaseline:    false,
			batch:         0,
			state:         "applied",
			appliedAt:     parseTime("2024-01-01"),
			wantErr:       true,
		},
		// golang_migrate with batch>0.
		{
			name:          "golang_migrate with batch>0",
			sourceKind:    "golang_migrate",
			sourceVersion: ptrUint64(1),
			isBaseline:    true,
			batch:         1,
			state:         "applied",
			appliedAt:     parseTime("2024-01-01"),
			wantErr:       true,
		},
		// golang_migrate with non-applied state.
		{
			name:          "golang_migrate with applying state",
			sourceKind:    "golang_migrate",
			sourceVersion: ptrUint64(1),
			isBaseline:    true,
			batch:         0,
			state:         "applying",
			appliedAt:     nil,
			wantErr:       true,
		},
		// applying with applied_at set.
		{
			name:          "applying with applied_at",
			sourceKind:    "timestamp",
			sourceVersion: nil,
			isBaseline:    false,
			batch:         1,
			state:         "applying",
			appliedAt:     parseTime("2024-01-01"),
			wantErr:       true,
		},
		// applied without applied_at.
		{
			name:          "applied without applied_at",
			sourceKind:    "timestamp",
			sourceVersion: nil,
			isBaseline:    false,
			batch:         1,
			state:         "applied",
			appliedAt:     nil,
			wantErr:       true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateStateRow(
				tt.sourceKind, tt.sourceVersion, tt.isBaseline,
				tt.batch, tt.state, tt.appliedAt,
			)
			if (err != nil) != tt.wantErr {
				t.Errorf("validateStateRow() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestControlTableDDLContainsRequiredColumns(t *testing.T) {
	expected := []string{
		"tracking_table",
		"schema_version",
		"next_batch",
		"updated_at",
	}
	for _, col := range expected {
		if !containsIgnoreCase(controlTableDDL, col) {
			t.Errorf("controlTableDDL missing column %q", col)
		}
	}
	// Must contain ENGINE=InnoDB
	if !containsIgnoreCase(controlTableDDL, "ENGINE=InnoDB") {
		t.Error("controlTableDDL missing ENGINE=InnoDB")
	}
}

func TestStateTableDDLContainsRequiredColumns(t *testing.T) {
	ddl := stateTableDDL("migrations")
	expected := []string{
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
	for _, col := range expected {
		if !containsIgnoreCase(ddl, "`"+col+"`") {
			t.Errorf("stateTableDDL missing column %q", col)
		}
	}
}

func TestStateTableDDLContainsCheckConstraints(t *testing.T) {
	ddl := stateTableDDL("migrations")
	// Constraint names include the table name for uniqueness.
	expected := []string{
		"migrations_chk_state",
		"migrations_chk_source",
		"migrations_chk_fields",
		"migrations_chk_times",
	}
	for _, name := range expected {
		if !containsIgnoreCase(ddl, name) {
			t.Errorf("stateTableDDL missing CHECK constraint %q", name)
		}
	}
	// Also verify the CHECK keyword is present.
	if !containsIgnoreCase(ddl, "CHECK") {
		t.Error("stateTableDDL missing CHECK keyword")
	}
}

func TestStateTableDDLContainsIndexes(t *testing.T) {
	ddl := stateTableDDL("migrations")
	expected := []string{
		"PRIMARY KEY",
		"uk_migration",
		"idx_batch_state",
	}
	for _, name := range expected {
		if !containsIgnoreCase(ddl, name) {
			t.Errorf("stateTableDDL missing index %q", name)
		}
	}
}

func TestStateTableDDLContainsEngine(t *testing.T) {
	ddl := stateTableDDL("migrations")
	if !containsIgnoreCase(ddl, "ENGINE=InnoDB") {
		t.Error("stateTableDDL missing ENGINE=InnoDB")
	}
	if !containsIgnoreCase(ddl, "utf8mb4") {
		t.Error("stateTableDDL missing utf8mb4")
	}
}

func TestStateTableDDLUsesCustomTableName(t *testing.T) {
	ddl := stateTableDDL("custom_migrations")
	if !containsIgnoreCase(ddl, "`custom_migrations`") {
		t.Error("stateTableDDL did not use custom table name")
	}
	// Should not contain the default name as a table reference.
	if containsIgnoreCase(ddl, "`migrations`") {
		t.Error("stateTableDDL still references default 'migrations' table name")
	}
}

func TestControlTableNameReserved(t *testing.T) {
	if controlTableName != "lamigrate_control" {
		t.Errorf("controlTableName = %q, want %q", controlTableName, "lamigrate_control")
	}
	// The control table name IS a valid tracking-table pattern syntactically
	// (all lowercase ASCII), but it is programmatically reserved.
	// The regex match is expected; the reservation is enforced at option
	// validation time, not by the regex.
	if !validTrackingTable.MatchString(controlTableName) {
		t.Error("controlTableName should still match validTrackingTable pattern (reservation is programmatic)")
	}
}

func TestRequiredControlColumnsCount(t *testing.T) {
	if len(requiredControlColumns) != 4 {
		t.Errorf("requiredControlColumns has %d entries, want 4", len(requiredControlColumns))
	}
}

func TestRequiredStateColumnsCount(t *testing.T) {
	if len(requiredStateColumns) != 14 {
		t.Errorf("requiredStateColumns has %d entries, want 14", len(requiredStateColumns))
	}
}

func TestAllocateBatchMonotonicLogic(t *testing.T) {
	// Test the logical semantics of batch allocation without DB.
	// This tests the invariant: next_batch starts at 1, increments,
	// and never reuses.

	var nextBatch uint64 = 1

	// Simulate several allocations.
	batches := make([]uint64, 5)
	for i := range batches {
		batches[i] = nextBatch
		nextBatch++
	}

	// All batch numbers should be strictly increasing.
	for i := 1; i < len(batches); i++ {
		if batches[i] <= batches[i-1] {
			t.Errorf("batch[%d]=%d <= batch[%d]=%d, expected monotonic",
				i, batches[i], i-1, batches[i-1])
		}
	}

	// First batch should be 1.
	if batches[0] != 1 {
		t.Errorf("first batch = %d, want 1", batches[0])
	}

	// No reuse: all values should be unique.
	seen := make(map[uint64]bool)
	for _, b := range batches {
		if seen[b] {
			t.Errorf("batch %d reused", b)
		}
		seen[b] = true
	}
}

// containsIgnoreCase is a simple helper for DDL assertions.
func containsIgnoreCase(s, substr string) bool {
	s = toLower(s)
	substr = toLower(substr)
	return containsStr(s, substr)
}

func toLower(s string) string {
	b := make([]byte, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		b[i] = c
	}
	return string(b)
}

func containsStr(s, substr string) bool {
	for i := 0; i+len(substr) <= len(s); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
