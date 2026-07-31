# LM-022 — Implement versioned metadata v1 and safe bootstrap

- Status: DONE
- Suggested owner: MySQL metadata engineer
- Depends on: LM-021, LM-002
- Architecture: §9, §§10–11, Phase 3

## Goal

Implement `lamigrate_control`, configurable state-table initialization, exact semantic validation, and prototype classification without unsafe automatic upgrades.

## Requirements

- Enforce validated lowercase table identifiers and fully qualified metadata SQL.
- Create/validate the schemas and control row described in §9, including schema version and durable next-batch value.
- Inventory existing objects before DDL; reject incompatible/prototype/partial shapes before creating another object.
- Validate semantic schema shape and row cross-field invariants via `information_schema`, not fragile `SHOW CREATE` string equality.
- Keep status/preview side-effect free; only write operations bootstrap metadata while locked.

## Acceptance criteria

- Custom state tables are selected before any metadata DDL.
- Unknown versions, extra unsafe objects, altered required fields, and unexpected state rows fail closed.
- Empty scope bootstrap is restartable and safe across custom scopes.
- Prototype tables are classified as adoption-required, never silently migrated.

## Verification

Pinned MySQL matrix with object-shape fixtures, first-write races, custom-name cases, bootstrap interruption, and metadata semantic rereads.
