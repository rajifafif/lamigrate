# lamigrate Production Architecture

Status: Review candidate

Last updated: 2026-07-30

Scope: Target architecture for the first production-capable release of lamigrate.

Implementation status: The current repository is a prototype and does not yet implement this architecture. This document defines the intended target and migration path; it must not be used as evidence that production-safety guarantees already exist.

## 1. Purpose

lamigrate is a MySQL migration tool for Go projects that provides a Laravel-style workflow:

- timestamp-named up/down SQL files;
- batch-based migration and rollback;
- status and dry-run planning;
- offline migration-file creation;
- controlled import from golang-migrate.

The production design prioritizes database integrity, deterministic behavior, recoverability, and operator clarity over feature count.

Normative terms such as MUST, MUST NOT, SHOULD, and MAY describe target behavior. They become release requirements only after this document is approved.

## 2. Goals

The production architecture MUST provide:

1. Exactly one lamigrate writer per database and tracking-table scope.
2. Fail-closed behavior for invalid input, drift, dirty state, missing files, and ambiguous migration order.
3. A durable record before migration SQL begins so interrupted work is visible and recoverable.
4. Deterministic planning and execution from the same immutable migration plan.
5. Strict separation between reusable library behavior and CLI presentation.
6. Side-effect-free construction and offline commands.
7. Exact migration-file checksums and visible drift detection.
8. Preflight validation before any forward or rollback SQL is executed.
9. Signal-aware cancellation with an explicit dirty state when completion is uncertain.
10. Reproducible, scanned, checksummed release artifacts.

## 3. Non-goals for the first production release

The first production release will not attempt to provide:

- PostgreSQL, SQLite, or MariaDB compatibility without a dedicated design and CI matrix;
- distributed coordination across different migration tools;
- automatic recovery from partially executed MySQL DDL;
- automatic SQL rewriting or a generic semicolon-based SQL parser;
- a sandbox for untrusted migration files;
- online-schema-change orchestration;
- a graphical interface;
- an application-level schema builder.

Migration files are trusted source code. Anyone able to modify a migration file can execute arbitrary SQL using the configured database credentials.

## 4. Current-state gap

The current prototype has a small and understandable implementation, but production release is blocked by these architectural gaps:

- no database advisory lock or dirty-state protocol;
- migration SQL and tracking changes can diverge after crashes or errors;
- the custom tracking table is selected after the default table is created;
- tracking-table identifiers are interpolated without validation;
- invalid numeric CLI input can broaden migration or rollback scope;
- reset dry-run shows only the last batch;
- legacy import does not reconcile golang-migrate version and dirty state;
- file creation can overwrite same-second migrations;
- applied migrations have no checksums or drift detection;
- the documented library path does not register the hard-coded MySQL driver;
- the documented module/install path does not match the public repository;
- there are no automated tests, CI gates, or release artifacts.

The implementation must be aligned incrementally. Approval of this architecture does not authorize an unreviewed big-bang rewrite.

## 5. Safety invariants

Every implementation and review MUST preserve the following invariants.

### 5.1 Single writer

Only one up, down, reset, import, adoption, or repair operation may modify a given database/tracking-table scope at a time, provided every migration obeys the trusted-SQL restrictions in §10.2. Migration SQL that releases lamigrate's lock or modifies its metadata is outside the supported safety contract.

The lock scope is derived from:

- the active MySQL database name; and
- the validated tracking-table name.

### 5.2 Intent before SQL

Before executing migration SQL, lamigrate MUST durably record that the migration is being applied or rolled back. A crash must leave a non-clean state that blocks later writes until an operator reconciles it.

### 5.3 Fail closed

Malformed counts, unknown flags, extra arguments, invalid names, drift, lock failure, unsupported metadata schema, and missing rollback files MUST return an error. They MUST NOT be converted into “all migrations” or “rollback the whole batch.”

### 5.4 Deterministic identity and order

Each migration has one canonical ID derived from its filename. Discovery and ordering must not depend on map iteration, filesystem enumeration order, locale, or case-insensitive database collation.

### 5.5 Plan/execution parity

Dry-run and real execution MUST use the same planner and validation rules. Execution consumes the exact plan and SQL bytes produced during preflight; it must not silently rediscover different files after displaying a plan.

### 5.6 Drift visibility

Once applied, changing either the up or down file is drift. Drift MUST appear in status and MUST block all write operations until explicitly reconciled.

### 5.7 No implicit destructive recovery

lamigrate must never guess whether partially executed DDL succeeded. Recovery requires an explicit operator decision backed by inspection of the actual database.

### 5.8 Secret-safe diagnostics

DSNs and credentials MUST NOT appear in normal logs, structured output, errors, or release diagnostics. SQL is shown only by an explicit dry-run mode and is treated as potentially sensitive output.

## 6. Component architecture

```text
+----------------------------+
| cmd/lamigrate              |
| CLI parsing, config, UX,   |
| signals, exit codes        |
+-------------+--------------+
              |
              v
+----------------------------+
| Public lamigrate package   |
| Options, Plan, Result,     |
| typed errors, orchestration|
+------+------+--------------+
       |      |          |
       v      v          v
+---------+ +---------+ +----------------+
| Source  | | Planner | | MySQL runtime  |
| scan,   | | status, | | dedicated conn,|
| validate| | preflight| | lock, metadata,|
| checksum| | ordering| | SQL execution  |
+---------+ +---------+ +----------------+
                         |
                         v
                 +------------------+
                 | MySQL 8.x        |
                 | advisory lock +  |
                 | tracking table   |
                 +------------------+
```

The initial codebase may keep these components in a small number of packages. Package proliferation is not a goal; behavioral boundaries and testability are.

### 6.1 CLI boundary

The CLI owns:

- command and flag parsing;
- DSN/config resolution;
- signal-aware contexts;
- human and JSON rendering;
- confirmation for highly destructive operations;
- stable process exit codes;
- build version display.

The CLI MUST NOT contain migration-state logic.

### 6.2 Public library boundary

The library owns:

- migration discovery and planning;
- metadata state transitions;
- locking and execution;
- structured results and typed errors.

Library methods MUST NOT write directly to process stdout or stderr.

### 6.3 Migration source boundary

The source component owns:

- filename parsing;
- canonical identity;
- up/down pairing;
- deterministic sorting;
- safe file reads;
- file-size limits;
- exact SHA-256 checksums.

### 6.4 MySQL runtime boundary

The MySQL runtime owns:

