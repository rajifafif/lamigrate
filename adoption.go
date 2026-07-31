package lamigrate

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"strings"
	"time"
)

// AdoptionRequest configures the prototype adoption operation.
// BackupTable is the operator-selected name for the retained prototype
// backup. Directory is the migration directory for timestamp sources.
// LegacyDir is the legacy directory for numeric sources.
type AdoptionRequest struct {
	BackupTable string
	Directory   string
	LegacyDir   string
}

// AdoptionPlanItem describes one row in the adoption plan.
type AdoptionPlanItem struct {
	PrototypeID  uint64
	Migration    string
	Batch        uint64
	AppliedAt    string
	SourceKind   string // "timestamp" or "golang_migrate"
	SourceName   string
	UpChecksum   string // lowercase hex
	DownChecksum string // lowercase hex
}

// AdoptionPlanView is a read-only view of the adoption plan.
type AdoptionPlanView struct {
	DryRun        bool
	PrototypeRows int
	BackupTable   string
	Items         []AdoptionPlanItem
	MaxBatch      uint64
	NextBatch     uint64
	TempTable     string
}

// collisionResistantTempName generates a collision-resistant temporary table
// name for adoption by hashing backupTable+tableName.
func collisionResistantTempName(backupTable, tableName string) string {
	h := sha256.Sum256([]byte(backupTable + tableName))
	return "lamigrate_adopt_" + hex.EncodeToString(h[:8])
}

// ensureControlTableForAdoption ensures the lamigrate_control table exists
// without going through the normal bootstrap protocol (which would reject
// the prototype shape). This is used exclusively by adoption operations
// that need the control table before the prototype is converted.
func ensureControlTableForAdoption(ctx context.Context, conn *sql.Conn) error {
	if _, err := conn.ExecContext(ctx, controlTableDDL); err != nil {
		return fmt.Errorf("ensure control table: %w", err)
	}
	return nil
}

// PreviewPrototypeAdoption returns a read-only plan of the prototype
// adoption operation. It acquires the advisory lock for read consistency
// but performs no metadata DDL/DML and creates no database objects.
func (m *Migrator) PreviewPrototypeAdoption(
	ctx context.Context,
	request AdoptionRequest,
) (AdoptionPlanView, error) {
	// Validate request fields before creating a connector.
	if err := validateAdoptionRequest(request, m.tableName); err != nil {
		return AdoptionPlanView{}, err
	}

	var view AdoptionPlanView

	err := m.withLockSession(ctx, func(ctx context.Context, conn *sql.Conn, caps *SessionCapabilities) error {
		// Detect prototype shape under lock.
		isProto, err := detectPrototypeShape(ctx, conn, caps.DatabaseName, m.tableName)
		if err != nil {
			return fmt.Errorf("preview adoption: detect prototype: %w", err)
		}
		if !isProto {
			interrupted, detErr := detectInterruptedAdoption(ctx, conn, caps.DatabaseName, m.tableName)
			if detErr != nil {
				return fmt.Errorf("preview adoption: detect interrupted: %w", detErr)
			}
			if interrupted {
				return fmt.Errorf(
					"%w: table %q has interrupted adoption",
					ErrInterruptedPrototypeAdoption, m.tableName,
				)
			}
			return fmt.Errorf(
				"%w: table %q is not a 4-column prototype",
				ErrRecoveryRequired, m.tableName,
			)
		}

		// Read prototype rows and validate.
		protoRows, err := readPrototypeRows(ctx, conn, m.tableName)
		if err != nil {
			return fmt.Errorf("preview adoption: read rows: %w", err)
		}
		if err := validatePrototypeRows(protoRows); err != nil {
			return fmt.Errorf("preview adoption: validate rows: %w", err)
		}

		// Map source files.
		directory := request.Directory
		if directory == "" {
			directory = m.directory
		}
		legacyDir := request.LegacyDir
		if legacyDir == "" {
			legacyDir = m.legacyDir
		}
		mappings, err := mapSourceFiles(protoRows, directory, legacyDir)
		if err != nil {
			return fmt.Errorf("preview adoption: map sources: %w", err)
		}

		var maxBatch uint64
		for _, sm := range mappings {
			if sm.Batch > maxBatch {
				maxBatch = sm.Batch
			}
		}
		nextBatch := maxBatch + 1
		if nextBatch < 1 {
			nextBatch = 1
		}

		items := make([]AdoptionPlanItem, len(mappings))
		for i, sm := range mappings {
			items[i] = AdoptionPlanItem{
				PrototypeID:  sm.PrototypeID,
				Migration:    sm.Migration,
				Batch:        sm.Batch,
				AppliedAt:    sm.AppliedAt.Format("2006-01-02 15:04:05.000000"),
				SourceKind:   sm.SourceKind,
				SourceName:   sm.SourceName,
				UpChecksum:   checksumHex(sm.UpChecksum),
				DownChecksum: checksumHex(sm.DownChecksum),
			}
		}

		tempName := collisionResistantTempName(request.BackupTable, m.tableName)

		view = AdoptionPlanView{
			DryRun:        true,
			PrototypeRows: len(protoRows),
			BackupTable:   request.BackupTable,
			Items:         items,
			MaxBatch:      maxBatch,
			NextBatch:     nextBatch,
			TempTable:     tempName,
		}
		return nil
	})
	if err != nil {
		return AdoptionPlanView{}, err
	}
	return view, nil
}

