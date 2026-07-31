# LM-044 — Activate supported CI matrix and collect release evidence

- Status: DONE
- Suggested owner: CI/security engineer
- Depends on: LM-024, LM-025, LM-026, LM-027, LM-030, LM-040
- Architecture: §§17–19, §18.3, §22

## Goal

Turn CI scaffolding into the full release-evidence matrix after all operational runtime paths are implemented.

## Requirements

- Require every §18.3 gate on the pinned Go/MySQL matrix.
- Run integration behavior on both supported MySQL lines: runtime, lock, metadata, execution, import, adoption, repair, CLI, and failure-injection coverage.
- Record SQL modes, timezones, TLS, privileges, character sets, and case-policy inputs with secret-safe artifacts.
- Require review/CI branch protection where hosting permits.

## Acceptance criteria

- No required matrix job is informational, skipped, or failing at certification time.
- Evidence links map each §22 runtime/verification condition to an exact CI job or documented manual test.
- Vulnerability findings are evaluated under patched toolchain and reachable findings block release.

## Verification

Full CI matrix rerun from a clean release-candidate branch/tag, reviewed independently by release/security owner.
