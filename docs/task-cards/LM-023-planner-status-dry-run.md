# LM-023 — Implement immutable planner, global integrity status, and dry-run parity

- Status: BLOCKED
- Suggested owner: planner/status engineer
- Depends on: LM-010, LM-011, LM-022
- Architecture: §§5.4–5.6, 11.1, 11.4–11.5

## Goal

Build one planner used by status, previews, and execution preflight, with immutable selected SQL bytes and global checksum validation.

## Requirements

- Scan/validate complete source input under lock.
- Read every applied/baseline source pair and validate exact checksums even when not selected.
- Preflight all selected up/down actions before any SQL/metadata mutation.
- Produce internal immutable execution plans and defensive read-only views.
- Report every required status classification, including dirty, drift, missing source, unpaired, irreversible, and unsupported metadata.
- Make dry-run use the same planner but never create metadata, allocate a batch, execute SQL, or mutate rows.

## Acceptance criteria

- No execution rescans files after preview/preflight.
- Reset dry-run shows all selected rollback actions.
- Selected and unselected applied drift block writes.
- Output structures contain no raw DSN.

## Verification

Unit tests for ordering/status/plan immutability and integration tests for drift, missing files, preview/execution parity, and uninitialized scope behavior.
