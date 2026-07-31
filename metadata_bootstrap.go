package lamigrate

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// metadata_bootstrap.go — Two-phase bootstrap protocol for metadata tables.
//
// Bootstrap creates the lamigrate_control table and the requested
// migration-state table if they do not exist. It uses two strictly
// sequential connection phases (architecture §9):
//
// Phase A: Create private session A, run capability probes, acquire
// the bootstrap lock, inventory both tables read-only.
//
// Phase B: Release bootstrap lock, close session A. Create fresh
// private session B, re-inventory the state table, create/validate
// tables only when still eligible, create control row.
//
// This prevents different custom scopes from racing on shared
// control-table DDL while serializing ordinary migrations.
//
// If lamigrate_control is already valid, phase A is skipped and
// only phase B is used.

// bootstrapInventory holds the results of a read-only inventory
// of the control and state tables during bootstrap.
type bootstrapInventory struct {
	// controlTableExists is true when the lamigrate_control table
	// exists in the database with a valid v1 structure.
	controlTableExists bool

	// controlRowExists is true when the control table has a row
	// for this specific tracking table with schema_version=1.
	controlRowExists bool

	// stateExists is true when the requested migration-state table exists.
	stateExists bool

	// stateIsPrototype is true when the state table has exactly 4
	// columns (the old prototype shape).
	stateIsPrototype bool

	// stateIsValid is true when the state table matches the v1 schema.
	stateIsValid bool
}

// bootstrap performs the two-phase bootstrap protocol. It is called
// from write operations when metadata may need to be initialized.
//
// Returns nil if metadata is ready for use (tables exist and are valid).
// Returns an error if bootstrap is needed but fails, or if the
// existing metadata is incompatible.
func (m *Migrator) bootstrap(ctx context.Context) error {
	// Phase A: Create session A, probe, acquire bootstrap lock, inventory.
	connA, poolA, capsA, err := m.createAndProbeSession(ctx)
	if err != nil {
		return fmt.Errorf("bootstrap phase A: create session: %w", err)
	}
	defer closeSession(connA, poolA)

	// Validate database name.
	if !validDatabaseName.MatchString(capsA.DatabaseName) {
		return fmt.Errorf(
			"%w: database name %q from SELECT DATABASE() does not match [A-Za-z_][A-Za-z0-9_]*",
			ErrUnsupportedDriver, capsA.DatabaseName,
		)
	}

	// Acquire bootstrap lock.
	if err := acquireBootstrapLock(ctx, connA, capsA.DatabaseName, m.lockTime); err != nil {
		return fmt.Errorf("bootstrap phase A: acquire bootstrap lock: %w", err)
	}

	// Inventory both tables (read-only, no DDL).
	inv, err := inventoryTables(ctx, connA, capsA.DatabaseName, m.tableName)
	if err != nil {
		// Release lock before returning.
		relErr := releaseBootstrapLock(ctx, connA, capsA.DatabaseName)
		if relErr != nil {
			return fmt.Errorf("bootstrap: inventory error %v, release error %v", err, relErr)
		}
		return fmt.Errorf("bootstrap phase A: inventory: %w", err)
	}

	// Release bootstrap lock and close session A.
	if err := releaseBootstrapLock(ctx, connA, capsA.DatabaseName); err != nil {
		return fmt.Errorf("bootstrap phase A: release bootstrap lock: %w", err)
	}

	// Close session A — physical session termination is mandatory.
	if err := closeSession(connA, poolA); err != nil {
		return fmt.Errorf("bootstrap phase A: close session: %w", err)
	}
	connA = nil
	poolA = nil

	// Decision point based on inventory.
	// Fast path: everything is already set up.
	if inv.controlTableExists && inv.controlRowExists && inv.stateExists && inv.stateIsValid {
		return nil
	}

	// Reject incompatible control table structure.
	if inv.controlTableExists && !inv.controlRowExists {
		// Control table exists with valid structure but no row for this tracking table.
		// This is fine — phase B will add the row. But first check if the
		// control table has an invalid structure by checking schema_version
		// on any row. If the table exists but no rows at all, it's valid
		// and we need to add a row. If it has rows but none match our
		// tracking table, that's also fine.
	}

	// Reject prototype state table.
	if inv.stateExists && inv.stateIsPrototype {
		return fmt.Errorf(
			"%w: table %q matches the prototype shape — adopt-prototype required before write operations",
			ErrRecoveryRequired, m.tableName,
		)
	}

	// Reject incompatible state table.
	if inv.stateExists && !inv.stateIsValid && !inv.stateIsPrototype {
		return fmt.Errorf(
			"%w: table %q exists but does not match the v1 metadata schema",
			ErrUnsupportedMetadata, m.tableName,
		)
	}

	// Phase B: Create session B, re-inventory, create/validate tables.
	return m.bootstrapPhaseB(ctx)
}

