# LM-001 — Evidence: Offline, Library, and CLI Prototype Characterization

- Status: IN_PROGRESS → DONE (coordinator-executed)
- Owner: Coordinator (wave-1 subagent executed directly)
- Baseline: 73014dc (LM-004 reconciled baseline)
- Verification: go test -count=1 ./... PASS, go test -race -count=1 ./... PASS

## 1. Migration Creation (Offline)

### Behavior
- `CreateMigration(dir, name)` and `Migrate.Make(name)` are fully offline — no DSN or database needed.
- Timestamp format: `YYYYMMDDHHMMSS` via `time.Now().UTC().Format("20060102150405")`.
- Name normalization: lowercases, replaces `-` and space with `_`, trims `.sql`/`.up`/`.down` suffixes, collapses `__`.
- Name validation: `^[a-z][a-z0-9_]*$`, max 200 chars.
- Same-second collision: lock file (`O_EXCL`) + recheck after lock acquisition. Fails with "retry in one second."
- File publication: down file first, then up file (crash-safe: down-only orphan is harmless, up-only is never discoverable).
- Symlink rejection: walks path components, rejects non-system symlinks. macOS `/var` and `/tmp` are trusted.

### Templates
| Pattern | Template | Behavior |
|---------|----------|----------|
| `create_X_table` | create_table | Immediately runnable CREATE TABLE |
| `add_X_to_Y_table` | add_column | Guarded with SIGNAL SQLSTATE '45000' |
| `drop_X_from_Y_table` | drop_column | Guarded with SIGNAL SQLSTATE '45000' |
| Everything else | generic | Guarded with SIGNAL SQLSTATE '45000' |

### Gap: No file-size limit enforcement
Architecture §8.1 requires configurable maximum file size. Current code reads entire files into memory via `os.ReadFile` with no bound.

### Gap: No checksum calculation
Architecture §8.3 requires exact SHA-256 checksums over file bytes. No checksumming exists.

### Gap: No duplicate ID detection
`scanMigrations` uses a `seen` map keyed by base name. Two different filenames that parse to the same base name (e.g., via symlinks or case-insensitive filesystem) would silently share one entry. Architecture §8.1 requires duplicate ID rejection.

### Gap: No irreversible migration marker detection
Architecture §8.3 requires detection of `-- lamigrate: irreversible` markers. No parsing exists.

## 2. File Discovery and Sorting

### Behavior
- `scanMigrations`: matches `^(\d{14})_(.+)\.up\.sql$`, requires paired down file, rejects symlinks/non-regular files.
- `scanLegacyMigrations`: matches `^(\d{6})_(.+)\.(up|down)\.sql$`, skips 14-digit (to avoid false legacy match).
- Sort: by timestamp ascending, then by full name as tie-breaker.
- Down-only orphans are silently ignored (correct per architecture).
- Up-only files cause an error (correct: requires down pair).

