# LM-021 — Implement canonical advisory-lock protocol v1

- Status: DONE
- Suggested owner: MySQL concurrency engineer
- Depends on: LM-020
- Architecture: §§5.1, 9, 10.1–10.2, 18.2

## Goal

Implement the permanent v1 advisory-lock algorithm and lifecycle around every database-dependent command.

## Requirements

- Validate the ASCII database and lowercase tracking-table domains before lock derivation.
- Use the exact SHA-256 byte framing and 192-bit truncated key in §10.1; publish fixed test vectors.
- Acquire/verify/release MySQL locks on the same dedicated connection; treat timeout/NULL/unreceived results as specified.
- Verify `IS_USED_LOCK` ownership before/after file execution and use a separate cleanup context for release.
- Implement the two-phase bootstrap lock protocol required when `lamigrate_control` is absent.

## Acceptance criteria

- No normal migration operation can run without confirmed scope-lock ownership.
- Uncertain lock acquisition/release/session termination produces a typed cleanup/lock error and blocks further work.
- Bootstrap lock cannot collide with valid public scope keys.
- All connections are disposed after every outcome.

## Verification

Two-process contention, fixed vectors, connection-ID continuity, cancellation/loss tests, implicit-DDL lock-survival tests, and both MySQL versions.