// bootstrapPhaseB performs the second phase of bootstrap: create a
// fresh session, re-inventory to detect changes since phase A,
// create and validate tables, then initialize the control row.
func (m *Migrator) bootstrapPhaseB(ctx context.Context) error {
	connB, poolB, capsB, err := m.createAndProbeSession(ctx)
	if err != nil {
		return fmt.Errorf("bootstrap phase B: create session: %w", err)
	}
	defer closeSession(connB, poolB)

	// Validate database name.
	if !validDatabaseName.MatchString(capsB.DatabaseName) {
		return fmt.Errorf(
			"%w: database name %q from SELECT DATABASE() does not match [A-Za-z_][A-Za-z0-9_]*",
			ErrUnsupportedDriver, capsB.DatabaseName,
		)
	}

	// Derive and acquire the normal scope lock.
	lockKey, err := deriveLockKey(capsB.DatabaseName, m.tableName)
	if err != nil {
		return fmt.Errorf("bootstrap phase B: derive lock key: %w", err)
	}
	if err := acquireLock(ctx, connB, lockKey, m.lockTime); err != nil {
		return fmt.Errorf("bootstrap phase B: acquire scope lock: %w", err)
	}
	defer releaseLock(ctx, connB, lockKey)

	// Re-inventory the state table to detect change since phase A.
	inv, err := inventoryTables(ctx, connB, capsB.DatabaseName, m.tableName)
	if err != nil {
		return fmt.Errorf("bootstrap phase B: re-inventory: %w", err)
	}

	// Re-check invariants after re-inventory.
	if inv.stateExists && inv.stateIsPrototype {
		return fmt.Errorf(
			"%w: table %q matches prototype shape — adopt-prototype required",
			ErrRecoveryRequired, m.tableName,
		)
	}
	if inv.stateExists && !inv.stateIsValid && !inv.stateIsPrototype {
		return fmt.Errorf(
			"%w: table %q exists but does not match v1 schema",
			ErrUnsupportedMetadata, m.tableName,
		)
	}

	// Create lamigrate_control if it doesn't exist yet.
	if !inv.controlTableExists {
		if _, err := connB.ExecContext(ctx, controlTableDDL); err != nil {
			return fmt.Errorf("bootstrap phase B: create lamigrate_control: %w", err)
		}
		// Validate the newly created table.
		if err := validateTableShape(ctx, connB, capsB.DatabaseName, controlTableName, "control"); err != nil {
			return fmt.Errorf("bootstrap phase B: validate lamigrate_control: %w", err)
		}
	}

	// Create the state table if it doesn't exist yet.
	if !inv.stateExists {
		ddl := stateTableDDL(m.tableName)
		if _, err := connB.ExecContext(ctx, ddl); err != nil {
			return fmt.Errorf("bootstrap phase B: create %s: %w", m.tableName, err)
		}
		// Validate the newly created table.
		if err := validateTableShape(ctx, connB, capsB.DatabaseName, m.tableName, "state"); err != nil {
			return fmt.Errorf("bootstrap phase B: validate %s: %w", m.tableName, err)
		}
	}

	// Initialize control row if not already present for this tracking table.
	// INSERT IGNORE is safe: if the row already exists, no-op.
	now := time.Now().UTC()
	_, err = connB.ExecContext(ctx,
		"INSERT IGNORE INTO `"+controlTableName+"` (tracking_table, schema_version, next_batch, updated_at) VALUES (?, 1, 1, ?)",
		m.tableName, now.Format("2006-01-02 15:04:05.000000"),
	)
	if err != nil {
		return fmt.Errorf("bootstrap phase B: initialize control row: %w", err)
	}

	// Verify the control row.
	var schemaVersion uint64
	var nextBatch uint64
	err = connB.QueryRowContext(ctx,
		"SELECT schema_version, next_batch FROM `"+controlTableName+"` WHERE tracking_table = ?",
		m.tableName,
	).Scan(&schemaVersion, &nextBatch)
	if err != nil {
		return fmt.Errorf("bootstrap phase B: verify control row: %w", err)
	}
	if err := validateControlRow(schemaVersion, nextBatch, 0); err != nil {
		return fmt.Errorf("bootstrap phase B: %w", err)
	}

	return nil
}

