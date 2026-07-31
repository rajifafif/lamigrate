# Migration File Format

Complete specification of the lamigrate migration file contract.

> **Status: experimental, pre-1.0.** The migration file contract is defined
> in [architecture.md](../architecture.md) section 8 and implemented in
> LM-011. Some production-target features (checksum drift detection,
> irreversible markers) are partially implemented.

## Filename Format

```text
YYYYMMDDHHMMSS_description.up.sql
YYYYMMDDHHMMSS_description.down.sql
```

Each migration consists of a **pair** of files: one `.up.sql` (forward) and one `.down.sql` (rollback).

### Components

| Component | Format | Example |
|-----------|--------|---------|
| Timestamp | 14-digit UTC: `YYYYMMDDHHMMSS` | `20260730094235` |
| Separator | Underscore `_` | `_` |
| Description | Lowercase ASCII snake_case | `create_users` |
| Extension | `.up.sql` or `.down.sql` | `.up.sql` |

### Canonical Migration ID

The canonical migration ID is formed by joining the timestamp and description:

```text
YYYYMMDDHHMMSS_description
```

Example: `20260730094235_create_users`

This ID is used as the primary key in the tracking table and for all metadata operations.

## Naming Rules

### Description Validation

Migration descriptions must match this regular expression:

```regex
^[a-z][a-z0-9_]*$
```

Rules:

- Must start with a lowercase letter `[a-z]`
- May contain lowercase letters, digits, and underscores
- No uppercase letters, hyphens, spaces, or special characters
- Maximum 200 characters

### Name Normalization

When creating migrations via `migration create`, names are normalized:

1. Trimmed and lowercased
2. `.sql`, `.up`, `.down` suffixes stripped
3. Hyphens and spaces replaced with underscores
4. Double underscores collapsed to single underscores

```bash
# Input                        # Normalized
Create-Users-Table             # create_users_table
"Create Users Table"           # create_users_table
create_users.up.sql            # create_users
create_users.down.sql          # create_users
```

### Timestamp Uniqueness

Each timestamp must be unique within the migrations directory. Creating a migration with a timestamp that already has files in the directory fails with:

```
lamigrate: timestamp 20260730094235 already has a migration; retry in one second
```

### Sorting

Migrations are sorted by timestamp first, then by canonical ID as a tie-breaker. This is deterministic and does not depend on filesystem enumeration order.

## Pair Requirements

Every `.up.sql` file must have a matching `.down.sql` file with the same base name. The discovery process:

1. Scans the directory for `*.up.sql` files matching the timestamp pattern.
2. For each `.up.sql`, checks that the corresponding `.down.sql` exists.
3. Rejects `.up.sql` files without a paired `.down.sql`.
4. Rejects orphaned `.down.sql` files that have no matching `.up.sql`.

## File Type Requirements

- Files must be **regular files** (not directories, symlinks, devices, or named pipes).
- Symlinks are explicitly rejected, even if they point to regular files.
- The only exception on macOS: `/var` and `/tmp` symlinks (system-managed compatibility aliases) are trusted.

## File Size Limits

Each migration file is subject to a maximum size limit:

- **Default:** 1 MB (1,048,576 bytes)
- **Configurable:** via `Options.MaxFileSize` in the library API
- **Checked:** before reading file contents

Files exceeding the limit are rejected during discovery/preflight.

## Checksums

### Algorithm

- **SHA-256** over the exact file bytes (not normalized or trimmed)
- Both `.up.sql` and `.down.sql` files are checksummed independently
- Checksums are stored in the tracking table as `BINARY(32)`

### Drift Detection

After a migration is applied, lamigrate verifies that the current file bytes match the stored checksum. If the file has been modified since application, this is **checksum drift**.

Drift is reported in `status` output and **blocks all write operations** (up, down, reset, import) until the drift is resolved.

**Resolution:** Restore the exact applied source bytes. The operator must verify and correct the file; there is no metadata command to acknowledge drift. This prevents unauthorized migration edits from being silently accepted.

### Checksum Behavior During Import

During legacy import (batch 0 baseline records), exact up/down checksums are computed from the files in the legacy directory. These checksums are used for drift detection on baseline records.

## Template Generation

When creating a migration with `migration create`, lamigrate generates SQL templates based on the name pattern.

### create_table Template

Pattern: `create_<table>_table`

```sql
-- Migration: create_<table>_table

CREATE TABLE `<table>` (
    `id` BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    `created_at` TIMESTAMP NULL DEFAULT NULL,
    `updated_at` TIMESTAMP NULL DEFAULT NULL,
    PRIMARY KEY (`id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
```

```sql
-- Rollback: create_<table>_table

DROP TABLE IF EXISTS `<table>`;
```

### add_column Template

Pattern: `add_<column>_to_<table>_table`

```sql
-- Migration: add_<column>_to_<table>_table
--
-- TODO: Review the suggested SQL, choose exact types/options, then remove SIGNAL.
SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'lamigrate: unfinished up migration add_<column>_to_<table>_table';

