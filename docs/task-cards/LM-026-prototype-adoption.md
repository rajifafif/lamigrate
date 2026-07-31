# LM-026 — Implement explicit prototype metadata adoption and recovery

- Status: BLOCKED
- Suggested owner: metadata-migration engineer
- Depends on: LM-011, LM-021, LM-022, LM-023
- Architecture: §9.3, §18.2, Phase 3

## Goal

Provide the only supported upgrade path from known prototype metadata to v1 without ordinary-command auto-upgrade.

## Requirements

- Detect exact known prototype shape; block ordinary write operations with adoption-required.
- Require a non-empty valid prototype, selected non-existing backup name, exact source evidence, and preview before mutation.
- Preserve historical IDs, batch numbers, `applied_at`, and execution order while calculating source identities/checksums.
- Perform one atomic rename swap; retain backup table.
- Recognize/recover only the specified rename-complete/control-row-missing interruption using same backup/source evidence.

## Acceptance criteria

- Empty prototype and `MAX(id)` exhaustion fail before temporary-table DDL.
- Unknown variants, source ambiguity, changed checksums, different backup request, concurrent adoption, lock loss/contention, and unexplained temporary tables fail closed.
- Successful adoption preserves exact row/order/checksum identity and correct `next_batch`.

## Verification

Integration fixtures for default/custom tables, batch-0 rows, interrupted states, retry cases, limited down after adoption, simultaneous adoption attempts, lock contention/loss, and no-mutation refusals.