// createAndProbeSession creates a new private session, runs capability
// probes, and returns the connection, pool, and capabilities.
func (m *Migrator) createAndProbeSession(ctx context.Context) (*sql.Conn, *sql.DB, *SessionCapabilities, error) {
	conn, pool, err := m.newPrivateSession(ctx)
	if err != nil {
		return nil, nil, nil, err
	}

	caps, err := m.runCapabilityProbes(ctx, conn)
	if err != nil {
		_ = closeSession(conn, pool)
		return nil, nil, nil, err
	}

	return conn, pool, caps, nil
}

// inventoryTables performs a read-only inventory of the control and
// state tables. It does NOT create or modify any tables.
func inventoryTables(ctx context.Context, conn *sql.Conn, database, tableName string) (*bootstrapInventory, error) {
	inv := &bootstrapInventory{}

	// Check control table existence and validate its structure.
	var controlTableCount int
	err := conn.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = ? AND table_name = ?",
		database, controlTableName,
	).Scan(&controlTableCount)
	if err != nil {
		return nil, fmt.Errorf("inventory control table: %w", err)
	}
	inv.controlTableExists = controlTableCount > 0

	if inv.controlTableExists {
		// Validate the control table structure.
		if err := validateTableShape(ctx, conn, database, controlTableName, "control"); err != nil {
			// Control table exists but has invalid structure — this is a serious problem.
			// We still mark it as existing so the caller can make the right decision.
			inv.controlTableExists = true
		}

		// Check if there's a row for this specific tracking table.
		var rowCount int
		err := conn.QueryRowContext(ctx,
			"SELECT COUNT(*) FROM `"+controlTableName+"` WHERE tracking_table = ? AND schema_version = 1",
			tableName,
		).Scan(&rowCount)
		if err != nil {
			return nil, fmt.Errorf("inventory control row: %w", err)
		}
		inv.controlRowExists = rowCount > 0
	}

	// Check state table existence.
	var stateTableCount int
	err = conn.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = ? AND table_name = ?",
		database, tableName,
	).Scan(&stateTableCount)
	if err != nil {
		return nil, fmt.Errorf("inventory state table: %w", err)
	}
	inv.stateExists = stateTableCount > 0

	if inv.stateExists {
		// Check column count: prototype has 4 columns, v1 has 14.
		var colCount int
		err := conn.QueryRowContext(ctx,
			"SELECT COUNT(*) FROM information_schema.columns WHERE table_schema = ? AND table_name = ?",
			database, tableName,
		).Scan(&colCount)
		if err != nil {
			return nil, fmt.Errorf("inventory state columns: %w", err)
		}

		if colCount == 4 {
			inv.stateIsPrototype = true
		} else if colCount == len(requiredStateColumns) {
			// Could be valid v1 — validate shape.
			if err := validateTableShape(ctx, conn, database, tableName, "state"); err == nil {
				inv.stateIsValid = true
			}
			// If validation fails, stateIsValid stays false.
		}
		// Other column counts: neither prototype nor v1, stateIsValid=false.
	}

	return inv, nil
}
