# LM-025 — Implement reconciled golang-migrate baseline import

- Status: DONE
- Suggested owner: import/migration-history engineer
- Depends on: LM-011, LM-021, LM-022, LM-023
- Architecture: §13, §9 baseline rules, §18.2

## Goal

Replace unsafe per-file import with explicit, reconciled, idempotent golang-migrate baseline import.

## Requirements

- Keep legacy source separate via `LegacyDir`; do not mix legacy files into normal timestamp discovery.
- Require a validated source metadata table and `SourceQuiesced` for mutation.
- Re-read source `version, dirty` immediately before target mutation.
- Accept any positive uint64 version, canonicalize leading zeroes, permit sparse versions, and require valid paired source files/checksums.
- Insert complete baseline set atomically into empty target or return exact-set idempotent no-op; reject partial/conflicting extension.
- Handle unacknowledged baseline commit through later separately locked replan, not compensating writes.

## Acceptance criteria

- Dirty/changed source, above-version unresolved files, source/destination collision, checksum conflict, concurrent import, lock loss, or lock contention blocks mutation safely.
- Baselines use batch 0 and never participate in normal down/reset.
- Status resolves imported rows only through `LegacyDir` and detects drift.

## Verification

Real MySQL fixtures for empty/forced/dirty/changed source, maximum/sparse/leading-zero versions, retry/idempotency/conflict, concurrent import, lock contention/loss, and lost commit acknowledgement.
