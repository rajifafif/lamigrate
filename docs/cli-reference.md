# CLI Command Reference

Complete reference for the `lamigrate` command-line interface.

> **Status: experimental, pre-1.0.** Exit codes, JSON schema, and signal
> behavior are stable categories but the exact format may change before v1.0.
> See [architecture.md](../architecture.md) section 14 for the target CLI contract.

## Usage

```text
lamigrate [global-flags] <command> [command-args]
```

Global flags must appear **before** the command name. Command arguments appear after the command name.

## Commands

### up

Apply pending migrations.

```text
lamigrate up [--step N]
```

| Argument | Description |
|----------|-------------|
| `--step N` | Apply at most N migrations. Must be a positive integer. |
| `N` (positional) | Backward-compatible alternative to `--step N`. |

With `--pretend`, shows the plan without executing. Without a limit, applies all pending migrations.

**Examples:**

```bash
lamigrate -config config.yaml up              # Apply all pending
lamigrate -config config.yaml --step 3 up     # Apply next 3
lamigrate -config config.yaml --pretend up    # Dry run
```

### down

Rollback applied migrations from the last batch.

```text
lamigrate down [--step N]
```

| Argument | Description |
|----------|-------------|
| `--step N` | Rollback at most N migrations from the latest batch. Must be a positive integer. |
| `N` (positional) | Backward-compatible alternative to `--step N`. |

Without a limit, rolls back all migrations in the latest batch. With `--pretend`, shows the plan without executing.

**Examples:**

```bash
lamigrate -config config.yaml down              # Rollback last batch
lamigrate -config config.yaml --step 1 down     # Rollback 1 from last batch
lamigrate -config config.yaml --pretend down    # Dry run
```

### reset

Rollback ALL applied migrations (all batches, in reverse order).

```text
lamigrate reset
```

**Requires confirmation.** The CLI prompts for `[y/N]` unless `-y`/`--yes` is provided. In non-interactive mode (piped stdin), `--yes` is mandatory.

With `--pretend`, shows the complete reset plan without executing.

**Examples:**

```bash
lamigrate -config config.yaml -y reset       # Skip confirmation
lamigrate -config config.yaml --pretend reset # Dry run
```

### status

Show migration status. This is a side-effect-free read operation; it does not create or alter metadata tables.

```text
lamigrate status
```

**Output columns:**

| Column | Description |
|--------|-------------|
| Migration | Migration name (e.g., `20260730094235_create_users`) |
| Status | `APPLIED`, `PENDING`, `BASELINE`, `DIRTY`, `DRIFT`, `MISSING_SOURCE` |
| Batch | Batch number (empty for pending migrations) |
| Applied At | Timestamp when the migration was applied |

**Examples:**

```bash
lamigrate -config config.yaml status
```

### migration create

Create a new migration file pair. This is an **offline** command -- no database connection required.

```text
lamigrate migration create <name>
```

| Argument | Description |
|----------|-------------|
| `<name>` | Migration name in snake_case (e.g., `create_users_table`) |

**Templates:** The generated SQL depends on the name pattern:

| Pattern | Template | Generated SQL |
|---------|----------|---------------|
| `create_<table>_table` | `create_table` | `CREATE TABLE` skeleton with `id`, `created_at`, `updated_at` |
| `add_<column>_to_<table>_table` | `add_column` | Guarded `ALTER TABLE ADD COLUMN` suggestion |
| `drop_<column>_from_<table>_table` | `drop_column` | Guarded `ALTER TABLE DROP COLUMN` suggestion |
| *(other names)* | `generic` | Guarded TODO template |

Guarded templates include a `SIGNAL SQLSTATE '45000'` statement that will cause the migration to fail if executed before the guard is removed. This prevents unfinished migrations from being silently recorded as applied.

**Examples:**

```bash
lamigrate -dir sql/migrations migration create create_users_table
lamigrate migration create add_email_to_users_table
```

### make

Compatibility alias for `migration create`.

```text
lamigrate make <name>
```

```bash
lamigrate make create_users_table
```

### make:migration

Laravel-style alias for `migration create`.

```text
lamigrate make:migration <name>
```

```bash
lamigrate make:migration create_users_table
```

### import

Import legacy numbered migration files (golang-migrate format) as already applied. Uses the legacy API.

```text
lamigrate import
```

