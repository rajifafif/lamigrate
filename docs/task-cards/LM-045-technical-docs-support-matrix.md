# LM-045 — Publish final technical documentation and support matrix

- Status: DONE
- Suggested owner: documentation/technical writer
- Depends on: LM-003, LM-010, LM-013, LM-030, LM-041, LM-044
- Architecture: §§14–19, §22

## Goal

Document accepted, tested behavior only: CLI/API/configuration contracts, support matrix, operations, recovery, and release limitations.

## Requirements

- Reconcile README/API examples with accepted LM-010/013/030 implementation and canonical module path.
- Document configuration/credentials/TLS policy from LM-003/013 without real secrets.
- Document MySQL versions, privileges, advisory-lock assumptions, implicit-DDL limitations, backup requirements, cancellation/connection-loss behavior, repair/import/adoption procedures, and experimental/release status.
- Link actual CI evidence from LM-044 and governance docs from LM-041.

## Acceptance criteria

- Every technical claim maps to an accepted implementation/test/evidence source.
- No target-only contract is described as currently shipped unless it is implemented and verified.
- Fresh installation, configuration, migration creation, safe operation, and recovery guidance are executable by a contributor.

## Verification

Fresh-contributor doc run-through, source/example compilation checks, secret scan, link/fence checks, and independent technical-accuracy review.
