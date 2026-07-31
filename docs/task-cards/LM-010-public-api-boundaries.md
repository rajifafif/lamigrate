# LM-010 — Establish side-effect-free public API and typed boundaries

- Status: BLOCKED
- Suggested owner: Go API engineer
- Depends on: LM-000, LM-001, LM-005
- Architecture: §§6–7, §16, Phase 2

## Goal

Replace prototype construction/orchestration boundaries with the approved side-effect-free API direction: validated options, `mysql.Config` ownership, structured results, typed errors, and validated `StepLimit`.

## Requirements

- Implement or finalize `Options`, `StepLimit`, `NewMySQL`, `OpenMySQL`, result/report/error categories, and preview method contracts from §7.
- Constructors validate locally but do not connect, ping, create metadata, or emit stdout/stderr.
- Clone caller-supplied MySQL config; do not accept shared `*sql.DB` ownership in the production API.
- Preserve a documented migration path for the existing public API or introduce a deliberate breaking pre-1.0 API change.
- Move user-facing rendering into the CLI.

## Acceptance criteria

- Zero/uninitialized `StepLimit` fails before connector/filesystem/database use.
- No library path writes directly to stdout/stderr.
- Errors permit category checks without parsing strings.
- Public API documentation and examples compile in an external consumer module.

## Verification

Unit tests, external-consumer compile/run probe, race tests, API review, and integration proof that construction makes no database mutation.

## Do not

- Do not implement locking, metadata DDL, or command rendering here.
