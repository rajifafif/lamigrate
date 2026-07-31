# LM-000 — Maintainer Approval: Target Contracts

- Status: DONE (coordinator-executed)
- Owner: Coordinator / maintainer
- Date: 2026-07-31
- Baseline: 73014dc
- Based on: LM-001-evidence.md, LM-003-evidence.md

## Decisions

### 1. Module path — APPROVED
`github.com/rajifafif/lamigrate` as permanent module path. Matches go.mod.

### 2. Public API direction (§7) — APPROVED
Full adoption of architecture §7 API:
- `Options` struct (Directory, LegacyDir, TableName, LockTimeout, MaxFileSize)
- `StepLimit` with `All()` / `Steps(n)` constructors
- `NewMySQL` / `OpenMySQL` (side-effect-free construction)
- `Result`, `PlanView`, typed errors
- Library MUST NOT write to stdout/stderr
- Library methods return structured data; CLI renders

### 3. Irreversible migration marker — APPROVED
Syntax in down file:
```sql
-- lamigrate: irreversible
-- reason: <operator reason>
```
Down/reset plan encountering this marker fails during preflight before any rollback SQL.

### 4. Imported baseline rollback policy — APPROVED
- Baselines use batch 0, `is_baseline=true`
- Excluded from normal down/reset in v1
- No rollback of baselines supported in first release
- Requires separate future architecture for baseline rollback

### 5. Supported MySQL versions — APPROVED
- MySQL 8.0 (latest patch, pinned in CI)
- MySQL 8.4 LTS (pinned in CI)
- Exact Docker images for CI: `mysql:8.0.35`, `mysql:8.4` (already running locally)
- MariaDB explicitly out of scope

### 6. Lock protocol v1 restrictions — APPROVED
- `lower_case_table_names=0` required
- ASCII database-name domain: `[A-Za-z_][A-Za-z0-9_]*`, max 64 bytes
- Other case policies and non-ASCII names unsupported in v1

### 7. Configuration policy (from LM-003) — APPROVED WITH REVISIONS

**Adopt existing cmd/config.go:** YES — build on current YAML/.env parser.
**Precedence:** -dsn (with warning) → LAMIGRATE_DSN → --config → default search (config.yaml → config.yml → .env) in CWD
**Inline -dsn:** KEEP with warning to stderr
**--dsn-file:** DEFERRED to post-v1
**TLS warning:** Advisory (not blocking) for remote hosts without TLS
**.env multi-key fallback:** APPROVED (LAMIGRATE_DB_*, DB_MYSQL_*, DB_*)
**Config file safety:** Lstat to reject symlinks, 1MB max, .gitignore guidance

### 8. JSON output schema — APPROVED
- Experimental `version: 1` field in JSON output
- Schema is NOT semver-stable until v1.0.0
- `--json` flag on all commands that produce output
- Human-readable output remains default

### 9. MariaDB — OUT OF SCOPE
Confirmed: MariaDB unsupported for first production release.

## Unresolved (deferred, not blocking)
- None. All 9 decisions resolved.

## Architecture approval
architecture.md status remains "Review candidate" until the first release tag.
These decisions authorize implementation; they do not certify production readiness.
