# LM-041 — Add open-source governance and experimental-status scaffolding

- Status: DONE
- Suggested owner: documentation/open-source maintainer
- Depends on: LM-000
- Architecture: §§17, 19, 22, Phase 5

## Goal

Add governance skeleton and accurate experimental-status framing only. Final technical support/API/configuration documentation belongs to LM-045.

## Requirements

- Add `CONTRIBUTING.md`, `SECURITY.md`, code of conduct, issue templates, pull-request template, changelog skeleton, and support/release policy skeleton.
- Add a documentation map linking README, architecture, task board, and task-card protocol.
- Mark the current project experimental and forbid unsupported production-safety claims.
- Provide no final CLI/API/configuration examples that may conflict with LM-003/010/013/030.

## Acceptance criteria

- Security reporting and supported-version policy are explicit.
- No file contains real DSNs/passwords.
- Governance docs do not assert features/evidence that remain target work.

## Verification

Secret scan, Markdown/link review, and independent governance review.
