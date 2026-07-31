# LM-003 — Configuration-Source and Credential Policy Proposal

- Status: IN_PROGRESS → DONE (coordinator-executed)
- Owner: Coordinator (wave-1 subagent executed directly)
- Baseline: 73014dc (LM-004 reconciled baseline)
- Type: Non-authoritative policy proposal for LM-000 approval

## 1. Existing Baseline Code

The repository already contains `cmd/config.go` with:
- YAML parsing (`gopkg.in/yaml.v3`) with strict `dbMySQL` mapping
- `.env` parsing with multiple key fallback patterns
- DSN construction via `go-sql-driver/mysql`
- Config file search: `config.yaml` → `config.yml` → `.env` in current directory
- File constraints: regular file, 1MB max

This code is committed at HEAD (73014dc). LM-000 decides whether to adopt or revert.

## 2. Configuration Precedence (Proposed)

Exact precedence order for database commands:

1. Explicit command-line `-dsn <value>` (highest priority)
2. `LAMIGRATE_DSN` environment variable
3. Explicit `--config <path>` flag
4. Default config file search in project root: `config.yaml` → `config.yml` → `.env`
5. No configuration found → error with actionable guidance

Offline commands (`make`, `make:migration`, `migration create`, `help`, `version`) MUST NOT read any configuration file or connect to any database. They operate on the `--dir` flag only.

### Rationale for inline -dsn
Inline DSNs appear in shell history and `ps` output. The proposal RECOMMENDS `LAMIGRATE_DSN` or config file over inline `-dsn` but DOES NOT remove `-dsn` support for backward compatibility and scripting convenience. A warning is emitted when `-dsn` is used directly.

### DSN file alternative
`--dsn-file <path>` is NOT included in this proposal. The YAML config provides the same capability with structured fields and validation. If `--dsn-file` is desired, it can be added as a separate decision by LM-000.

## 3. YAML Configuration Policy

### File format

```yaml
dbMySQL:
  host: db.example.invalid
  port: 3306
  user: migration_user
  pass: ${LAMIGRATE_DB_PASS}
  dbName: application_dev
  timeout: 30s
```

### Field requirements

| Field | Required | Type | Default | Validation |
|-------|----------|------|---------|------------|
| `host` | YES | string | — | Non-empty after trim |
| `port` | no | int | 3306 | 1–65535; 0 or absent → default 3306 |
| `user` | YES | string | — | Non-empty after trim |
| `pass` | no | string | "" | No validation; may be empty for local trust auth |
| `dbName` | YES | string | — | Non-empty after trim |
| `timeout` | no | string | "30s" | Go duration; must parse and be positive |

### Strict field policy
- `KnownFields(true)` on the YAML decoder: unknown fields are rejected with an error.
- Only the exact fields above are accepted under `dbMySQL`.
- No additional top-level keys are accepted.
- Multiple YAML documents in one file are rejected.

### Secret handling in YAML
- The `pass` field value is treated as a credential.
- It MUST NOT appear in logs, error messages, structured output, or `--json` output.
- When displaying DSN-related diagnostics, credentials are replaced with `***`.
- The config file should be listed in `.gitignore` and distributed as `.env.example` / `config.yaml.example`.

## 4. .env Configuration Policy

### Exact environment variable keys

For `.env` files, the following keys are recognized (in priority order per field):

| Field | Keys (first match wins) |
|-------|-------------------------|
| DSN (shortcut) | `LAMIGRATE_DSN` |
| host | `LAMIGRATE_DB_HOST`, `DB_MYSQL_HOST`, `DB_HOST` |
| timeout | `LAMIGRATE_DB_TIMEOUT`, `DB_MYSQL_TIMEOUT`, `DB_TIMEOUT` |
| port | `LAMIGRATE_DB_PORT`, `DB_MYSQL_PORT`, `DB_PORT` |
| user | `LAMIGRATE_DB_USER`, `DB_MYSQL_USER`, `DB_USER` |
| pass | `LAMIGRATE_DB_PASS`, `DB_MYSQL_PASS`, `DB_PASS`, `DB_PASSWORD` |
| dbName | `LAMIGRATE_DB_NAME`, `DB_MYSQL_DB_NAME`, `DB_NAME`, `DB_DATABASE` |

### .env may contain LAMIGRATE_DSN
If `.env` defines `LAMIGRATE_DSN`, that DSN is used directly and individual field keys are ignored for DSN construction.

### .env parsing rules
- Blank lines and lines starting with `#` are ignored.
- `export` prefix is stripped.
- Keys: `[A-Z][A-Z0-9_]*` only.
- Values: unquoted, single-quoted, or double-quoted (Go `strconv.Unquote`).
- Inline comments: ` #` separates value from comment (space + hash).
- Max file size: 1MB (same as YAML).

## 5. Project-Root / Current-Directory Discovery

Config file search order:
1. If `--config <path>` is specified: use that exact path; error if missing or unreadable.
2. Otherwise, search in the current working directory for:
   - `config.yaml`
   - `config.yml`
   - `.env`
