# LM-000 — Approve target contracts and maintainer decisions

- Status: BLOCKED
- Suggested owner: maintainer / architecture owner
- Depends on: LM-001, LM-003
- Architecture: §§1–5, 7, 8.3, 15, 17, 21

## Goal

Turn the review-candidate architecture into approved maintainer contracts before workers make production-safety changes.

## Required decisions

1. Confirm `github.com/rajifafif/lamigrate` as permanent module path.
2. Approve/revise target public API direction in §7.
3. Decide irreversible-migration marker syntax and operator policy.
4. Decide imported-baseline rollback policy.
5. Confirm supported MySQL lines and exact pinned-image policy.
6. Confirm lock protocol v1 restriction: `lower_case_table_names=0` and ASCII database-name domain.
7. Approve, revise, or reject the LM-003 configuration policy proposal, including inline-DSN support.
8. Decide JSON output schema/versioning policy.
9. Confirm MariaDB is out of scope for the first production release.

## Acceptance criteria

- Every decision has an explicit maintainer outcome: approved or revised into a concrete selected contract.
- `architecture.md` is updated only for approved revisions and carries approval status/date.
- A deferred required decision keeps LM-000 `BLOCKED`; it cannot be marked `DONE` or unblock any implementation card. A new scoped decision card must be created for the unresolved choice.
- LM-003 policy status is reflected accurately.
- No runtime code is changed.

## Verification

Independent architecture review checks the decisions against §§5, 7–15, 17, and 21. Coordinator runs the board-consistency check and updates statuses after approval.

## Do not

- Do not call the existing prototype production ready.
- Do not implement the architecture here.
