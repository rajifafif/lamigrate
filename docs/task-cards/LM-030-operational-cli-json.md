# LM-030 — Deliver operational CLI, confirmation, JSON, and secret-safe UX

- Status: BLOCKED
- Suggested owner: CLI/operator-experience engineer
- Depends on: LM-012, LM-013, LM-023, LM-024, LM-025, LM-026, LM-027
- Architecture: §§6.1, 14–16

## Goal

Wire accepted library operations into the production CLI contract with configuration, confirmations, dry-run output, stable exit codes, versioned JSON, and secret-safe diagnostics.

## Requirements

- Implement all target commands in §14 and their approved options.
- Require `--yes`/interactive confirmation where required; confirmation must occur before database mutation.
- Render human and JSON output from structured results only.
- Map typed error categories to exit codes in §14.
- Ensure status/preview can report metadata/prototype/dirty conditions without mutating state.
- Redact credentials from every stdout/stderr/JSON/error path.

## Acceptance criteria

- CLI contains no migration state-machine logic.
- JSON schema is documented, versioned, and tested for backward-compatible fields.
- Dry-run semantics match LM-023 exactly.
- Signals return the documented cancellation/error category without falsely reporting success.

## Verification

Compiled subprocess tests, JSON contract tests, secret redaction tests, confirmation/no-confirmation tests, signal tests, and integration scenarios across command families.