- acquisition of a dedicated `*sql.Conn`;
- advisory-lock acquisition and release;
- tracking-table creation/version validation;
- metadata reads and state transitions;
- execution of trusted SQL bytes;
- conversion of MySQL errors into typed lamigrate errors.

## 7. Public Go API

The target API separates database ownership from migration orchestration.

```go
type Options struct {
    Directory    string
    LegacyDir    string
    TableName    string
    LockTimeout  time.Duration
    MaxFileSize  int64
}

// StepLimit has private fields and is created only by these constructors.
// All() means every eligible migration; Steps rejects n <= 0.
type StepLimit struct { /* unexported */ }
func All() StepLimit
func Steps(n int) (StepLimit, error)

type Migrator struct {
    // unexported state
}

// NewMySQL is side-effect free. It clones and validates the driver config.
// The production runtime supports the pinned go-sql-driver/mysql connector.
func NewMySQL(config *mysql.Config, opts Options) (*Migrator, error)

// OpenMySQL is a convenience constructor. It parses the DSN and delegates to
// NewMySQL. Parsing performs no network or database I/O.
func OpenMySQL(dsn string, opts Options) (*Migrator, error)

// Preview methods return opaque informational snapshots. They are not later
// executable; real commands plan and execute under one uninterrupted lock.
func (m *Migrator) PreviewUp(ctx context.Context, limit StepLimit) (PlanView, error)
func (m *Migrator) PreviewDown(ctx context.Context, limit StepLimit) (PlanView, error)
func (m *Migrator) PreviewReset(ctx context.Context) (PlanView, error)
func (m *Migrator) Up(ctx context.Context, limit StepLimit) (Result, error)
func (m *Migrator) Down(ctx context.Context, limit StepLimit) (Result, error)
func (m *Migrator) Reset(ctx context.Context) (Result, error)
func (m *Migrator) Status(ctx context.Context) (StatusReport, error)

type GolangMigrateImportOptions struct {
    SourceTable    string
    SourceQuiesced bool // required for mutation; explicit operator attestation
}
func (m *Migrator) PreviewGolangMigrateImport(ctx context.Context, opts GolangMigrateImportOptions) (ImportPlanView, error)
func (m *Migrator) ImportGolangMigrate(ctx context.Context, opts GolangMigrateImportOptions) (Result, error)

func (m *Migrator) PreviewRepair(ctx context.Context, request RepairRequest) (RepairPlanView, error)
func (m *Migrator) Repair(ctx context.Context, request RepairRequest) (Result, error)
func (m *Migrator) PreviewPrototypeAdoption(ctx context.Context, request AdoptionRequest) (AdoptionPlanView, error)
func (m *Migrator) AdoptPrototype(ctx context.Context, request AdoptionRequest) (Result, error)

// Migration creation is offline and requires no DSN.
func Make(ctx context.Context, directory, name string) (CreatedMigration, error)
```

Exact exported names may change during API review, but these contracts are required:

- `NewMySQL` performs local validation only and does not connect to or mutate MySQL. It defensively clones the supplied `mysql.Config`; later caller changes cannot alter runtime behavior.
- Production support is deliberately restricted to the audited `github.com/go-sql-driver/mysql` connector version pinned in `go.mod`. Arbitrary `driver.Connector` and caller-owned `*sql.DB` values are not accepted by the production API because their physical-session disposal semantics cannot be guaranteed.
- Every connection phase creates a new go-sql-driver/mysql connector from the stored configuration, creates a private one-session `*sql.DB`, and physically closes it. lamigrate never uses or closes the application’s shared pool.
- `OpenMySQL` parses a DSN, enables the required multi-statement and robust time settings, and retains only the cloned configuration.
- Normal planning, capability probes, lock acquisition, metadata reads, and migration execution use the caller-provided context. Filesystem work checks cancellation before discovery, between bounded reads/writes, and before publishing results, but ordinary Go filesystem calls are not claimed to be interruptible while inside the operating system. After execution starts, bounded safety finalization uses a fresh internal context even if the caller context is canceled: rollback of an open transaction, minimum session-state restoration, lock/transaction/database verification, conditional failure-state marking, advisory-lock release, and outcome rereads needed to classify cleanup. The internal context does not continue normal migration work and cannot convert cancellation into success. `database/sql`/driver close calls themselves are not context-aware; uncertain completion is reported rather than represented as successful cancellation.
- Results are structured; rendering belongs to the caller.
- Limits use the validated `StepLimit`; zero never means “all.” Because Go permits `StepLimit{}` and an uninitialized variable even with private fields, every preview/execution method validates the value locally and rejects the zero/unknown representation before creating a connector or touching the filesystem/database. `All()` returns a distinct valid non-zero internal kind.
- Import, adoption, and repair orchestration remain library responsibilities. The CLI only collects input, requests confirmation, calls these APIs, and renders their results.
- `PlanView`, `ImportPlanView`, `RepairPlanView`, and `AdoptionPlanView` expose read-only accessors or defensive copies. Internal executable plans and SQL byte slices are unexported and cannot be mutated by callers.

Before any metadata mutation, the runtime performs side-effect-free capability probes on every private dedicated connection: a two-`SELECT` multi-statement execution with both result sets drained, a `CURRENT_TIMESTAMP(6)` scan into `time.Time`, server-version validation, `SELECT DATABASE()`, connection/result character-set validation, `@@lower_case_table_names`, `@@session.autocommit`, and `@@session.in_transaction`. Failure returns an unsupported-driver/configuration error before metadata or migration SQL.

## 8. Migration file contract

### 8.1 Canonical format

```text
YYYYMMDDHHMMSS_description.up.sql
YYYYMMDDHHMMSS_description.down.sql
```

Rules:

- timestamp is a valid 14-digit UTC timestamp;
- description is lowercase ASCII snake case matching `[a-z][a-z0-9_]*`;
- canonical migration ID is `YYYYMMDDHHMMSS_description`;
- the full ID MUST fit the metadata column and portable filename limits;
- each timestamp is unique within one migration source;
- up and down files form one pair;
- discovery rejects duplicate IDs, duplicate timestamps, down-only files, symlinks, and non-regular files;
- sorting is by timestamp, then canonical ID as a defensive tie-breaker;
- the default maximum file size is bounded and configurable.

### 8.2 Creation

`make` MUST:

1. work without a DSN or database connection;
2. create the directory when missing;
3. use UTC;
4. validate the name before touching the filesystem;
5. use exclusive file creation and never truncate an existing file;
6. handle same-second collisions with a bounded retry or explicit error;
7. clean up an up file if creation of its paired down file fails;
8. report an orphan clearly if cleanup itself fails.