### Gap: Sort stability
`sort.Slice` is not guaranteed stable. For equal timestamps AND equal names (shouldn't happen with validation, but defensive coding), order is undefined. Architecture §5.4 requires deterministic identity and order.

### Gap: Directory enumeration order dependency
Files are collected from `os.ReadDir` map iteration before sorting. The map keyed on base name means duplicate filenames are deduplicated — but legitimate different files with the same base name (from different directories or paths) would be silently merged.

## 3. CLI Parsing and Commands

### Behavior
- `splitArgs`: separates global flags (before first non-flag arg) from command + args.
- Global flags: `-dir`, `-dsn`, `-table`, `-pretend`/`--pretend` (with `=` form supported).
- Commands: `up [N]`, `down [N]`, `reset`, `status`, `migration create <name>`, `make <name>`, `make:migration <name>`, `import`.
- `parseN`: rejects 0, negative, non-integer, multiple args. Returns nil for no args (= all).
- Unknown flags: rejected with error. Unknown commands: rejected with error.
- Extra arguments on `reset`/`status`: rejected.

### Gap: No signal handling
Architecture §14 requires `SIGINT/SIGTERM` cancellation through `signal.NotifyContext`. Current code uses `context.Background()` with no cancellation.

### Gap: No confirmation for destructive operations
Architecture §14 requires `reset`, `import`, and repair to require explicit confirmation or `--yes`. Current code has no confirmation flow.

### Gap: No --json flag
Architecture §14 requires `--json` with versioned schema. Not implemented.

### Gap: No version command
Architecture §14 lists `lamigrate version`. Not implemented.

### Gap: No help command
No `--help`/`-h` flag. Usage prints on error only.

### Gap: Exit code taxonomy
Architecture §14 defines codes 0–4 (success, execution failure, usage error, lock timeout, dirty state). Current code uses exit(1) for all errors.

### Gap: No DSN file support
Architecture §15 mentions `--dsn-file`. Not implemented.

### Gap: DSN appears in -dsn flag
Architecture §5.8 requires DSNs/credentials not appear in process listings. The current `-dsn` flag is inline.

## 4. Library API (lamigrate.go)

### Behavior
- `New(dir, dsn)`: opens DB, Pings, ensures migrations table — immediately connected.
- `Up(ctx, n...)` / `Down(ctx, n...)` / `Reset(ctx)`: variadic step limit.
- `Status(ctx)`: returns `[]MigrationStatus`.
- `Make(name)`: delegates to `CreateMigration`.
- `ImportLegacy(ctx)`: marks legacy files as applied, batch 0.
- `PretendUp` / `PretendDown`: prints SQL without executing.

### Gap: Library writes to stdout
`Up`, `Down`, `Reset`, `PretendUp`, `PretendDown`, `ImportLegacy` all call `fmt.Println`/`fmt.Printf` directly. Architecture §6.2: "Library methods MUST NOT write directly to process stdout or stderr."

### Gap: No side-effect-free construction
`New()` immediately opens a DB connection and calls `ensureTable`. Architecture §6.2: `NewMySQL` must perform local validation only.

### Gap: No Options struct / StepLimit type
Architecture §7 defines `Options` (Directory, LegacyDir, TableName, LockTimeout, MaxFileSize) and `StepLimit` with `All()`/`Steps(n)`. Current API uses variadic int.

### Gap: No typed errors/sentinels
Architecture §16 requires `errors.Is`/`errors.As` compatible typed error categories. Current code wraps errors with `fmt.Errorf` only.

### Gap: No advisory lock
Architecture §10 requires MySQL advisory lock protocol. Not implemented.

### Gap: No metadata state machine
Architecture §9 defines applying/applied/apply_failed/rolling_back/rollback_failed states. Current code has only `migration` + `batch` columns with no state tracking.

### Gap: No checksums or drift detection
Architecture §8.3, §9: checksums stored in metadata, drift blocks all writes. Not implemented.

### Gap: No plan/execution parity
Architecture §5.5 requires dry-run and real execution use the same planner. Current PretendUp/PretendDown re-read files independently.

### Gap: No global integrity check
Architecture §11.1 step 4: all applied records must have source checksums verified, even when not selected. Not implemented.

## 5. Metadata Table (lamigrate.go ensureTable)

### Current schema
```sql
CREATE TABLE IF NOT EXISTS migrations (
    id         BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
    migration  VARCHAR(255) NOT NULL,
    batch      INT UNSIGNED NOT NULL,
    applied_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE KEY uk_migrations_migration (migration)
)
```

### Gaps vs architecture §9 target
| Aspect | Current | Target |
|--------|---------|--------|
| State field | absent | `state VARCHAR(24)` with CHECK constraint |
| Source tracking | absent | `source_kind`, `source_version`, `source_name` |
| Checksums | absent | `up_checksum BINARY(32)`, `down_checksum BINARY(32)` |
| Runner tracking | absent | `runner_id CHAR(36)` |
| Timestamps | `applied_at` only | `started_at`, `applied_at`, `updated_at` |
| Baseline flag | absent | `is_baseline BOOLEAN` |
| Control table | absent | `lamigrate_control` |
| Schema version | absent | `schema_version` via control row |
| Collation | `utf8mb4_general_ci` | `utf8mb4_0900_ai_ci` |
| CHECK constraints | absent | 4 CHECK constraints |
| InnoDB explicit | absent | explicit ENGINE=InnoDB |
| Table validation | none | semantic shape validation via information_schema |

### Gap: No control table
Architecture §9 requires `lamigrate_control` for schema versioning and batch-counter durability.

### Gap: No semantic validation
Architecture §9: startup must validate column type families, keys, constraints, InnoDB, no partitioning. Current code uses `CREATE TABLE IF NOT EXISTS` only.

## 6. Import (import.go)

### Behavior
- `importLegacy`: reads 6-digit numbered files, marks as applied in batch 0.
- Skips already-tracked migrations.
- No SQL execution.

### Gaps vs architecture §13
| Aspect | Current | Target |
|--------|---------|--------|
| Source metadata table | absent | Must read golang-migrate `schema_migrations` table |
| Version/dirty check | absent | Must validate source is clean |
| Source quiescence | absent | `--source-quiesced` attestation required |
| Checksums | absent | Exact up/down checksums |
| Atomic transaction | absent | Single metadata transaction |
| Version validation | absent | Numeric version validation, sparse-gap reporting |
| Future version blocking | absent | Versions > DB version must be resolved |
| Empty/conflict handling | absent | Must handle empty set or exact previously imported set |
| Retry safety | absent | Idempotent retry for same source |
| Preview | absent | `PreviewGolangMigrateImport` |
| Separate legacy dir | absent | Uses same directory, not `LegacyDir` |

## 7. Config Loader (cmd/config.go)

### Behavior (already committed baseline)
- Precedence: explicit -dsn → LAMIGRATE_DSN → config file search (config.yaml → config.yml → .env).
- YAML: strict `dbMySQL` mapping with host, timeout, port, user, pass, dbName.
- .env: accepts LAMIGRATE_DSN directly, or individual DB_* vars with multiple key fallbacks.
- File constraints: regular file, max 1MB.
- DSN construction: go-sql-driver/mysql with MultiStatements=true, ParseTime=true.

### Note
This code is part of the committed baseline. LM-003 will propose whether to adopt/revert. LM-013 implements the approved policy.

### Gap vs architecture §15
- No `--dsn-file` support.
- No project-root/config-path discovery rule (only searches current directory).
- No TLS documentation/policy.
- No remote-host warning.
- Password may appear in config file but no redaction in logs/errors (need to verify).

## 8. Regression Cases (Architecture §4 Gaps)

| Gap | Reproducible Without DB? | Status |
|-----|--------------------------|--------|
| No advisory lock or dirty-state protocol | No (LM-005) | Documented |
| Migration SQL and tracking diverge after crashes | No (LM-005) | Documented |
| Custom tracking table selected after default is created | Yes: `Table()` mutates after construction | Confirmed in code |
| Tracking-table identifiers interpolated without validation | Yes: `ensureTable` uses raw tableName in SQL | Confirmed in code |
| Invalid numeric CLI input broadens scope | Yes: `parseN` rejects 0, negative, multiple args | Already mitigated |
| Reset dry-run shows only last batch | Yes: `PretendDown` calls `appliedInLastBatch` only | Confirmed in code |
| Legacy import doesn't reconcile golang-migrate version/dirty | Yes: `importLegacy` has no source validation | Confirmed in code |
| File creation can overwrite same-second migrations | Partially mitigated: lock + recheck, but not atomic | Partial — retry only |
| No checksums or drift detection | Yes: no checksum code exists | Confirmed absent |
| Library path doesn't register MySQL driver | No: import.go has blank import of go-sql-driver/mysql | Already mitigated |
| Module path doesn't match public repository | No: go.mod uses `github.com/rajifafif/lamigrate` | Matches |
| No automated tests, CI, release artifacts | Partially: tests exist but no CI/release | Tests present; no CI/release |

## 9. Summary of Findings

### Total gaps identified: 25+
Critical architectural gaps (must fix before production):
1. No advisory lock protocol
2. No metadata state machine (dirty state)
3. No side-effect-free construction
4. No typed errors
5. No checksums/drift detection
6. No plan/execution parity
7. No signal handling
8. Library writes to stdout
9. No confirmation for destructive ops
10. No JSON output mode
11. No exit code taxonomy

Existing mitigation (already correct):
1. Migration creation is offline
2. Name validation rejects unsafe names
3. Same-second collision handling
4. File publication order (down-first)
5. Symlink rejection
6. `parseN` rejects 0/negative/multiple args
7. Unknown flags/commands rejected
8. Down-only orphans ignored
9. Module path matches public repository
10. MySQL driver registered via blank import

### For downstream tasks
- LM-010 (API boundaries): Current API must be replaced per architecture §7. The variadic-int pattern, Options struct, StepLimit, typed errors, and no-stdout constraint all require new code.
- LM-011 (source contract): File discovery mostly correct but needs checksums, file-size limits, duplicate ID rejection, and irreversible marker detection.
- LM-012 (CLI foundation): Parsing is reasonable foundation but needs signal handling, exit codes, confirmation, JSON, version, help, and DSN-file support.
- LM-013 (config): Config loader exists as baseline; policy decision pending from LM-003/LM-000.
