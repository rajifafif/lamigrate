# LM-040 — Add non-certifying CI and security scaffolding

- Status: BLOCKED
- Suggested owner: CI/security engineer
- Depends on: LM-000, LM-002
- Architecture: §§17–19, §18.3, Phase 5

## Goal

Create CI/security scaffolding for the approved toolchain and harness without claiming the full production matrix is complete. Final activation/evidence belongs to LM-044.

## Requirements

- Pin patched Go and approved MySQL image versions in CI configuration.
- Add format, tidy-diff, verify, unit, race, vet, staticcheck, govulncheck, integration, and cross-build workflow stages, allowing not-yet-implemented later behavior to remain explicitly pending/disabled only where necessary.
- Add isolated integration safety guards and dependency update automation.
- Establish CI evidence/log retention policy with no secrets.

## Acceptance criteria

- Baseline CI gates applicable to current code run automatically.
- The workflow clearly identifies which final matrix jobs are scaffolding versus release-certifying.
- No local-only or scaffolded result is presented as §22 production evidence.

## Verification

Run CI on a branch/PR and document available job outputs; LM-044 will later require all final matrix jobs passing.