### 8.3 Irreversible migrations

The default policy is that every applied migration has a down file. An intentionally irreversible migration must be explicit, not represented by an accidentally missing file.

Proposed representation:

```sql
-- lamigrate: irreversible
-- reason: data transformation cannot be safely reversed
```

A down/reset plan encountering this marker fails during preflight before executing any rollback SQL. It is never silently skipped. After an operator performs and verifies a manual compensating action, the explicit repair workflow may remove the clean applied record with `mark-rolled-back`; without that evidence, the irreversible migration remains a hard stop.

### 8.4 SQL execution contract

- A migration file is trusted SQL source code.
- Exact file bytes are checksummed before execution.
- Files may contain multiple MySQL statements.
- The supported MySQL connection must enable driver multi-statement execution.
- Client-only directives such as `DELIMITER` are not SQL and are not supported in migration files.
- lamigrate does not split SQL on semicolons.
- MySQL DDL may implicitly commit; a migration file is not assumed atomic.
- A multi-statement error may occur after earlier statements succeeded. The metadata state therefore becomes dirty and requires reconciliation.

## 9. Metadata model

The default table remains `migrations` for the Laravel-style workflow. A custom name is accepted only through validated options before any SQL is generated.

Table-name rules for the first release:

- unqualified identifier in the database captured before lock acquisition;
- lowercase ASCII pattern `[a-z_][a-z0-9_]*`;
- maximum 64 characters;
- consistently quoted after validation;
- no schema-qualified name, expression, whitespace, comment, or punctuation;
- `lamigrate_control` is reserved;
- only `@@lower_case_table_names = 0` is accepted for lock protocol v1;
- the selected database name must match ASCII `[A-Za-z_][A-Za-z0-9_]*` and be at most 64 bytes;
- connection and result character sets must be `utf8mb4`, while the accepted database-name domain remains ASCII.

Lowercase-only tracking names avoid table aliases, while `lower_case_table_names=0` preserves case-sensitive physical database identity. The database name is obtained from `SELECT DATABASE()`, validated as ASCII, and used byte-for-byte for lock derivation and fully qualified metadata SQL. Other case policies and non-ASCII database names are explicitly unsupported by lock protocol v1 rather than deferred to future matrix results.

The runtime uses two metadata tables:

1. the configurable migration-state table, default `migrations`;
2. the fixed internal control table `lamigrate_control`.

The control table stores one row per validated migration-state table. It preserves the metadata schema version and a durable next-batch counter, so batch numbers are never reused after rollback. A custom migration-state table named `lamigrate_control` is therefore reserved and rejected.

Target schemas are:

```sql
CREATE TABLE `lamigrate_control` (
    tracking_table    VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    schema_version    INT UNSIGNED NOT NULL,
    next_batch        BIGINT UNSIGNED NOT NULL,
    updated_at        DATETIME(6) NOT NULL,
    PRIMARY KEY (tracking_table)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

CREATE TABLE `migrations` (
    id                BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    migration         VARCHAR(255) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    source_kind       VARCHAR(24) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    source_version    BIGINT UNSIGNED NULL,
    source_name       VARCHAR(255) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    up_checksum       BINARY(32) NOT NULL,
    down_checksum     BINARY(32) NULL,
    batch             BIGINT UNSIGNED NOT NULL,
    state             VARCHAR(24) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    is_baseline       BOOLEAN NOT NULL DEFAULT FALSE,
    runner_id         CHAR(36) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    started_at        DATETIME(6) NOT NULL,
    applied_at        DATETIME(6) NULL,
    updated_at        DATETIME(6) NOT NULL,
    PRIMARY KEY (id),
    UNIQUE KEY uk_migration (migration),
    KEY idx_batch_state (batch, state),
    CONSTRAINT chk_migration_state CHECK (
        state IN ('applying', 'applied', 'apply_failed',
                  'rolling_back', 'rollback_failed')
    ),
    CONSTRAINT chk_source_kind CHECK (
        source_kind IN ('timestamp', 'golang_migrate')
    ),
    CONSTRAINT chk_source_fields CHECK (
        (source_kind = 'timestamp' AND source_version IS NULL
         AND is_baseline = FALSE AND batch > 0)
        OR
        (source_kind = 'golang_migrate' AND source_version IS NOT NULL
         AND is_baseline = TRUE AND batch = 0 AND state = 'applied')
    ),
    CONSTRAINT chk_state_times CHECK (
        (state IN ('applying', 'apply_failed') AND applied_at IS NULL)
        OR
        (state IN ('applied', 'rolling_back', 'rollback_failed')
         AND applied_at IS NOT NULL)
    )
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;
```

Metadata initialization happens only for write operations, while holding the applicable advisory lock. Before executing any DDL, operation-specific local preflight and the bootstrap phase inventory the control table, requested state table, and known prototype shape. If an existing state object is incompatible, if ordinary work encounters a prototype requiring adoption, or if adoption preflight rejects an empty/exhausted/ambiguous prototype, the command fails without creating another database object. For an empty scope it creates missing known tables in a restartable order, validates their required semantic shape through `information_schema`, and initializes a control row with schema version 1 and `next_batch = 1`. Status and dry-run report uninitialized metadata but do not create it.

Semantic validation compares required column type families, signedness, lengths, nullability, binary collations, keys, enforced check constraints, InnoDB engine, and absence of partitioning. It accepts MySQL-normalized equivalents such as `BOOLEAN` reported as `TINYINT(1)` and permits only additional ordinary non-unique indexes. Extra columns, triggers, foreign keys, generated columns, partitions, extra unique keys, missing/disabled checks, changed required columns, permissive migration/state collations, and unknown control versions are rejected. Every row is also validated against the cross-field source/state/time invariants when read, even when MySQL reports the checks as enforced. Exact rules are fixtures tested on every supported MySQL line rather than raw `SHOW CREATE TABLE` string equality.

Row identity validation is exact:

- a timestamp row's `migration` and `source_name` must both equal the same canonical timestamp migration ID;
- a golang-migrate row's `migration` must equal `golang-migrate:<canonical decimal version>`, `source_version` must equal that version, and the numeric component of `source_name` must canonicalize to it;
- stored checksums must match the applicable exact source bytes, and down checksum/nullability must agree with the reversible/irreversible contract;
- the selected control row must have schema version 1 and `next_batch > max(batch)` for all positive batches, unless an explicitly recognized interrupted adoption is being recovered.