// AdoptPrototype performs the explicit prototype adoption operation
// defined in architecture §9.3.
func (m *Migrator) AdoptPrototype(
	ctx context.Context,
	request AdoptionRequest,
) (Result, error) {
	// Validate request fields before creating a connector.
	if err := validateAdoptionRequest(request, m.tableName); err != nil {
		return Result{}, err
	}

	var result Result
	result.Command = "adopt-prototype"

	err := m.withLockSession(ctx, func(ctx context.Context, conn *sql.Conn, caps *SessionCapabilities) error {
		// a. Verify prototype shape (re-inventory under lock).
		isProto, err := detectPrototypeShape(ctx, conn, caps.DatabaseName, m.tableName)
		if err != nil {
			return fmt.Errorf("adopt-prototype: detect prototype: %w", err)
		}
		if !isProto {
			interrupted, detErr := detectInterruptedAdoption(ctx, conn, caps.DatabaseName, m.tableName)
			if detErr != nil {
				return fmt.Errorf("adopt-prototype: detect interrupted: %w", detErr)
			}
			if interrupted {
				return doRecoverAdoption(ctx, conn, caps, m, request)
			}
			return fmt.Errorf(
				"%w: table %q is not a 4-column prototype",
				ErrRecoveryRequired, m.tableName,
			)
		}

		// Ensure the control table exists. We can't use normal bootstrap
		// because it rejects the prototype shape before creating the
		// control table. Instead, we create it directly.
		if err := ensureControlTableForAdoption(ctx, conn); err != nil {
			return fmt.Errorf("adopt-prototype: %w", err)
		}

		// b. Verify backup table does not exist.
		var backupExists int
		err = conn.QueryRowContext(ctx,
			"SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = ? AND table_name = ?",
			caps.DatabaseName, request.BackupTable,
		).Scan(&backupExists)
		if err != nil {
			return fmt.Errorf("adopt-prototype: check backup table: %w", err)
		}
		if backupExists > 0 {
			return fmt.Errorf(
				"%w: backup table %q already exists",
				ErrInvalidConfig, request.BackupTable,
			)
		}

		// c. Read prototype rows, validate, map sources.
		protoRows, err := readPrototypeRows(ctx, conn, m.tableName)
		if err != nil {
			return fmt.Errorf("adopt-prototype: read rows: %w", err)
		}
		if err := validatePrototypeRows(protoRows); err != nil {
			return fmt.Errorf("adopt-prototype: validate rows: %w", err)
		}
		directory := request.Directory
		if directory == "" {
			directory = m.directory
		}
		legacyDir := request.LegacyDir
		if legacyDir == "" {
			legacyDir = m.legacyDir
		}
		mappings, err := mapSourceFiles(protoRows, directory, legacyDir)
		if err != nil {
			return fmt.Errorf("adopt-prototype: map sources: %w", err)
		}

		// d. Create collision-resistant temporary table name.
		tempName := collisionResistantTempName(request.BackupTable, m.tableName)

		// e. Create the temp v1 state table.
		ddl := stateTableDDL(tempName)
		if _, err := conn.ExecContext(ctx, ddl); err != nil {
			return fmt.Errorf("adopt-prototype: create temp table: %w", err)
		}

		// f. Validate the temp table shape.
		if err := validateTableShape(ctx, conn, caps.DatabaseName, tempName, "state"); err != nil {
			return fmt.Errorf("adopt-prototype: validate temp table: %w", err)
		}

		// g. Copy records in ascending prototype id order.
		now := time.Now().UTC()
		runnerID := generateRunnerID()
		for _, sm := range mappings {
			var sourceVersion sql.NullInt64
			isBaseline := sm.Batch == 0
			var downChecksum sql.NullString
			if sm.DownChecksum != [32]byte{} {
				downChecksum = sql.NullString{
					String: string(sm.DownChecksum[:]),
					Valid:  true,
				}
			}
			upChecksum := string(sm.UpChecksum[:])

			if sm.SourceKind == "golang_migrate" {
				ver := mustParseUint(sm.Migration)
				sourceVersion = sql.NullInt64{Int64: int64(ver), Valid: true}
			}

			_, err := conn.ExecContext(ctx,
				fmt.Sprintf(
					"INSERT INTO `%s` (id, migration, source_kind, source_version, source_name, "+
						"up_checksum, down_checksum, batch, state, is_baseline, runner_id, "+
						"started_at, applied_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, 'applied', ?, ?, ?, ?, ?)",
					tempName,
				),
				sm.PrototypeID, sm.Migration, sm.SourceKind,
				sourceVersion, sm.SourceName,
				upChecksum, downChecksum,
				sm.Batch, isBaseline,
				runnerID, sm.AppliedAt, sm.AppliedAt, now,
			)
			if err != nil {
				return fmt.Errorf("adopt-prototype: insert into temp: %w", err)
			}
		}

		// h. Set AUTO_INCREMENT above max preserved ID.
		var maxID uint64
		for _, sm := range mappings {
			if sm.PrototypeID > maxID {
				maxID = sm.PrototypeID
			}
		}
		if _, err := conn.ExecContext(ctx,
			fmt.Sprintf("ALTER TABLE `%s` AUTO_INCREMENT = %d", tempName, maxID+1),
		); err != nil {
			return fmt.Errorf("adopt-prototype: set auto_increment: %w", err)
		}

		// i. Verify count, exact IDs, and identities.
		var tempCount int
		err = conn.QueryRowContext(ctx,
			fmt.Sprintf("SELECT COUNT(*) FROM `%s`", tempName),
		).Scan(&tempCount)
		if err != nil {
			return fmt.Errorf("adopt-prototype: verify count: %w", err)
		}
		if tempCount != len(mappings) {
			return fmt.Errorf(
				"%w: temp count %d != expected %d",
				ErrRecoveryRequired, tempCount, len(mappings),
			)
		}

		vRows, err := conn.QueryContext(ctx,
			fmt.Sprintf("SELECT id, migration FROM `%s` ORDER BY id ASC", tempName),
		)
		if err != nil {
			return fmt.Errorf("adopt-prototype: verify IDs: %w", err)
		}
		defer vRows.Close()

		idx := 0
		for vRows.Next() {
			var id uint64
			var migration string
			if err := vRows.Scan(&id, &migration); err != nil {
				return fmt.Errorf("adopt-prototype: verify IDs scan: %w", err)
			}
			if idx >= len(mappings) {
				return fmt.Errorf("%w: extra rows in temp table", ErrRecoveryRequired)
			}
			if id != mappings[idx].PrototypeID {
				return fmt.Errorf(
					"%w: id mismatch at %d: got %d want %d",
					ErrRecoveryRequired, idx, id, mappings[idx].PrototypeID,
				)
			}
			if migration != mappings[idx].Migration {
				return fmt.Errorf(
					"%w: migration mismatch at id %d: got %q want %q",
					ErrRecoveryRequired, id, migration, mappings[idx].Migration,
				)
			}
			idx++
		}
		if err := vRows.Err(); err != nil {
			return fmt.Errorf("adopt-prototype: iterate verify: %w", err)
		}

		// Compute expected next_batch.
		var maxBatch uint64
		for _, sm := range mappings {
			if sm.Batch > maxBatch {
				maxBatch = sm.Batch
			}
		}
		nextBatch := maxBatch + 1
		if nextBatch < 1 {
			nextBatch = 1
		}

		// j. Atomic swap: RENAME TABLE prototype TO backup, temp TO target.
		if _, err := conn.ExecContext(ctx, fmt.Sprintf(
			"RENAME TABLE `%s` TO `%s`, `%s` TO `%s`",
			m.tableName, request.BackupTable, tempName, m.tableName,
		)); err != nil {
			return fmt.Errorf("adopt-prototype: atomic swap: %w", err)
		}

		// k. Create control row.
		if _, err := conn.ExecContext(ctx,
			"INSERT INTO `"+controlTableName+"` (tracking_table, schema_version, next_batch, updated_at)"+
				" VALUES (?, 1, ?, ?)",
			m.tableName, nextBatch, now.Format("2006-01-02 15:04:05.000000"),
		); err != nil {
			return fmt.Errorf("adopt-prototype: create control row: %w", err)
		}

		// l. Re-read and verify control row.
		cr, verifyMaxPB, err := readControlRow(ctx, conn, m.tableName)
		if err != nil {
			return fmt.Errorf("adopt-prototype: verify control row: %w", err)
		}
		if err := validateControlRow(cr.SchemaVersion, cr.NextBatch, verifyMaxPB); err != nil {
			return fmt.Errorf("adopt-prototype: validate control row: %w", err)
		}
		if cr.NextBatch != nextBatch {
			return fmt.Errorf(
				"%w: next_batch=%d != expected %d",
				ErrRecoveryRequired, cr.NextBatch, nextBatch,
			)
		}

		return nil
	})
	if err != nil {
		return Result{}, err
	}
	return result, nil
}

// validateAdoptionRequest validates all fields of an AdoptionRequest.
func validateAdoptionRequest(request AdoptionRequest, trackingTable string) error {
	if err := validateBackupTableName(request.BackupTable, trackingTable); err != nil {
		return fmt.Errorf("adopt-prototype: %w", err)
	}
	if strings.TrimSpace(request.Directory) == "" {
		return fmt.Errorf(
			"%w: Directory must not be empty for adoption",
			ErrInvalidConfig,
		)
	}
	if strings.TrimSpace(request.LegacyDir) == "" {
		return fmt.Errorf(
			"%w: LegacyDir must not be empty for adoption",
			ErrInvalidConfig,
		)
	}
	return nil
}
