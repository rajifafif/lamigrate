# lamigrate

Laravel-style database migrations for Go + MySQL.

Timestamp-based filenames, batch-tracked rollback, pretend mode -- the workflow you know from `php artisan migrate`, now in Go.

> **Status: experimental, pre-1.0.** All 25 implementation tasks are complete.
> The tool is functional and tested against real MySQL 8.4. It has not yet
> received an independent security review or release certification.
> `architecture.md` defines the production target. Do not use in production
> without completing the pre-1.0 gaps listed below.

## Features

- **Timestamp filenames** -- `20260730094235_create_users.up.sql` (no collision, sortable)
- **Batch tracking** -- rollback by batch, not by version number
- **Pretend mode** -- see what would happen without executing (`--pretend`)
- **Checksums** -- SHA-256 checksums detect file drift after application
- **Status reporting** -- applied, pending, baseline, dirty, drift detection
- **Legacy import** -- one-command import from golang-migrate numbered files
- **Configuration** -- YAML, `.env`, or environment variable (see [Configuration](docs/configuration.md))
- **JSON output** -- structured JSON for scripting (`--json`, experimental schema v1)
- **Minimal dependencies** -- `go-sql-driver/mysql` and `gopkg.in/yaml.v3` plus their transitive deps
- **Standalone CLI** or **Go library** -- use however you want

## Installation

```bash
go install github.com/rajifafif/lamigrate/cmd/lamigrate@latest
```

Requires Go 1.24 or later. To install an exact release, replace `@latest` with a tag:

```bash
go install github.com/rajifafif/lamigrate/cmd/lamigrate@v0.1.2-experimental
```

`go install` writes binaries to `$(go env GOBIN)` when set, otherwise to `$(go env GOPATH)/bin`. Ensure that directory is on your shell `PATH` before running the command:

```bash
export PATH="$(go env GOPATH)/bin:$PATH"
lamigrate version
```

For zsh, make it persistent by adding the export line to `~/.zshrc`, then start a new terminal or run `source ~/.zshrc` and `rehash`.

Cross-builds are available for Linux, macOS, and Windows on amd64 and arm64. CI evidence for all six platform/architecture pairs is in [docs/ci-evidence.md](docs/ci-evidence.md).

## Quick Start

### 1. Create a migration

```bash
lamigrate migration create create_users_table
```

This creates a timestamped pair of files (no database connection required):

```text
sql/migrations/
  20260730123045_create_users_table.up.sql
  20260730123045_create_users_table.down.sql
```

### 2. Edit the SQL

Write your `CREATE TABLE` / `ALTER TABLE` statements in the `.up.sql` file and the corresponding rollback in the `.down.sql` file. Unfinished templates contain a `SIGNAL` guard that blocks execution until you remove it.

### 3. Apply

```bash
lamigrate -config config.yaml up
```

### 4. Check status

```bash
lamigrate -config config.yaml status
```

### 5. Rollback

```bash
lamigrate -config config.yaml down
```

## CLI Reference

```text
lamigrate [global-flags] <command> [command-args]
```

### Commands

| Command | Description |
|---------|-------------|
| `up [--step N]` | Apply next N pending migrations (all if omitted) |
| `down [--step N]` | Rollback N from last batch (all in batch if omitted) |
| `reset` | Rollback ALL migrations (requires confirmation or `--yes`) |
| `status` | Show applied vs pending migrations |
| `migration create <name>` | Create a `.up.sql`/`.down.sql` pair (no DSN required) |
| `make <name>` | Compatibility alias for `migration create` |
| `make:migration <name>` | Laravel-style alias for `migration create` |
| `import` | Import legacy numbered files as already applied (requires confirmation or `--yes`) |
| `version` | Print version and exit |

### Global Flags

| Flag | Default | Description |
|------|---------|-------------|
| `-dir` | `sql/migrations` | Migrations directory |
| `-dsn` | -- | MySQL DSN (overrides config; warns about shell history exposure) |
| `-config` / `--config` | -- | Explicit path to config file |
| `-table` | `migrations` | Tracking table name |
| `-pretend` / `--pretend` | false | Show SQL without executing |
| `-y` / `--yes` | false | Skip confirmation prompts (`reset`, `import`) |
| `--json` | false | Output structured JSON (experimental, schema v1) |
| `-h` / `--help` | -- | Show help text |

Global flags must appear **before** the command name. See [docs/cli-reference.md](docs/cli-reference.md) for the complete reference including exit codes, JSON schema, and signal handling.

### Examples

```bash
# Apply all pending migrations
lamigrate -config config.yaml up

# Apply exactly 2 pending migrations
lamigrate -config config.yaml --step 2 up

# Dry run (pretend) -- show what would happen
lamigrate -config config.yaml --pretend down

# Check status
lamigrate -config config.yaml status

# Create a new migration (offline, no DSN needed)
lamigrate -dir sql/migrations migration create create_users_table

# Import legacy numbered files (one-time)
lamigrate -config config.yaml -y import

# JSON output
lamigrate --json version
```