A non-empty up plan allocates its batch by locking the relevant control row in a short metadata transaction, reading `next_batch`, and incrementing it before migration intent is inserted. Gaps after failures are acceptable; reuse is not.

Because every configurable state table shares `lamigrate_control`, first-time control-table bootstrap uses a second database-wide advisory lock derived by the same v1 algorithm with the internal tracking component `!lamigrate-control-bootstrap-v1!`, which cannot be a valid public tracking-table name. A write operation uses two strictly sequential connection phases when bootstrap is needed:

1. complete operation-specific local preflight, then create private pool/session A, repeat all capability and database-identity probes, acquire the bootstrap lock, and read-only inventory both `lamigrate_control` and the requested state/prototype table before any DDL;
2. abort without DDL for an incompatible state object, ordinary operation against a prototype, rejected empty/exhausted adoption, or unexplained partial adoption; otherwise create/validate only `lamigrate_control`, release the lock, and physically terminate pool/session A;
3. abort before any state-table or migration work if bootstrap release or physical termination is uncertain;
4. create fresh private pool/session B, repeat all probes, derive/acquire the normal state-table scope lock, re-inventory the requested state/prototype table to detect change since phase A, create/validate the state table and control row only when still eligible, execute the command, release the scope lock, and physically terminate pool/session B.

No command holds both locks simultaneously and no session is reused across phases. If `lamigrate_control` is already valid, phase A is skipped and only phase B is used. This prevents different custom scopes from racing on shared control-table DDL without serializing ordinary migrations across independent scopes.

Timestamps are supplied as UTC values by the application rather than depending on the MySQL session timezone.

The metadata schema itself is versioned by the control row. Startup must detect unsupported versions or unexpected table layouts and return a migration instruction; it must not destructively rewrite unknown metadata automatically.

### 9.1 State machine

```text
row absent
    |
    | insert intent
    v
applying --------execution error--------> apply_failed
    |                                      ^
    | SQL success                          | state-update failure leaves
    v                                      | applying, also treated dirty
applied -----------------------------------+
    |
    | mark rollback intent
    v
rolling_back ---execution error---------> rollback_failed
    |
    | SQL success
    v
row deleted
```

Dirty states are:

- `applying`;
- `apply_failed`;
- `rolling_back`;
- `rollback_failed`.

Any dirty row blocks up, down, reset, and import. Status remains available and explains the required operator action.

Successful rollback deletes the current-state row. Reapplying later creates a new row and therefore a new monotonic execution order. Long-term audit history may be added separately; it must not complicate the correctness of current state.

### 9.2 Batch semantics

- One successful `up` invocation allocates one batch number.
- Batch allocation happens while holding the advisory lock.
- Batch numbers are monotonic and never reused.
- `down` without a limit rolls back the latest non-baseline batch.
- A positive down limit affects only the latest batch and is strictly validated.
- `reset` rolls back all non-baseline applied migrations in reverse execution order.
- Baseline records use batch 0 and are excluded from normal down/reset.

### 9.3 Prototype metadata adoption

The current prototype table is not silently upgraded. A production binary that finds its exact four-column prototype shape returns `prototype_adoption_required` for every write command. Status may read and report it, but up/down/reset/import remain blocked.

The explicit `adopt-prototype` operation is the only supported conversion path for a non-empty prototype. An exact prototype with zero rows contains no migration history and is rejected by adoption before any DDL; the operator must archive/drop that empty table explicitly, after which normal empty-scope initialization applies. While holding the canonical advisory lock, adoption:

1. requires the exact known prototype columns and unique migration key, at least one row, and `MAX(id) < 18446744073709551615`; it rejects any unknown variation or exhausted ID domain before creating a temporary table;
2. requires an operator-selected backup table name that follows the lowercase identifier policy and does not exist;
3. maps positive-batch timestamp records to exact timestamp source pairs in `Directory`;
4. maps batch-0 records to exact numeric source pairs in `LegacyDir`;
5. rejects every missing, ambiguous, malformed, duplicated, or unclassifiable source;
6. computes exact up/down checksums and displays the complete conversion preview;
7. creates and validates a temporary v1 state table with a generated collision-resistant lowercase name;
8. copies records in strictly ascending prototype `id` order, preserving each original `id`, `batch`, and `applied_at`, and using `source_kind`/`source_name` appropriate to each source plus a conversion runner ID;
9. sets the temporary table’s next auto-increment value above the preserved maximum ID, verifies counts, exact IDs, relative execution order, identities, and checksums, and computes expected `next_batch = max(1, max(positive batch)+1)`;
10. atomically swaps tables using one `RENAME TABLE prototype TO backup, temporary TO requested` statement;
11. creates or verifies the control row with schema version 1 and the computed next batch, then durably re-reads and verifies both values;
12. retains the backup table until the operator verifies status and removes it manually.

Preview performs steps 1–6 only and creates nothing. A failure before the atomic rename leaves the prototype authoritative and any known temporary table safe to remove/retry. A crash after rename but before control-row creation leaves a recognizable v1 state table plus backup. Ordinary commands classify a non-empty v1 table without its control row as `interrupted_prototype_adoption`; they never synthesize control state. Recovery requires retrying `adopt-prototype` with the same requested backup table, migration directories, and source evidence. Under the lock, that retry verifies the complete v1 table against the retained prototype backup and exact source checksums, then reconstructs only the missing control row. It never repeats the rename. A different backup request, missing backup, unexplained temporary table, changed source, or any other partial shape fails closed for manual inspection.

This adoption policy preserves existing prototype users without weakening v1 invariants. It is part of Phase 3 and must be verified before a production-capable release.

## 10. Locking and connection lifecycle

Every database-dependent connection phase creates a fresh go-sql-driver/mysql connector from the stored cloned configuration, creates a private `*sql.DB`, sets its maximum open and idle connections to one, obtains one `*sql.Conn`, and closes the connection and private pool at phase completion. The supported connector opens a direct MySQL network session and its `driver.Conn.Close` terminates that session; this behavior is pinned, audited, and integration-tested. The physical session is never returned to an application pool and is never reused by a later phase or command. This also prevents trusted migration SQL from leaking `USE`, SQL mode, timezone, `FOREIGN_KEY_CHECKS`, temporary tables, autocommit, or other session state into application work.

### 10.1 Canonical lock key

After capability checks, the runtime captures the non-empty database name from `SELECT DATABASE()` and the validated lowercase tracking table. It computes:

