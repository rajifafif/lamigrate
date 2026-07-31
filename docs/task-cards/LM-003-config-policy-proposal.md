# LM-003 — Propose configuration-source and credential policy

- Status: DONE
- Suggested owner: maintainer + security/CLI designer
- Depends on: LM-004
- Architecture: §§5.8, 6.1, 14–16
- Trigger: maintainer requested `.env` / `config.yaml` support with `dbMySQL` mapping.

## Goal

Produce a decision-ready, non-authoritative configuration policy proposal. This card does not implement a loader, alter `go.mod`, or approve the architecture.

## Proposal must specify

- Precedence among explicit `-dsn`, `LAMIGRATE_DSN`, explicit config path, default `config.yaml` / `config.yml`, and `.env`.
- Exact YAML mapping and strict-field policy:

```yaml
dbMySQL:
  host: example.invalid
  timeout: 60m
  port: 3306
  user: migration_user
  pass: ${SECRET_NOT_COMMITTED}
  dbName: application_dev
```

- Exact `.env` keys and whether `.env` may contain `LAMIGRATE_DSN`.
- Project-root/current-directory/config-path discovery rule.
- Regular-file/symlink/size/permission policy and Git ignore/example-file policy.
- Password/DSN redaction requirements, TLS policy, remote-host warning, timeout mapping, and required MySQL DSN parameters.
- Explicit rule that offline commands never read configuration or connect.

## Acceptance criteria

- The proposal is limited to policy and concrete testable contract language.
- It contains no live credential or remote host.
- It names every architecture section LM-000 must approve, the CLI/configuration implementation scope LM-013 must implement, and the final public-documentation scope LM-045 must publish. It does not assign public API documentation changes to LM-013.
- It identifies whether any current partial loader/dependency code must be reverted or adopted by LM-013; LM-004 remains authoritative for its worktree disposition.

## Verification

Independent security/CLI review for ambiguity, secret leakage, precedence conflicts, and compatibility with offline commands.

## Do not

- Do not add a parser/dependency or modify runtime code.
- Do not mark configuration support implemented.
