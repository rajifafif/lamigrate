# LM-005 — Characterize prototype database behavior in isolated MySQL

- Status: BLOCKED
- Suggested owner: QA / database characterization engineer
- Depends on: LM-001, LM-002
- Architecture: §4, §§9–13, §18.2, Phase 1

## Goal

Use the isolated harness to record the current prototype’s actual database behavior and known unsafe states before the production runtime rewrite.

## Scope

- Constructor side effects, default/custom tracking-table timing, status/pretend effects, batch reuse, up/down/reset selections, multi-statement behavior, and legacy import behavior.
- Demonstrations of current race/crash/error windows only through disposable test databases/failpoints.
- Actual MySQL 8.0/8.4 compatibility evidence for the prototype, clearly labeled non-production.

## Acceptance criteria

- Every database case uses LM-002’s isolated-database guard.
- Expected unsafe results are asserted/documented as regression evidence, never accepted as target behavior.
- Output identifies which later cards own remediation.
- No real host, live password, or production/developer database appears in tests or logs.

## Verification

Run tagged integration characterization on both MySQL lines, including a test database identity assertion and cleanup confirmation.

## Do not

- Do not redesign metadata, locking, or execution behavior.
- Do not change production code except approved test seams.