```text
scope bytes = UTF-8("lamigrate-lock-v1") || 0x00 ||
              UTF-8(database-name)      || 0x00 ||
              UTF-8(tracking-table)
digest      = SHA-256(scope bytes)
lock key    = "lamigrate:v1:" + lowercase_hex(digest[0:24])
```

The final key is 61 ASCII characters and provides a 192-bit digest within MySQL’s 64-character lock-name limit. This algorithm and byte framing are permanent for lock protocol v1. Tests publish fixed input/output vectors. A future algorithm cannot silently change this key; a protocol migration must coordinate old and new locks explicitly.

Tracking-table names are lowercase-only, `@@lower_case_table_names` must equal `0`, and the database name must satisfy the fixed ASCII domain before lock acquisition. The captured database name, table name, lock-protocol version, and final key may appear in debug diagnostics; credentials may not.

### 10.2 Protocol

For every database-dependent command, including status and preview:

1. validate local options and migration arguments;
2. create the fresh audited one-session private pool and obtain its dedicated connection;
3. run capability probes and capture `CONNECTION_ID()`, database, case policy, and clean session state;
4. derive the canonical lock key;
5. call `GET_LOCK(lockKey, timeoutSeconds)` using the command context;
6. treat `1` as acquired, `0` as timeout, and `NULL`, query error, cancellation, or an unreceived result as uncertain;
7. after a reported acquisition, verify `IS_USED_LOCK(lockKey) = CONNECTION_ID()`;
8. keep all metadata and migration operations on that connection;
9. verify lock ownership before and after each migration-file execution;
10. release using a fresh bounded cleanup context independent of the possibly canceled command context;
11. consider release successful only when `RELEASE_LOCK(lockKey)` returns exactly `1`;
12. close the `*sql.Conn` and private `*sql.DB` in all cases, forcing physical session termination rather than returning it to a shared pool.

Closing the private pool is mandatory even after successful release. It is the final safeguard for uncertain acquisition/release, additional named locks, open transactions, and modified session state. If physical-session termination cannot be established, the command returns a cleanup-uncertain error and the connector implementation is not production-supported.

`LockTimeout` must be non-negative and no greater than 24 hours. It is rounded up to whole seconds for `GET_LOCK`; zero means do not wait. The effective wait is bounded by the earlier of this timeout and the caller context deadline.

Migration files are trusted but MUST NOT call `GET_LOCK`, `RELEASE_LOCK`, or related advisory-lock functions; change the selected database; modify lamigrate metadata tables; or intentionally leave an open transaction. lamigrate verifies ownership and session state at the boundaries, but no migration runner can prevent trusted SQL from releasing its own session lock in the middle of one multi-statement request. Violating this contract is unsupported and produces a dirty/cleanup-uncertain result when detectable.

The advisory lock coordinates only lamigrate clients using lock protocol v1. Running another migration tool against the database at the same time remains unsupported.

## 11. Planning and execution

Every command uses a shared planner. All metadata statements are fully qualified with the captured and quoted database plus validated table name, so migration SQL cannot redirect metadata by issuing `USE`.

Metadata DML never relies on ambient autocommit. Each intent, state transition, batch allocation, failure mark, or deletion is performed in its own explicit short transaction:

1. assert the session has `@@session.autocommit = 1`, `@@session.in_transaction = 0`, the captured database, and the expected lock owner;
2. `START TRANSACTION`;
3. execute one conditional metadata mutation;
4. require exactly one affected row where a row should already exist;
5. `COMMIT` and require acknowledgement;
6. re-read the row/control value to verify the committed state before migration SQL proceeds.

A commit error or lost acknowledgement is uncertain and blocks further execution. The command MUST NOT assert which side of the transition is durable and MUST NOT issue a compensating metadata mutation. For an intent insert, the row may be absent or `applying`; SQL is never executed after an ambiguous intent commit. For later transitions, either the old or new state may be durable, including row absence after an ambiguous rollback deletion. The physical session is terminated. A subsequent separately locked status/repair read determines the actual durable state before any operator action. Failure-marking uses a fresh cleanup context but the same still-owned session only when session and lock ownership can be proven. It never changes an uncertain state optimistically.

### 11.1 Preflight sequence

While holding the lock, the planner:

1. validates options and tracking-table identity;
2. scans and validates the complete migration directory;
3. reads metadata without mutating it for status/preview;
4. reads and checksums every source pair corresponding to every applied or baseline metadata row, even when it is not selected, to enforce global drift blocking;
5. reads selected pending up files or selected rollback down files into bounded internal immutable storage;
6. computes SHA-256 checksums over exact bytes;
7. rejects unsupported metadata schema, dirty rows, missing sources, orphaned records, checksum drift, and duplicate order;
8. validates the complete selected action set before any SQL runs;
9. produces an immutable ordered internal execution plan plus a defensive informational view.

The global-integrity read set and selected execution read set are distinct. All applied records are validated; only selected SQL bytes are retained for execution. Execution consumes the internal plan directly without rescanning.

### 11.2 Up protocol

For each planned migration:

1. commit an `applying` row with `source_kind=timestamp`, the canonical `source_name`, null `source_version`, checksums, batch, runner ID, and start time using the explicit metadata transaction protocol;
2. re-verify lock ownership and execute the exact up SQL bytes;
3. inspect `@@session.in_transaction`, `@@session.autocommit`, `DATABASE()`, and lock ownership immediately afterward;
4. if migration execution failed or session state changed, issue and require acknowledgement of `ROLLBACK` when a transaction is open, require acknowledged restoration of `autocommit=1` and the captured database, then freshly verify `in_transaction=0`, database, and lock ownership before conditionally committing `applying -> apply_failed`;
5. if execution succeeded with clean expected session state, conditionally commit `applying -> applied` and set `applied_at`;
6. if the final transition is unacknowledged, the durable row may be `applying` or `applied`; make no further mutation, terminate the session, and return outcome-unknown/recovery-required. If it is acknowledged as failed before commit, `applying` remains dirty.

If rollback, state restoration, or any post-restoration check fails or is unacknowledged, lamigrate does not attempt a failure-state transition: it leaves the original durable `applying` row untouched, physically terminates the session, and reports recovery-required. Migration SQL that leaves an open transaction is treated as failure; lamigrate rolls it back rather than silently committing it. Previously implicit-committed DDL may still have taken effect, so the row remains dirty and requires inspection. No later migration executes after one migration fails.

### 11.3 Down/reset protocol

Before the first rollback SQL, the entire selected rollback set and all globally applied source checksums are preflighted.

For each migration in reverse execution order:

