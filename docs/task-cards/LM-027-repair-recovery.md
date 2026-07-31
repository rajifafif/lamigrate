# LM-027 — Implement explicit dirty-state repair workflow

- Status: BLOCKED
- Suggested owner: operations/recovery engineer
- Depends on: LM-024
- Architecture: §12, §§9–11, §14

## Goal

Implement conservative inspection and repair commands for dirty migrations and explicit irreversible-manual-compensation cases.

## Requirements

- Implement preview/show and legal `mark-applied`, `mark-rolled-back`, and `remove-failed` requests through library APIs.
- Require scope lock, named target, operator confirmation, and free-text reason for every mutation.
- Never run migration SQL automatically during repair.
- Show current metadata/checksums and document required database inspection before action.
- Preserve the rule that checksum drift is repaired only by restoring exact source bytes.

## Acceptance criteria

- Illegal transitions fail closed.
- Dirty/recovery-required/outcome-unknown states are distinguishable in status and CLI exit categories.
- Every repair result exposes structured audit fields without secrets.
- Clean irreversible row removal requires explicit verified manual compensation policy from LM-000.

## Verification

Integration tests for every legal/illegal transition, confirmation rejection, lock contention, drift refusal, and ambiguous-commit follow-up behavior.
