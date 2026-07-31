# LM-024 — Implement dirty-state execution and batch semantics

- Status: DONE
- Suggested owner: migration execution engineer
- Depends on: LM-021, LM-022, LM-023
- Architecture: §§5.1–5.3, 9.1–9.2, 11.2–11.3

## Goal

Implement safe up/down/reset execution with intent rows, explicit metadata transactions, monotonic batches, session restoration, and fail-closed uncertain outcomes.

## Requirements

- Allocate batches from locked control metadata and never reuse them.
- Commit/re-read intent/final transitions exactly as §11 requires.
- Execute immutable exact SQL bytes only after acknowledged intent.
- Detect SQL errors, changed session state, and uncertain commits; restore/verify session only when lock ownership is proven.
- Leave dirty state rather than guessing about implicit DDL outcomes.
- Preflight entire rollback set and delete state rows only on clean acknowledged rollback completion.

## Acceptance criteria

- Invalid counts cannot broaden scope.
- No subsequent migration runs after an execution failure or uncertain metadata commit.
- `down`, `reset`, baseline exclusion, reverse ordering, and non-reused batches conform to §9.2.
- Every crash window yields a documented clean, dirty, or outcome-unknown state.

## Verification

Failpoint/crash-window integration tests, multi-statement partial failure, simultaneous runners, cancellation, batch monotonicity after rollback/failure, and independent review.