1. verify current up/down checksums against stored values;
2. conditionally commit `applied -> rolling_back` using the explicit metadata transaction protocol;
3. re-verify lock ownership and execute exact down SQL bytes;
4. inspect transaction, autocommit, selected database, and lock state;
5. on SQL failure or changed session state, require acknowledged rollback of any open transaction, acknowledged restoration of `autocommit=1` and the captured database, and fresh verification of `in_transaction=0`, database, and lock ownership before conditionally committing `rolling_back -> rollback_failed`;
6. on clean SQL success, delete the `rolling_back` row in an explicit metadata transaction;
7. if rollback/restoration verification fails, leave `rolling_back` unchanged, terminate the session, and stop; if deletion commit acknowledgement is lost, the durable state may be `rolling_back` or row-absent, so make no further mutation, terminate the session, and return outcome-unknown.

### 11.4 Status

Status is side-effect free. It does not create or alter metadata tables.

It reports at least:

- pending;
- applied with batch and time;
- baseline;
- applying or rolling back;
- failed/dirty;
- checksum drift;
- applied record with missing source file;
- missing or irreversible down file;
- malformed, duplicate, or unpaired source files;
- unsupported metadata layout.

### 11.5 Dry run

Dry-run acquires the same lock and invokes the same planner as execution, but performs no metadata DDL/DML and no migration SQL.

- up dry-run shows every selected forward action;
- down dry-run shows every selected rollback action;
- reset dry-run shows the complete reset set, not only the latest batch;
- an uninitialized target explicitly reports that real execution would initialize metadata, without creating tables or allocating a batch;
- JSON output excludes raw SQL unless explicitly requested;
- human SQL output warns that migration content may be sensitive.

## 12. Recovery and repair

MySQL cannot prove that an interrupted DDL operation either fully happened or did not happen. Repair is therefore explicit and conservative.

A target repair command supports operations such as:

```text
lamigrate repair show
lamigrate repair mark-applied <migration> --yes
lamigrate repair mark-rolled-back <migration> --yes
lamigrate repair remove-failed <migration> --yes
```

Repair requirements:

- requires the advisory lock;
- operates on one named dirty migration, except `mark-rolled-back`, which may also target one clean applied irreversible migration after manual compensation;
- prints current metadata and expected checksums;
- requires explicit confirmation in non-interactive use;
- never executes migration SQL automatically;
- implements only legal conditional transitions: `applying|apply_failed -> applied`, `rolling_back|rollback_failed -> applied`, dirty state -> row absent after verified rollback, or clean irreversible `applied -> row absent` after verified manual compensation;
- requires a free-text operator reason for every mutation and includes it in the structured result;
- records a structured result suitable for an operator audit log;
- documents the database inspection the operator must perform first.

Checksum drift is not repairable by acknowledgment in metadata schema v1. The operator must restore the exact applied source bytes. This prevents a repair command from normalizing unauthorized migration edits.

A future append-only audit table may persist repair history. For schema v1, durable external audit retention is the operator's responsibility; the JSON result contains the evidence fields needed for it.

## 13. golang-migrate import

Import is a reconciliation operation, not “mark every file applied.”

Import requires an explicit operator attestation (`--source-quiesced`) that no golang-migrate process or manual writer can change the source metadata or schema during reconciliation. The lamigrate lock cannot coordinate the old tool. Immediately before target mutation, the importer re-reads the source `version, dirty` tuple and aborts if it differs from the planned snapshot. A clean version proves only the tool's current version, not historical execution after manual force; the preview warns about this limitation and requires operator verification.

`ImportGolangMigrate` rejects `SourceQuiesced=false` before creating a connector or reading files. `PreviewGolangMigrateImport` may run with false and reports that mutation remains blocked until the explicit attestation is supplied. The CLI flag sets this request field; direct library callers receive the same enforcement.

Import uses the separate legacy source directory configured by `Options.LegacyDir` and the source metadata table supplied by `GolangMigrateImportOptions`/CLI flag. The source table is validated using the same lowercase identifier policy and MUST differ from the destination state table and `lamigrate_control`. Legacy files remain in that configured directory after import because their exact bytes are required for baseline status and drift checks. They are never mixed into normal timestamp discovery. Changing `LegacyDir` later is a deployment configuration change and must still resolve every stored `source_name` to identical bytes.

Legacy filenames use `<numeric-version>_<description>.up.sql` and the paired down name, where version is any positive unsigned 64-bit decimal and description follows the canonical lowercase snake-case rule. Leading zeroes are accepted in filenames but numeric identity is canonicalized. Sparse version sequences are valid and are reported, not rejected. A baseline metadata row uses:

- `migration = "golang-migrate:" + canonical decimal version`;
- `source_kind = golang_migrate`;
- `source_version = version`;
- `source_name = exact validated filename base`;
- exact up/down checksums from the legacy directory;
- `batch = 0`, `state = applied`, and `is_baseline = true`;
- a fresh import runner ID and the import time for required timestamps.

The import planner MUST:

1. read golang-migrate’s explicitly configured source metadata table and require its recognized one-row `version, dirty` shape;
2. read and validate its current `version` and `dirty` value;
3. refuse import when golang-migrate is dirty;
4. discover legacy files using numeric versions rather than a fixed six-digit assumption;
5. classify versions at or below the recorded database version as baseline candidates;
6. require the recorded current version and every baseline candidate to have a unique valid up/down pair;
7. identify files above the current database version as unresolved and block mutation until the operator moves or converts them into timestamp migrations;
8. reject duplicate numeric versions, source/destination collisions, malformed files, and mixed incompatible layouts while merely reporting sparse-version gaps;
9. display exact baseline IDs, names, and checksums before mutation;
10. support dry-run with no metadata initialization;
11. require either an empty destination state table or an exact previously imported baseline set with no conflicting rows; extension of a previous baseline is not supported in import v1;
12. for an empty destination, initialize target metadata if needed, re-read the unchanged source tuple, then insert all baseline rows in one explicit metadata transaction while holding the advisory lock;
13. for an exact existing baseline set with identical identities/checksums and the same source tuple, return an idempotent no-op; any partial or conflicting set fails closed.

