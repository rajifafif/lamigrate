# Changelog

All notable changes to lamigrate will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/),
and this project adheres to [Semantic Versioning](https://semver.org/).

## [v0.2.0-experimental] - 2026-08-03

### Added
- `repair` CLI command with `show`, `mark-applied`, `mark-rolled-back`,
  `remove-failed`, and `forget` operations for dirty-state and missing-source
  recovery.
- `repair forget` operation: forget a clean `applied` orphan whose source file
  no longer exists (shown as `MISSING_SOURCE`), unblocking `up`/`down`/`reset`.
- `--ignore-missing-source` global flag (`Options.IgnoreMissingSource`): in a
  shared/remote-database workflow, skip the missing-source check so orphaned
  branch migrations no longer block `up`/`down`/`reset`. Does NOT delete the
  metadata row; all other safety checks (dirty state, checksum drift on present
  sources) remain enforced.
- `repair show` renders operator inspection instructions and expected checksums.

### Changed
- CLI `--yes`/`-y` is accepted as a command-level flag after the migration name
  in addition to the global (before-command) position.

### Fixed
- Exposed the previously library-only `repair` workflow through the CLI
  (was specified in architecture.md but not wired to the executable).
- Missing-source orphans no longer hard-block migrations for shared-DB teams
  when `--ignore-missing-source` is used.

### Known limitations (experimental)
See `docs/release-certification.md` for tested areas and known gaps.
