# LM-020 — Implement private MySQL session lifecycle and capability probes

- Status: BLOCKED
- Suggested owner: MySQL runtime engineer
- Depends on: LM-010, LM-002
- Architecture: §§7, 10, 17–18, Phase 3

## Goal

Implement the audited private one-session MySQL runtime required before lock or metadata work.

## Requirements

- Create a fresh connector/private pool per phase from a cloned supported `mysql.Config`.
- Set one open/idle connection, obtain one dedicated `*sql.Conn`, and physically close connection/pool in every outcome.
- Implement all §7/§10 capability probes before mutation: multi-statement/result draining, time scan, server version, selected DB, character sets, case policy, autocommit, transaction state, and connection ID.
- Return typed unsupported-configuration errors before metadata/migration SQL.
- Use caller context for work and bounded fresh cleanup context only for safety finalization.

## Acceptance criteria

- No connection is borrowed from or returned to an application pool.
- Probe failures make no metadata changes.
- Tests demonstrate physical-session disposal and driver/session state isolation on both MySQL lines.
- TLS and timeout behavior match the approved configuration policy.

## Verification

LM-002 integration matrix, connection-ID/session-state assertions, cancellation tests, race tests, and independent runtime review.