If metadata initialization succeeds and the baseline transaction is definitively rolled back or fails before commit, the target remains empty and an identical retry is valid. If commit acknowledgement is lost, the durable target may be empty or contain the complete baseline set; return outcome-unknown, perform no compensating mutation, terminate the session, and require a newly planned, separately locked import using the same source table, legacy directory, source-quiescence attestation, and current source snapshot. That import replans from source and target: it classifies an exact complete baseline set as an idempotent completed import, an empty set as safe to retry, and any partial/conflicting set as recovery-required. Generic `status` may report the observed target rows and outcome-unknown condition but cannot prove import completeness because schema v1 does not persist the old source tuple. Normal status resolves `golang_migrate` rows only through the configured legacy directory. Normal up discovers only timestamp migrations. Normal down/reset never rolls back baseline rows. Moving or changing legacy source after import is drift; converting or rolling back baseline history requires a separate future architecture and is not supported by repair v1.

## 14. CLI contract

Target commands:

```text
lamigrate up [--step N] [--pretend] [--json]
lamigrate down [--step N] [--pretend] [--json]
lamigrate reset [--pretend] [--yes] [--json]
lamigrate status [--json]
lamigrate migration create <name>
lamigrate make <name>               # compatibility alias
lamigrate make:migration <name>     # Laravel-style alias
lamigrate import golang-migrate --source-table <name> --legacy-dir <dir> --source-quiesced [--pretend] [--yes] [--json]
lamigrate adopt-prototype --backup-table <name> [--pretend] [--yes] [--json]
lamigrate repair ...
lamigrate version
```

Requirements:

- `help`, `version`, and `make` require no DSN;
- unknown flags and extra arguments fail with usage output;
- `N` must be a strictly positive integer;
- malformed, zero, and negative limits fail before database access;
- flags have one documented location/syntax and are tested;
- `reset`, import mutation, prototype adoption, and repair require explicit confirmation or `--yes`;
- stdout contains requested results; stderr contains diagnostics;
- `--json` has a versioned, documented schema;
- SIGINT/SIGTERM cancel through `signal.NotifyContext`;
- no error or debug output includes the DSN.

Stable exit-code categories:

| Code | Meaning |
|---:|---|
| 0 | Success, including a valid no-op |
| 1 | Execution or unexpected internal failure |
| 2 | Usage or configuration error |
| 3 | Lock unavailable or timed out |
| 4 | Dirty state, drift, or preflight safety failure |

## 15. Configuration and credentials

Configuration precedence must be documented and deterministic. Recommended order:

1. explicit command option;
2. `LAMIGRATE_DSN`;
3. protected DSN file when `--dsn-file` is used.

The CLI should discourage inline DSNs because command-line values can appear in shell history and process listings. A DSN file must be a regular file and should be restricted to owner access on platforms that expose POSIX permissions.

The supported MySQL convenience constructor parses the DSN through go-sql-driver/mysql configuration rather than concatenating strings. It enables the required multi-statement contract and robust time parsing. TLS behavior is documented explicitly; production examples use authenticated TLS rather than silently implying plaintext remote connections are safe.

## 16. Errors and observability

The library exposes typed/sentinel categories suitable for `errors.Is`/`errors.As`, including:

- invalid configuration or migration;
- lock timeout;
- dirty state;
- checksum drift;
- unsupported metadata schema;
- SQL execution failure;
- recovery required;
- outcome unknown after an unacknowledged metadata commit;
- cleanup or physical-session termination uncertain;
- unsupported driver/configuration or lock-protocol domain.

Structured results include:

- command;
- runner ID;
- migration ID;
- direction;
- batch;
- outcome;
- duration;
- error category.

Raw DSNs are never retained in result objects. SQL text is absent unless explicitly requested by dry-run. The CLI may support quiet, human, and JSON renderers without changing library behavior.

## 17. MySQL support policy

Initial production support targets:

- the latest supported MySQL 8.0 patch line; and
- MySQL 8.4 LTS.

Each supported line must be present in integration CI. MariaDB is unsupported until its advisory-lock, DDL, collation, metadata, and driver behavior is covered by a separate compatibility matrix.

Production documentation must state:

- required MySQL privileges;
- advisory-lock assumptions;
- DDL implicit-commit limitations;
- backup requirements before destructive migrations;
- supported character sets and SQL modes;
- TLS configuration examples;
- behavior under cancellation and connection loss.

## 18. Verification architecture

Production readiness requires executable evidence, not only source review.

### 18.1 Unit tests

Unit tests cover:

- filename validation and portable limits;
- UTC timestamp validation;
- deterministic ordering and duplicate timestamps;
- up/down pairing and irreversible markers;
- symlink/non-regular-file rejection;
- exclusive file creation and collision cleanup;
- checksum calculation;
- CLI parsing and fail-closed limits;
- zero/uninitialized `StepLimit`, integer overflow, repeated limit arguments, and proof of rejection before connector creation;
- direct library import mutation with missing `SourceQuiesced`, preview without attestation, and CLI-to-request attestation propagation;
- cancellation checks around bounded filesystem work without claiming in-kernel interruption;
- plan rendering and JSON schema;
- drift and status classification;
- lock-v1 fixed vectors for every accepted ASCII database/table boundary and rejection of non-ASCII names and case-policy values other than `0`;
- metadata semantic-shape and cross-field row fixtures, including every allowed normalization and rejected extra object/constraint;
- typed error behavior.

### 18.2 MySQL integration tests

Integration tests run against real MySQL 8.0 and 8.4 and cover:

- metadata initialization and custom table names;
- semantic metadata validation on both supported MySQL lines;
- a two-scope first-write race proving bootstrap serialization, two sequential disposable sessions, abort after uncertain phase-A cleanup, and no bootstrap/public-scope key collision;
- multi-statement migrations;
- up/down/reset batches;
- monotonic non-reused batches after rollback, reset, failures, repair, and crashes;
- baseline exclusion;
- advisory-lock contention between two processes;
- fixed lock-key vectors, connection-ID continuity, ASCII/case/character-set policy, lock survival across implicit DDL commits, and physical session disposal after every outcome;
- simultaneous up attempts;
- cancellation, signal termination, ambiguous lock acquisition/release, and connection loss;
- partial multi-statement failure;
- crash/failpoint before/after every intent/final-state commit boundary, including failed or unacknowledged rollback, autocommit/database restoration, post-restoration ownership checks, `autocommit=0`, and changed session state;
- lost commit acknowledgements proving all allowed durable outcomes (`absent|applying`, `applying|applied`, `rolling_back|row absent`) and proving no compensating mutation occurs before a separate locked reread;
- dirty-state blocking and explicit repair;
- global checksum drift in selected and unselected applied migrations, missing files, and immutable execution bytes;
- legacy import with empty/forced/changed source state, arbitrary-width/sparse/maximum versions, leading-zero identity, dirty/malformed source metadata, unresolved future versions, exact/repeated/conflicting baseline sets, changed `LegacyDir`, atomic retry, and source/destination collisions;
- preview/execution parity for up, limited down, and complete reset, plus proof that previews create no metadata or batch allocation;
- side-effect-free construction, offline commands, no library stdout/stderr, cloned MySQL config, capability-probe failure before mutation, result-set draining, and per-phase physical-session disposal;
- exact prototype adoption, old-ID execution-order preservation, limited-down parity after adoption, atomic swap, same/different-request interrupted recovery, orphan temporary tables, custom prototype tables, batch-0 records, and `next_batch` reconstruction;
- prototype adoption rejection for an empty table and for `MAX(id)` exhaustion before temporary-table DDL;
- time handling with common DSN options;
- metadata upgrade detection.