## Configuration

lamigrate supports multiple configuration sources with a well-defined precedence order. Full details are in [docs/configuration.md](docs/configuration.md).

### Precedence Order

1. `-dsn` flag (highest priority; warns about shell history exposure)
2. `LAMIGRATE_DSN` environment variable
3. `-config` flag pointing to a specific file
4. Default search in current directory: `config.yaml`, `config.yml`, `.env`

### YAML Configuration

```yaml
dbMySQL:
  host: localhost
  port: 3306
  user: migration_user
  pass: your_password
  dbName: my_application
  timeout: 30s
```

### Environment Variables

```bash
LAMIGRATE_DB_HOST=localhost
LAMIGRATE_DB_PORT=3306
LAMIGRATE_DB_USER=migration_user
LAMIGRATE_DB_PASS=your_password
LAMIGRATE_DB_NAME=my_application
```

Or use a direct DSN:

```bash
LAMIGRATE_DSN="user:pass@tcp(localhost:3306)/dbname"
```

> **Security note:** The `-dsn` flag exposes credentials in shell history and
> process listings. Use `LAMIGRATE_DSN` or a config file instead. Config files
> should be regular files with restricted permissions and added to `.gitignore`.

## Migration File Format

```
sql/migrations/
  20260730094235_create_users.up.sql
  20260730094235_create_users.down.sql
  20260730102802_add_email_index.up.sql
  20260730102802_add_email_index.down.sql
```

- Filename pattern: `YYYYMMDDHHMMSS_description.up.sql` / `.down.sql`
- Description: lowercase ASCII snake_case (`[a-z][a-z0-9_]*`)
- Timestamp determines execution order (UTC)
- Each migration must have both an up and down file
- SHA-256 checksums are computed over exact file bytes
- Default maximum file size: 1 MB (configurable via `Options.MaxFileSize`)

For the complete file contract, naming rules, templates, and irreversible migration markers, see [docs/migration-format.md](docs/migration-format.md).

## Library Usage

### Production API (recommended)

```go
package main

import (
    "context"
    "fmt"
    "log"

    "github.com/rajifafif/lamigrate"
)

func main() {
    ctx := context.Background()

    opts := lamigrate.Options{
        Directory: "sql/migrations",
        TableName: "migrations",     // default
        LockTimeout: 30 * time.Second, // default
    }

    m, err := lamigrate.OpenMySQL("user:pass@tcp(localhost:3306)/mydb", opts)
    if err != nil {
        log.Fatal(err)
    }

    // Apply all pending
    result, err := m.Up(ctx, lamigrate.All())
    if err != nil {
        log.Fatal(err)
    }
    fmt.Printf("Applied %d migration(s)\n", len(result.Migrated))

    // Apply next 3
    limit, _ := lamigrate.Steps(3)
    result, err = m.Up(ctx, limit)

    // Rollback last batch
    result, err = m.Down(ctx, lamigrate.All())

    // Rollback 2 from last batch
    limit, _ = lamigrate.Steps(2)
    result, err = m.Down(ctx, limit)

    // Rollback everything
    result, err = m.Reset(ctx)

    // Check status (side-effect free, no metadata creation)
    report, err := m.Status(ctx)
    for _, s := range report.Migrations {
        fmt.Printf("%-40s %-12s batch=%s\n", s.Filename, s.Status, s.Batch)
    }

    // Preview without executing
    plan, err := m.PreviewUp(ctx, lamigrate.All())
    fmt.Printf("Would apply %d migration(s)\n", len(plan.Migrations))
}
```

### Creating migrations offline

```go
// Create a migration pair (no database connection needed)
created, err := lamigrate.Make(ctx, "sql/migrations", "create_users_table")
if err != nil {
    log.Fatal(err)
}
fmt.Println(created.UpPath)   // sql/migrations/20260730123045_create_users_table.up.sql
fmt.Println(created.DownPath) // sql/migrations/20260730123045_create_users_table.down.sql
```

### Step Limits

`StepLimit` controls how many migrations are processed. A zero value is invalid and must be rejected before any I/O.

```go
limit, err := lamigrate.Steps(5) // at most 5 migrations
limit = lamigrate.All()          // every eligible migration
```

### Constructor

```go
// OpenMySQL parses a DSN and creates a Migrator. No network I/O.
m, err := lamigrate.OpenMySQL(dsn, opts)

// NewMySQL from a go-sql-driver/mysql Config. Defensive clone.
m, err := lamigrate.NewMySQL(config, opts)
```

Both constructors perform local validation only. They do not connect to or mutate the database.

### Legacy API (deprecated)

The original `New()` constructor and `Migrate` type are retained for backward compatibility and will be removed in a future release. Use `OpenMySQL` / `NewMySQL` instead.