-- Suggested SQL:
-- ALTER TABLE `<table>` ADD COLUMN `<column>` VARCHAR(255) NULL;
```

```sql
-- Rollback: add_<column>_to_<table>_table
--
-- TODO: Review the suggested SQL, choose exact types/options, then remove SIGNAL.
SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'lamigrate: unfinished down migration add_<column>_to_<table>_table';

-- Suggested SQL:
-- ALTER TABLE `<table>` DROP COLUMN `<column>`;
```

### drop_column Template

Pattern: `drop_<column>_from_<table>_table`

```sql
-- Migration: drop_<column>_from_<table>_table
--
-- TODO: Review the suggested SQL, choose exact types/options, then remove SIGNAL.
SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'lamigrate: unfinished up migration drop_<column>_from_<table>_table';

-- Suggested SQL:
-- ALTER TABLE `<table>` DROP COLUMN `<column>`;
```

```sql
-- Rollback: drop_<column>_from_<table>_table
--
-- TODO: Review the suggested SQL, choose exact types/options, then remove SIGNAL.
SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'lamigrate: unfinished down migration drop_<column>_from_<table>_table';

-- Suggested SQL:
-- ALTER TABLE `<table>` ADD COLUMN `<column>` VARCHAR(255) NULL;
```

### Generic Template

Any other name:

```sql
-- Migration: <name>
--
-- TODO: Review the suggested SQL, choose exact types/options, then remove SIGNAL.
SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'lamigrate: unfinished up migration <name>';

-- Suggested SQL:
-- Write the forward SQL for this migration.
```

```sql
-- Rollback: <name>
--
-- TODO: Review the suggested SQL, choose exact types/options, then remove SIGNAL.
SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'lamigrate: unfinished down migration <name>';

-- Suggested SQL:
-- Write the rollback SQL for this migration.
```

### SIGNAL Guard

Templates (except `create_table`) include a `SIGNAL SQLSTATE '45000'` statement. This is an active SQL statement that causes MySQL to return an error when executed. The guard must be **removed** after reviewing and finishing the migration SQL.

This prevents unfinished migrations from being silently recorded as applied, which would leave the database in an inconsistent state.

## Irreversible Migrations

> **Note:** The irreversible migration marker is part of the target
> architecture and is not yet fully implemented. The following describes the
> planned behavior.

An intentionally irreversible migration must be explicit, not represented by an accidentally missing down file.

### Planned Representation

```sql
-- lamigrate: irreversible
-- reason: data transformation cannot be safely reversed
```

When a down or reset plan encounters this marker in a `.down.sql` file, it fails during preflight **before** executing any rollback SQL. The migration is never silently skipped.

After an operator performs and verifies a manual compensating action, the explicit repair workflow may remove the clean applied record. Without that evidence, the irreversible migration remains a hard stop.

## SQL Execution Contract

Migration files contain trusted SQL source code. lamigrate executes them with these constraints:

- **Multi-statement:** Files may contain multiple MySQL statements separated by semicolons.
- **No splitting:** lamigrate does not split SQL on semicolons. The driver handles multi-statement execution.
- **No `DELIMITER`:** Client-only directives (`DELIMITER`, `SOURCE`, etc.) are not SQL and are not supported.
- **Not atomic:** MySQL DDL may implicitly commit transactions. A migration file is not assumed to be atomic.
- **Partial failure:** A multi-statement error may occur after earlier statements succeeded. The metadata state becomes dirty and requires reconciliation.
- **Exact bytes:** SQL is executed from the exact bytes that were checksummed during preflight. No re-reading of files occurs after the plan is created.

## Migration Discovery Process

1. Scan the configured directory for `*.up.sql` files matching `YYYYMMDDHHMMSS_description.up.sql`.
2. For each match, verify the corresponding `.down.sql` exists and is a regular file.
3. Reject symlinks, non-regular files, duplicate IDs, and duplicate timestamps.
4. Compute SHA-256 checksums for both files in each pair.
5. Sort by timestamp, then by canonical ID as tie-breaker.

## File Creation Process

1. Normalize the migration name.
2. Ensure the migrations directory exists (create if needed).
3. Generate a UTC timestamp.
4. Check timestamp availability (no existing files with this timestamp).
5. Acquire an exclusive creation lock (`.lamigrate-create-<timestamp>.lock`).
6. Re-check timestamp availability under the lock.
7. Generate SQL from the name pattern template.
8. Stage both files as temporary files.
9. Publish the `.down.sql` file first.
10. Publish the `.up.sql` file second.
11. Sync the directory after each publish.

**Crash safety:** The down file is published before the up file. A crash during creation can leave at most a harmless down-only orphan, never a runnable up-only migration. Orphaned down files are ignored by discovery.

## Legacy Migration Format

For import from golang-migrate, the legacy format uses 6-digit numbered versions:

```text
000001_description.up.sql
000001_description.down.sql
```

| Component | Format |
|-----------|--------|
| Version | Positive unsigned integer, zero-padded to 6 digits (other widths accepted) |
| Description | Lowercase ASCII snake_case |
| Extension | `.up.sql` or `.down.sql` |

Legacy files are not mixed into normal timestamp discovery. They are only scanned by the `import` command.

## Task Card Reference

The migration source contract and checksums are implemented. The architecture specification is in [architecture.md](../architecture.md) section 8.
