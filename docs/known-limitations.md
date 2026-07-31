# Known Limitations

> **Status:** Experimental pre-1.0
> **Last updated:** 2026-07-31
> **Scope:** Limitations of the current implementation and target architecture.
> Not all limitations may be fixed before v1.0 — some are by design.

---

## 0. Experimental status disclaimer

This is an **experimental pre-1.0 release**. The public API, metadata schema,
and CLI interface may change without notice. It does not claim production
safety. See [architecture.md](../architecture.md) for the target architecture
and production-ready criteria. See [release-certification.md](release-certification.md)
for the current §22 certification assessment.

---

## 1. Database engine limitations

### No PostgreSQL support
lamigrate targets MySQL exclusively. PostgreSQL, SQLite, and MariaDB are not
supported and are explicitly listed as non-goals for the first production
release (architecture.md §3). Adding support for other engines requires a
dedicated design, driver, and CI test matrix.

### No SQLite support
Same as above. SQLite lacks advisory locks, multi-statement transaction
semantics, and DDL behavior compatible with lamigrate's safety model.

### No MariaDB support
MariaDB compatibility is not tested or guaranteed. While MariaDB shares a
MySQL wire protocol, differences in DDL implicit-commit behavior, advisory lock
semantics, and `lower_case_table_names` handling may cause incorrect results.

---

## 2. Concurrency and coordination limitations

### No distributed coordination
lamigrate coordinates only lamigrate clients using lock protocol v1 via MySQL
advisory locks. Running another migration tool (Flyway, golang-migrate,
Liquibase, etc.) against the same database simultaneously is unsupported and
may cause metadata corruption.

### Lock protocol v1 restrictions
The current advisory lock protocol (v1) has specific requirements:

| Requirement | Reason |
|-------------|--------|
| `@@lower_case_table_names = 0` | Lock key derivation uses byte-exact database name. Case-insensitive modes (1, 2) would break key matching. |
| ASCII database names only | Lock key is derived from `SELECT DATABASE()` and must satisfy `[A-Za-z_][A-Za-z0-9_]*` (max 64 bytes). Non-ASCII names are rejected. |
| Lowercase tracking table names | Tracking table name is lowercase-only to avoid table-alias ambiguity. |
| Single database scope | Lock scope is per-database. Cross-database migrations are not supported. |
| `GET_LOCK` / `RELEASE_LOCK` required | Advisory lock support must be available on the MySQL server. |

A future lock protocol may relax some of these restrictions, but the v1 key
algorithm is permanent — any protocol migration must coordinate old and new
locks explicitly.

---

## 3. DDL and recovery limitations

### No automatic recovery from partial DDL
MySQL DDL statements (`CREATE TABLE`, `ALTER TABLE`, `DROP TABLE`, etc.) may
implicitly commit open transactions. When a multi-statement migration fails
partway through, earlier statements may have already taken effect. MySQL cannot
prove whether an interrupted DDL completed.

Recovery requires an explicit operator decision via the `repair` command.
lamigrate does not attempt automatic DDL rollback or undo.

### No SQL parser or rewriter
lamigrate does not parse, analyze, or rewrite SQL. Migration files are executed
as-is by the MySQL driver's multi-statement execution. Client-only directives
(`DELIMITER`, `SOURCE`, `SET STATEMENT`) are not supported and will cause
errors.

### No online schema change
`gh-ost`, `pt-online-schema-change`, or similar online DDL orchestration is
not included. For large tables, operators must manage online schema changes
outside lamigrate and then record the result as an already-applied migration.

---

## 4. Security limitations

### No sandbox for untrusted migration files
Migration files are executed with the full privileges of the configured MySQL
connection. Anyone who can write to the migration directory can execute
arbitrary SQL. Migration files must be treated as trusted code. The `.gitignore`
and CI gates help prevent accidental inclusion, but there is no runtime sandbox.

### No migration file signing
Migration files are not cryptographically signed. Integrity relies on
SHA-256 checksums stored in the tracking table (drift detection), not on
digital signatures. A compromised file will be detected on drift check but
not rejected by hash mismatch if the compromised version is what was applied.

### Credentials in shell history
When using the `-dsn` flag directly, credentials are exposed in shell history
and process list. The CLI warns about this. Users should prefer `LAMIGRATE_DSN`
environment variables or configuration files.

---

## 5. Feature limitations

### No GUI
lamigrate is a CLI tool and Go library. There is no graphical interface.

### No schema builder or visual editor
Migration SQL must be written by hand in `.sql` files. There is no DSL, schema
builder, or visual migration editor.

### No auto-generated rollback
The `migration create` command generates template `.down.sql` files, but the
rollback SQL is a template that requires manual review and completion. Complex
migrations require hand-written rollback SQL.

