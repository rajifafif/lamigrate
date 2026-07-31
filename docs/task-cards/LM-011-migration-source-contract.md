# LM-011 — Implement canonical migration-source and file-creation contract

- Status: BLOCKED
- Suggested owner: Go filesystem/source engineer
- Depends on: LM-000, LM-001, LM-002
- Architecture: §§8, 11.1, 18.1–18.2, Phase 2

## Goal

Make source discovery and creation conform to the target file contract: canonical UTC IDs, validated pairs, bounded reads, deterministic ordering, SHA-256 checksums, irreversible markers, and crash-safe creation.

## Requirements

- Validate actual UTC timestamps, lowercase snake-case descriptions, portable lengths, unique timestamps/IDs, regular files, and no symlinks.
- Reject down-only and up-only files in normal source discovery as required by §8.1; creation crash recovery must have a documented operator path.
- Read bounded exact bytes and calculate immutable SHA-256 checksums.
- Implement explicit irreversible marker parsing and preflight behavior after LM-000 chooses syntax.
- Keep creation offline, exclusive, collision-safe, and no-truncate; use a publish protocol that never exposes executable up without durable down.
- Coordinate generated create-table template integration tests with LM-002; this card owns adapting/expanding the test when source/template behavior changes.

## Acceptance criteria

- Source ordering never depends on map/filesystem order.
- Every accepted pair has canonical identity and exact byte checksums.
- Generated runnable templates execute up/down successfully on both pinned MySQL lines; guarded templates retain their active failure guard.
- All source errors are diagnostic but never disclose database configuration.

## Verification

Unit/race tests, LM-002 integration execution of generated templates on both lines, and a reviewer exercise of kill/failure scenarios using test seams.