### 18.3 Target CI gates

These are target gates to be created; they are not currently available as a complete passing pipeline:

```bash
test -z "$(gofmt -l .)"
go mod tidy -diff
go vet ./...
go test -count=1 ./...
go test -race -count=1 ./...
staticcheck ./...
govulncheck ./...
go test -tags=integration -count=1 ./...
```

The integration command must create isolated databases and must never target developer or production data.

CI also verifies cross-builds for supported Linux, macOS, and Windows architectures. Database behavior is verified on Linux runners using pinned MySQL 8.0 and 8.4 patch images. The matrix records SQL modes, UTC and non-UTC sessions, TLS, least-privilege failures, character sets, and the accepted `lower_case_table_names` policy. Unpinned `latest` images are not release evidence.

## 19. Release and supply-chain architecture

Before the first production-capable release:

1. use the approved permanent module path `github.com/rajifafif/lamigrate`;
2. make all source imports, README commands, package docs, and release metadata agree with it;
3. use a patched, pinned Go toolchain;
4. require CI and independent review on the protected default branch;
5. tag releases using semantic versioning;
6. build from a clean tag with GoReleaser or an equivalent reproducible workflow;
7. publish platform archives, SHA-256 checksums, and an SBOM;
8. generate GitHub artifact attestations/provenance;
9. run `govulncheck` before release;
10. inspect each binary with `go version -m` to verify module and toolchain provenance;
11. test the documented `go install ...@<version>` path from a clean environment;
12. maintain a changelog, security policy, contribution guide, and release support policy.

A stable `v1.0.0` is not appropriate until the public API and metadata schema have demonstrated compatibility through at least one real pre-1.0 release cycle.

## 20. Incremental implementation sequence

### Phase 0 — Approve contracts

Decide and record:

- permanent repository/module path `github.com/rajifafif/lamigrate` (decided);
- exact CLI compatibility policy;
- irreversible-migration marker;
- imported baseline rollback policy;
- supported MySQL versions;
- exact prototype adoption and interrupted-adoption recovery policy described in §9.3;
- production support for exactly `lower_case_table_names=0` and the ASCII database-name domain defined by lock protocol v1.

Exit criterion: this architecture is reconciled with those decisions and explicitly approved.

### Phase 1 — Characterize the prototype

- Add unit tests for current discovery, ordering, CLI parsing, make, status, up/down/reset selection, and import behavior.
- Add regression tests that demonstrate each known unsafe behavior before changing it.
- Add an isolated MySQL test harness.

Exit criterion: known behavior and known failures are reproducible without production data.

### Phase 2 — Establish pure boundaries

- Introduce validated options and side-effect-free construction.
- Separate structured library results from CLI output.
- Introduce source validation, deterministic plans, checksums, and safe file creation.
- Make `migration create` (with `make`/`make:migration` compatibility aliases), help, and version fully offline.

Exit criterion: source/planner/CLI unit tests pass and public API direction is independently reviewed.

### Phase 3 — Establish database safety

- Implement the dedicated-connection advisory lock.
- Introduce versioned metadata and the dirty-state machine.
- Implement and verify the explicit prototype adoption path; never auto-upgrade it during ordinary commands.
- Execute immutable preflight plans.
- Add drift checks, full rollback preflight, and baseline semantics.
- Implement failure-injection and concurrent-runner integration tests.

Exit criterion: all state transitions and crash windows have executable MySQL evidence.

### Phase 4 — Safe operations

- Replace permissive argument handling with strict command parsing.
- Implement correct dry-run parity and structured JSON output.
- Implement reconciled golang-migrate import.
- Implement explicit repair workflows and operator documentation.

Exit criterion: destructive operations fail closed and recovery procedures pass integration tests.

### Phase 5 — Open-source release readiness

- Add CI, security scanning, release automation, checksums, SBOM, and provenance.
- Add README limitations/support matrix, `CONTRIBUTING.md`, `SECURITY.md`, code of conduct, templates, and changelog.
- Run an independent architecture, security, and release review.
- Publish an explicitly experimental pre-1.0 release first.

Exit criterion: clean-environment installation and the complete supported matrix pass from a release tag.

Each phase should be delivered as bounded, reviewable changes. Behavior changes, refactoring, metadata migration, and documentation updates should be separated where practical.

## 21. Open decisions requiring maintainer approval

1. Final irreversible marker syntax; the architecture already requires explicit manual compensation plus repair for bypass.
2. Whether inline `--dsn` remains supported with a warning or is removed in favor of environment/file input.
3. Exact JSON output schema and compatibility promise.
4. Whether MariaDB remains explicitly unsupported or is scheduled for a later compatibility milestone.

Until these decisions are approved, dependent implementation tasks remain design candidates rather than settled contracts.

## 22. Definition of production ready

lamigrate is production ready only when all of the following are true:

- no unresolved critical/high independent-review findings;
- canonical module installation works from a clean environment;
- constructors and offline commands have no unexpected database side effects;
- invalid input cannot broaden an operation;
- custom metadata identifiers are validated and initialized correctly;
- advisory-lock contention and concurrent runners are tested;
- every execution crash window leaves an observable, blocking dirty state;
- every unacknowledged metadata commit is reported as outcome-unknown and reconciled only by a later separately locked read;
- up/down/reset/import/repair have real MySQL integration coverage;
- status detects dirty state, missing files, and checksum drift;
- dry-run and execution share one plan;
- release binaries use a patched Go toolchain and pass vulnerability scanning;
- unit, race, static-analysis, integration, and cross-build gates pass in CI;
- backup, failure, recovery, compatibility, and security limitations are documented;
- release artifacts include checksums, SBOM, and provenance;
- an independent reviewer verifies the release candidate against this architecture.

Until then, releases must be labeled experimental and must not claim production safety.