**Requires confirmation.** The CLI prompts for `[y/N]` unless `-y`/`--yes` is provided. In non-interactive mode (piped stdin), `--yes` is mandatory.

This command reads numbered files (`000001_name.up.sql` / `000001_name.down.sql`) from the migrations directory and records them as already applied in batch 0. It does not execute any SQL.

> **Note:** This is the legacy import. The production reconciled import
> (`import golang-migrate`) with source/dirty validation is planned for
> LM-025.

**Examples:**

```bash
lamigrate -config config.yaml -y import
```

### version

Print the version string and exit. This is an **offline** command.

```text
lamigrate version
```

With `--json`, outputs structured JSON:

```json
{
  "version": 1,
  "command": "version",
  "data": {
    "version": "0.1.0-experimental"
  }
}
```

## Global Flags Reference

| Flag | Alias | Type | Default | Description |
|------|-------|------|---------|-------------|
| `-dir` | -- | string | `sql/migrations` | Path to migrations directory |
| `-dsn` | -- | string | (none) | MySQL DSN string. Overrides config. Warns about shell history. |
| `-config` | `--config` | string | (none) | Explicit path to configuration file |
| `-table` | -- | string | `migrations` | Tracking table name |
| `-pretend` | `--pretend` | bool | `false` | Show plan without executing |
| `-y` | `--yes` | bool | `false` | Skip confirmation prompts |
| `--json` | -- | bool | `false` | Output structured JSON (experimental schema v1) |
| `-h` | `--help` | bool | `false` | Show help text |

**Flag syntax:**

```bash
# Separate form: -flag value
lamigrate -dsn "user:pass@tcp(host:3306)/db" up

# Equals form: -flag=value
lamigrate -dsn="user:pass@tcp(host:3306)/db" up

# Boolean flags (no value)
lamigrate -pretend up
lamigrate --pretend up
```

**Important:** Global flags must appear before the command name. Flags placed after the command name are treated as command arguments and may cause errors.

```bash
# Correct
lamigrate -dsn "..." --pretend up

# Incorrect (flags after command are treated as args)
lamigrate up -dsn "..." --pretend
```

## Exit Codes

| Code | Name | Meaning |
|------|------|---------|
| `0` | `ExitSuccess` | Command completed successfully, including a valid no-op |
| `1` | `ExitExecution` | General runtime or execution error |
| `2` | `ExitUsage` | Bad arguments, unknown command, or flag misuse |
| `3` | `ExitLockTimeout` | Advisory lock could not be acquired within the timeout |
| `4` | `ExitDirtyState` | Migration state is inconsistent (dirty state, checksum drift, preflight safety failure) |

### Exit Code Mapping by Error Type

| Error Sentinel | Exit Code |
|----------------|-----------|
| `ErrLockTimeout` | 3 |
| `ErrDirtyState` | 4 |
| `ErrChecksumDrift` | 4 |
| All other errors | 1 |

Usage errors (bad flags, missing arguments, unknown commands) always exit with code 2, even before a database connection is attempted.

Confirmation aborts (user declines `reset`/`import` prompt) exit with code 2.

## JSON Output Schema

JSON output is enabled with `--json` and is experimental (schema version 1). The schema may change before v1.0.

### Wrapper Object

Every JSON response wraps output in this structure:

```json
{
  "version": 1,
  "command": "up",
  "data": { },
  "error": null
}
```

| Field | Type | Description |
|-------|------|-------------|
| `version` | int | Schema version (currently `1`) |
| `command` | string | Command name (`up`, `down`, `reset`, `status`, `make`, `import`, `version`) |
| `data` | object | Command-specific output (see below) |
| `error` | object or null | Error details (see below) |

### Error Object

```json
{
  "category": "lock_timeout",
  "message": "lamigrate: lock timeout: could not acquire lock within 30s"
}
```

| Field | Type | Description |
|-------|------|-------------|
| `category` | string | Error category (see table below) |
| `message` | string | Human-readable error message |

### Error Categories

