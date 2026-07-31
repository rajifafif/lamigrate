# LM-001 — Characterize offline, library, and CLI prototype behavior

- Status: DONE
- Suggested owner: QA / test engineer
- Depends on: LM-004
- Architecture: §4, §18.1, Phase 1 (§20)

## Goal

Freeze repeatable non-database characterization of the prototype before refactoring. Database behavior belongs to LM-005 and must use LM-002.

## Scope

- Offline migration creation, discovery/order, pair validation, and filesystem behavior.
- CLI parsing, command/flag placement, help, exit behavior, pretend parsing, and no-DSN offline behavior.
- Library constructor/API/output behavior that can be observed without a database.
- Regression cases tied to current architecture §4 gaps that do not require a database.

## Acceptance criteria

- Tests distinguish existing behavior from desired production behavior; known unsafe cases are named as characterization evidence, not desired behavior.
- Every test runs with no MySQL connection and no developer/production data.
- Findings are recorded as prerequisites or regression references for LM-010 through LM-013.
- The current reconciled baseline from LM-004 is the exact code under test.

## Verification

```bash
go test -count=1 ./...
go test -race -count=1 ./...
```

## Do not

- Do not test up/down/reset/import against any database.
- Do not refactor production behavior except approved minimal test seams.
