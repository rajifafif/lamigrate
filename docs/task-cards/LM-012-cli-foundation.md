# LM-012 — Establish strict CLI foundation and offline command boundary

- Status: BLOCKED
- Suggested owner: Go CLI engineer
- Depends on: LM-000, LM-001
- Architecture: §§6.1, 14, 16, Phase 2

## Goal

Replace prototype command parsing with one strict, testable command grammar, signal-aware context, stable exit categories, and an offline command boundary. Configuration implementation belongs exclusively to LM-013.

## Requirements

- Define a supported CLI parser with one documented option location/syntax.
- Implement signal-aware context creation, stdout/stderr separation, help, version, and stable exit categories.
- Keep `migration create`, `make`, `make:migration`, help, and version fully offline; they must not call any configuration resolver.
- Reject unknown/missing/repeated/misplaced flags and malformed limits before file/database work.
- Define interfaces/inputs that LM-013 can use for approved database configuration without embedding a loader here.

## Acceptance criteria

- Every target command grammar has parser-level acceptance tests.
- Offline commands run without configuration reads or MySQL connections.
- Database command orchestration delegates to library APIs; no migration state machine lives in `cmd/`.
- Diagnostics are secret-safe and error categories map to documented exit codes.

## Verification

Compiled subprocess tests for command parsing/offline behavior, signal tests, output-channel tests, and explicit instrumentation proving offline commands do not invoke configuration resolution.