| Category | Meaning | Exit Code |
|----------|---------|-----------|
| `lock_timeout` | Advisory lock could not be acquired | 3 |
| `dirty_state` | Migration state is inconsistent | 4 |
| `checksum_drift` | Applied migration file was modified | 4 |
| `sql_execution` | Migration SQL statement failed | 1 |
| `unsupported_metadata` | Tracking table has unrecognized schema | 1 |
| `recovery_required` | Operator intervention needed | 1 |
| `outcome_unknown` | Metadata commit outcome ambiguous | 1 |
| `confirmation_required` | `--yes` required for destructive operation | 2 |
| `invalid_config` | Configuration validation failed | 2 |
| `execution_error` | Catch-all for other errors | 1 |

### Command-Specific Data

#### up / down / reset (execution result)

```json
{
  "command": "up",
  "migrated": [
    {
      "Name": "20260730094235_create_users",
      "Direction": "up",
      "Batch": 1,
      "Applied": true,
      "Duration": 120000000
    }
  ],
  "errors": [],
  "count": 1
}
```

#### up / down / reset (pretend / preview)

```json
{
  "command": "up",
  "directory": "sql/migrations",
  "table_name": "migrations",
  "migrations": ["20260730094235_create_users"],
  "dry_run": true,
  "batch": 0,
  "count": 1
}
```

#### status

```json
{
  "migrations": [
    {
      "Name": "20260730094235_create_users",
      "Filename": "20260730094235_create_users.up.sql",
      "Status": "applied",
      "Batch": 1,
      "AppliedAt": "2026-07-30 12:30:45.000000",
      "UpChecksum": "a1b2c3...",
      "Drift": false
    },
    {
      "Name": "20260730102802_add_email_index",
      "Filename": "20260730102802_add_email_index.up.sql",
      "Status": "pending",
      "Batch": 0,
      "AppliedAt": "",
      "UpChecksum": "",
      "Drift": false
    }
  ],
  "count": 2
}
```

#### make (migration creation)

```json
{
  "name": "create_users_table",
  "up_path": "sql/migrations/20260730123045_create_users_table.up.sql",
  "down_path": "sql/migrations/20260730123045_create_users_table.down.sql",
  "template": "create_table"
}
```

#### import

```json
{
  "command": "import",
  "status": "success"
}
```

#### version

```json
{
  "version": 1,
  "command": "version",
  "data": {
    "version": "0.1.0-experimental"
  }
}
```

## Signal Handling

lamigrate handles the following OS signals:

| Signal | Behavior |
|--------|----------|
| `SIGINT` | Cancels the running operation via context cancellation |
| `SIGTERM` | Cancels the running operation via context cancellation |

Signal handling is implemented via `signal.NotifyContext`. When a signal is received:

1. The context is canceled, which propagates to all database operations.
2. lamigrate attempts cleanup: rollback open transactions, restore session state, release advisory locks, and close connections.
3. Cleanup uses a fresh bounded internal context, not the canceled command context.
4. If cleanup is uncertain, the command reports `outcome_unknown` or `cleanup_uncertain` and exits with code 1.

**On Windows:** `SIGTERM` is accepted by Go even though Windows has no native SIGTERM. Only `os.Interrupt` (equivalent to `SIGINT`) is delivered by the OS.

**In non-interactive mode** (piped stdin, CI): Destructive commands (`reset`, `import`) require `--yes` or they abort immediately.

## Interactive Confirmation

The following commands require confirmation in interactive mode:

| Command | Prompt |
|---------|--------|
| `reset` | "This will rollback ALL migrations. Continue? [y/N]" |
| `import` | "This will import legacy migrations as already applied. Continue? [y/N]" |

Confirmation behavior:

- **Interactive terminal:** Prompts for `[y/N]`. Accepts `y`, `Y`, `yes`, `YES`. Any other input or `Enter` aborts.
- **Non-interactive** (piped stdin): Aborts with exit code 2 and message "aborted: non-interactive mode requires --yes for destructive operations".
- **`--yes` / `-y`:** Skips the prompt entirely.

## DSN Warning

When `-dsn` is used directly (not from config file or environment variable), the CLI writes to stderr:

```
Warning: -dsn flag exposes credentials in shell history and process list. Consider using LAMIGRATE_DSN or a config file instead.
```

This warning is informational and does not affect operation.

## Task Card Reference

The CLI foundation is defined in [LM-012](task-cards/LM-012-cli-foundation.md) and the operational CLI with JSON output is [LM-030](task-cards/LM-030-operational-cli-json.md). Signal handling and exit codes are part of the CLI contract in [architecture.md](../architecture.md) sections 14.