## Supported MySQL Versions

| Version | Status |
|---------|--------|
| MySQL 8.0 (latest patch) | Supported, CI-tested |
| MySQL 8.4 LTS | Supported, CI-tested |
| MariaDB | Not supported |

Both versions are tested in CI using pinned Docker images (`mysql:8.0.35` and `mysql:8.4`). Unpinned `latest` images are not used.

### MySQL Requirements

- `@@lower_case_table_names = 0` (required for lock protocol v1)
- Connection and result character sets must be `utf8mb4`
- Database name must match `[A-Za-z_][A-Za-z0-9_]*` and be at most 64 bytes
- Multi-statement support must be enabled (lamigrate configures this automatically via the driver)
- Advisory lock support (`GET_LOCK` / `RELEASE_LOCK`) is required

### Required Privileges

Migration operations require the following MySQL privileges:

- `SELECT` (metadata reads, capability probes)
- `INSERT`, `UPDATE`, `DELETE` (metadata state management)
- `CREATE TABLE` (tracking table initialization)
- `ALTER TABLE` (if tracking table needs schema updates)
- `DROP TABLE` (for rollback table deletion)
- `GET_LOCK`, `RELEASE_LOCK` (advisory locking)
- `FILE` (for certain diagnostic queries)

### DDL Limitations

MySQL DDL statements (`CREATE TABLE`, `ALTER TABLE`, `DROP TABLE`, etc.) may implicitly commit open transactions. A migration file is not assumed to be atomic. If a multi-statement migration fails partway through, earlier statements may have already taken effect. The metadata state becomes dirty and requires manual reconciliation. See [architecture.md](architecture.md) section 11 for details.

## Limitations and Known Issues

This is an experimental pre-1.0 release. The following limitations apply:

### Architecture Limitations

- **No PostgreSQL, SQLite, or MariaDB support.** MySQL 8.x only.
- **No distributed coordination.** Running multiple migration tools against the same database simultaneously is unsupported.
- **No automatic recovery from partial DDL.** MySQL cannot prove whether interrupted DDL completed. Recovery requires explicit operator decision.
- **No SQL rewriting.** Client-only directives (`DELIMITER`) are not supported in migration files.
- **No online schema change.** `gh-ost` or `pt-online-schema-change` orchestration is not included.
- **No sandbox for untrusted migration files.** Migration files are trusted source code — anyone who can modify a migration file can execute arbitrary SQL using the configured database credentials.
- **Lock protocol v1 restrictions.** Requires `@@lower_case_table_names = 0` and ASCII-only database names matching `[A-Za-z_][A-Za-z0-9_]*`.

### What IS Implemented (Not Just Planned)

All 25 implementation tasks from the task board are complete:

| Feature | Status | Task |
|---------|--------|------|
| Advisory lock protocol v1 | Done | LM-021 |
| Metadata v1 with control table | Done | LM-022 |
| Execution state machine (applying/applied/apply_failed/rolling_back/rollback_failed) | Done | LM-024 |
| Batch semantics (monotonic, never-reused) | Done | LM-024 |
| Import from golang-migrate | Done | LM-025 |
| Prototype adoption (4-column detection, atomic swap) | Done | LM-026 |
| Repair workflows (mark-applied, mark-rolled-back, remove-failed) | Done | LM-027 |
| Checksums and drift detection | Done | LM-011, LM-023 |
| Immutable planner with dry-run parity | Done | LM-023 |
| Side-effect-free constructors | Done | LM-010 |
| Private session lifecycle (fresh connector per phase) | Done | LM-020 |

### Pre-1.0 Gaps

The tool works but has not yet been independently reviewed for production use:

- **Independent security review pending.** The advisory lock, metadata transactions, and crash-recovery paths need external audit.
- **Clean-install smoke test needed.** The tool has not been tested from a fresh `go install` in a clean environment.
- **Signed release artifacts pending.** SBOM and provenance attestations are not yet generated.
- **MySQL 8.0 CI evidence only.** Local testing here runs against MySQL 8.4; MySQL 8.0 is tested in CI only.

### Status Codes

| Code | Meaning |
|------|---------|
| 0 | Success |
| 1 | Execution or unexpected internal error |
| 2 | Usage or configuration error |
| 3 | Lock unavailable or timed out |
| 4 | Dirty state, drift, or preflight safety failure |

## Further Documentation

| Document | Description |
|----------|-------------|
| [architecture.md](architecture.md) | Target production architecture and safety invariants |
| [docs/configuration.md](docs/configuration.md) | Configuration reference (YAML, .env, precedence) |
| [docs/cli-reference.md](docs/cli-reference.md) | Complete CLI command reference |
| [docs/migration-format.md](docs/migration-format.md) | Migration file format and contract |
| [docs/ci-evidence.md](docs/ci-evidence.md) | CI matrix evidence and job mapping |

## License

MIT
