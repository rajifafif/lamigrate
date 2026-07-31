# LM-013 — Implement approved YAML/.env/DSN configuration sources

- Status: DONE
- Suggested owner: CLI/security engineer
- Depends on: LM-000, LM-003, LM-010, LM-012
- Architecture: §§5.8, 6.1, 14–16

## Goal

Implement only the configuration behavior approved by LM-000 from the LM-003 policy: safe YAML/.env discovery/parsing, approved precedence, DSN construction, timeout/TLS options, redaction, and ignored example files.

## Requirements

- Reconcile the existing partial config-loader/dependency work only after LM-004 records its disposition.
- Implement exact `dbMySQL` YAML and approved `.env` mapping; reject unknown/malformed fields according to policy.
- Enforce file regularity, symlink/size/permission policy, safe discovery root, and supported config extensions.
- Parse/build DSNs through the audited MySQL driver configuration rather than unsafe interpolation.
- Ensure `parseTime`, multi-statement, timeout, TLS, and credential redaction policy matches approved contract.
- Add only approved dependencies and ensure lockfile/module metadata is deliberate.
- Add `.gitignore` rules and placeholder-only `.env.example` / config example if approved. Final README/API/support documentation is owned by LM-045; this card may update only the CLI help and narrowly necessary implementation comments.

## Acceptance criteria

- Precedence is deterministic and documented: explicit DSN, environment DSN, explicit config, defaults only if approved.
- No raw password/DSN appears in errors, JSON, tests, examples, or logs.
- Offline commands never read config or connect.
- Malformed/missing/non-regular/oversized/insecure files fail closed per policy.

## Verification

Unit tests for YAML/.env/precedence/error/redaction cases; compiled CLI tests for config and offline isolation; external secret scan; full Go gates and independent security review.