3. The first existing regular file found is used.
4. If none found: error with message listing all checked paths and suggesting `-dsn`, `LAMIGRATE_DSN`, or creating a config file.

### No parent-directory walk
Config discovery does NOT walk up to parent directories. This prevents accidental credential loading from system-wide or unrelated project directories. The `--dir` flag for migrations also defaults to `sql/migrations` relative to CWD, so CWD is the natural project root.

## 6. File Safety Policy

### Regular file requirement
Config files MUST be regular files (not symlinks, not directories, not FIFOs). `os.Lstat` checks before reading.

### Size limit
Maximum 1MB. Larger files are rejected with a clear error message.

### Permission guidance
- The proposal RECOMMENDS owner-only read permissions on config files containing credentials (POSIX 0600), but does NOT enforce this at runtime because Windows doesn't support POSIX permission bits.
- The `.gitignore` and example-file patterns serve as the primary protection against accidental credential commit.

### Symlink policy
Following LM-004's worktree reconciliation pattern and architecture §8.1, config file paths reject symlinks. The existing `readConfigFile` uses `os.Stat` (follows symlinks); this should be changed to `os.Lstat` to reject symlink config files, consistent with migration-file handling.

## 7. TLS Policy

### For the first production release
- TLS configuration is passed through `go-sql-driver/mysql` DSN parameters (`tls=true`, `tls=custom`, etc.).
- The YAML `.env` config does NOT expose TLS fields; operators configure TLS in the DSN or via environment variables that affect the driver.
- Production documentation MUST include TLS examples for remote connections.
- A warning is emitted when connecting to a non-localhost host without TLS configuration (advisory, not blocking — local dev shouldn't require TLS).

### Remote-host warning
When the host is not `localhost`, `127.0.0.1`, or `::1`, and no `tls` parameter is present in the constructed DSN, emit to stderr:
```
Warning: connecting to remote host without TLS. Use TLS in production.
```
This is advisory (exit code 0) and does not block execution.

## 8. DSN Redaction Requirements

All of the following MUST NOT contain the database password in any output path:
- Error messages from config resolution
- Structured JSON output
- `--pretend` output
- Debug/verbose logging (if added later)
- `status` output
- Process arguments displayed in diagnostics

Redaction approach: when the password is non-empty, replace with `***` in all display paths. The DSN is constructed internally and only the redacted version appears in any user-facing context.

## 9. MySQL DSN Parameters

The config loader MUST set these go-sql-driver/mysql parameters:
- `MultiStatements=true` (required for multi-statement migrations)
- `ParseTime=true` (required for timestamp handling)
- `Timeout` from config or default 30s
- `ReadTimeout` and `WriteTimeout` set to the same value as Timeout
- `Net=tcp` (explicit)

These are NOT configurable by the operator because they are required for correct lamigrate behavior.

## 10. Offline Command Rule

**Normative requirement:** Offline commands (`make`, `make:migration`, `migration create`, `help`, `version`) MUST NOT:
- Read any configuration file
- Resolve any DSN
- Open any database connection
- Emit any credential-related output

This is enforced by the CLI boundary: config resolution only happens after `isDatabaseCommand()` returns true.

## 11. Architecture Sections Requiring LM-000 Approval

| Section | Topic | LM-003 Decision |
|---------|-------|------------------|
| §5.8 | Secret-safe diagnostics | Proposal defines redaction policy |
| §6.1 | CLI boundary (config responsibility) | CLI owns config resolution |
| §14 | CLI contract (flags) | Adds --config flag; keeps -dsn with warning |
| §15 | Configuration and credentials | Full proposal in this document |
| §16 | Errors and observability | Config-resolution errors are category 2 (usage) |

## 12. LM-013 Implementation Scope

When LM-000 approves this proposal, LM-013 must implement:
1. Config resolution function with the exact precedence order
2. YAML parser with strict field policy
3. .env parser with the exact key table
4. Config file discovery in CWD only
5. Regular-file and size enforcement (Lstat)
6. DSN construction with required MySQL parameters
7. Password redaction in all output paths
8. Remote-host TLS advisory warning
9. -dsn inline usage warning
10. Offline command bypass (no config read)

## 13. LM-045 Documentation Scope

When LM-045 publishes technical docs, it must document:
1. Configuration precedence order
2. YAML field reference with types and defaults
3. .env key reference with fallback order
4. Config file discovery rules
5. TLS configuration guidance
6. Security guidance for credential files
7. `.gitignore` and example-file templates
8. Remote-host warning behavior

## 14. Decision Required from LM-000

1. **Adopt or revert existing cmd/config.go**: The current baseline includes YAML + .env parsing. Does LM-013 build on this or replace it?
2. **Inline -dsn support**: Keep with warning, or remove entirely?
3. **--dsn-file flag**: Include in this release, or defer?
4. **Remote-host TLS warning**: Advisory (current proposal) or blocking?
5. **.env key table**: The multi-key fallback (LAMIGRATE_DB_*, DB_MYSQL_*, DB_*) is extensive. Accept or simplify to one key per field?