### No dry-run SQL execution preview
The `--pretend` flag shows which migrations would be applied or rolled back,
but does not show the actual SQL content. There is no `EXPLAIN` or SQL preview
mode.

### No migration dependency graph
Migrations are ordered strictly by timestamp. There is no dependency
declaration between migrations (e.g., "migration B depends on migration A").
The only ordering guarantee is timestamp ascending.

### No down-migration validation
lamigrate does not validate that `.down.sql` files can successfully reverse
their `.up.sql` counterparts. A broken rollback is only discovered at rollback
time.

---

## 6. Operational limitations

### No automatic backup before migration
lamigrate does not automatically create database backups before executing
migrations. Operators should implement their own backup strategy.

### No migration file locking during execution
Multiple developers can create migration files with the same timestamp. The
timestamp uniqueness check is local to the filesystem at creation time and does
not prevent concurrent creation by separate processes.

### No remote migration directory
The migrations directory must be accessible on the local filesystem. There is
no support for remote migration sources (S3, git, HTTP).

### No transaction wrapping
MySQL DDL may implicitly commit transactions. A migration file is not executed
within a transaction that can be rolled back. Non-DDL statements in a
multi-statement file are also not transactionally atomic unless MySQL's
autocommit behavior permits it.

---

## 7. Testing limitations

### No failpoint injection framework
Integration tests cover controlled SQL failures and dirty-state scenarios, but
there is no formal failpoint injection framework for OS-level crash simulation
(SIGKILL, SIGTERM during DDL). Crash-window coverage relies on SQL-level
failure injection.

### No outcome-unknown recovery integration test
The `ErrOutcomeUnknown` error path is implemented and the sentinel is tested,
but there is no dedicated integration test that simulates a lost commit
acknowledgement and verifies the subsequent reconciliation.

### Integration tests require MySQL
Full integration test coverage requires a running MySQL 8.0 or 8.4 instance.
CI provides this via Docker service containers, but local development without
Docker limits test coverage to unit tests only.

---

## 8. Release and packaging limitations

### No published release artifacts yet
GoReleaser configuration exists but no actual release has been published. The
SBOM and provenance pipeline has not been validated end-to-end.

### No clean-install smoke test
Automated `go install github.com/rajifafif/lamigrate/cmd/lamigrate@<tag>` from
a clean GOPATH has not been verified in CI.

### No signed release artifacts
Release binaries and checksums are not GPG/Sigstore signed. Checksums are
SHA-256 only.

### Experimental API stability
The public Go API (`lamigrate.Options`, `lamigrate.OpenMySQL`, `lamigrate.Up`,
etc.) may change without notice between experimental releases. There is no
backward compatibility guarantee until v1.0.0.

---

## 9. Architecture vs. implementation gaps

The following production-architecture targets (architecture.md) have partial
or no implementation:

| Feature | Architecture section | Status |
|---------|---------------------|--------|
| Irreversible migration markers | §8 | Partially implemented — error sentinel exists but full `--pretend` integration not complete |
| Prototype adoption workflow | LM-026 | Implemented per task card status |
| Legacy import with source validation | LM-025 | Implemented per task card status |
| Repair workflows | LM-027 | Implemented with integration tests |
| CI security scaffolding | LM-040 | Implemented per task card status |
| Full CI evidence matrix | LM-044 | Scaffolding exists; activation pending |
| Technical docs support matrix | LM-045 | Pending |
| Release supply chain | LM-042 | Implemented (GoReleaser config, Makefile) |

---

## 10. What is NOT a limitation

For clarity, the following are intentional design decisions, not limitations:

- **MySQL-only scope:** Deliberate focus for v1.0. Multi-engine support is a
  future consideration, not a missing feature.
- **No auto-rollback of DDL:** MySQL makes this impossible. The `repair`
  workflow is the correct approach.
- **File-based migrations:** Intentional — provides version control
  integration, code review, and auditability that database-only approaches
  lack.
- **Timestamp-based ordering:** Intentional — deterministic, merge-safe, and
  avoids version-number conflicts in parallel development.
- **SHA-256 drift detection (not signing):** Checksums detect accidental
  modification. File signing is a separate security concern addressed by
  Git commit signatures and CI gates.

---

## References

- [architecture.md](../architecture.md) — target production architecture
- [architecture.md §3](../architecture.md#3-non-goals-for-the-first-production-release) — non-goals for first release
- [architecture.md §22](../architecture.md#22-definition-of-production-ready) — definition of production ready
- [release-certification.md](release-certification.md) — §22 certification assessment
- [ci-evidence.md](ci-evidence.md) — CI evidence matrix
- [release-process.md](release-process.md) — release process and tagging
- [README.md](../README.md) — project overview and limitations
