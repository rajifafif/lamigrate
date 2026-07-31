# lamigrate multi-agent execution backlog

Status: derived from `architecture.md` (Review candidate)

This is the dependency-aware execution backlog for the target architecture. `architecture.md` remains authoritative: a task card cannot weaken one of its normative requirements or claim that target behavior already exists.

## Coordinator rules

1. Before assigning any code task, complete LM-004 and record the disposition of every pre-existing modified/untracked file.
2. Every implementation agent uses a dedicated branch/worktree created from an immutable reconciled baseline. The baseline is either a maintainer-approved local commit or a content-addressed reviewed patch applied verbatim to an individually owned clean branch. Do not use a copied dirty worktree as a worker baseline. Do not reset, stage, reformat, overwrite, or silently omit pre-existing work.
3. One agent owns one card. Workers update their card/PR evidence; only the coordinator changes queue status and dependencies.
4. Before every assignment, run the board-consistency check in LM-004: queue, graph, and card dependencies/statuses must match.
5. A task is DONE only after its acceptance criteria, required verification, documentation changes, and independent review pass.
6. Do not commit or push unless the maintainer explicitly authorizes it.

## Status vocabulary

- `DRAFT`: task details exist but a decision/contract is not approved.
- `READY`: dependencies are accepted and an isolated agent may start.
- `BLOCKED`: named task/gate prevents work.
- `IN_PROGRESS`: assigned to one owner.
- `REVIEW`: implementation/verification is ready for independent review.
- `DONE`: accepted with recorded evidence.

## Maintainer and reconciliation gates

| ID | Status | Owner | Depends on | Purpose |
|---|---|---|---|---|
| [LM-004](task-cards/LM-004-reconcile-current-worktree.md) | READY | Coordinator / maintainer | none | Inventory, preserve, and assign or discard every existing uncommitted change; establish the baseline for all worker worktrees. |
| [LM-003](task-cards/LM-003-config-policy-proposal.md) | BLOCKED | Maintainer + security/CLI designer | LM-004 | Produce a non-authoritative configuration policy proposal; no runtime implementation. |
| [LM-000](task-cards/LM-000-approve-target-contracts.md) | BLOCKED | Maintainer / architecture owner | LM-001, LM-003 | Approve the architecture and all production contract decisions, including the configuration proposal. |

## Queue

| ID | Status | Suggested lane | Depends on | Goal |
|---|---|---|---|---|
| [LM-001](task-cards/LM-001-characterize-prototype.md) | BLOCKED | QA / characterization | LM-004 | Characterize offline/library/CLI prototype behavior and known gaps. |
| [LM-002](task-cards/LM-002-mysql-integration-harness.md) | BLOCKED | QA infrastructure | LM-004, LM-000 | Create isolated, pinned MySQL 8.0/8.4 test harness from approved support policy. |
| [LM-005](task-cards/LM-005-database-characterization.md) | BLOCKED | QA / characterization | LM-001, LM-002 | Characterize current database behavior and unsafe failure modes only in isolated databases. |
| [LM-010](task-cards/LM-010-public-api-boundaries.md) | BLOCKED | API | LM-000, LM-001, LM-005 | Establish side-effect-free public API, typed errors/results, validated options. |
| [LM-011](task-cards/LM-011-migration-source-contract.md) | BLOCKED | Source / filesystem | LM-000, LM-001, LM-002 | Implement source validation/checksums/file creation and execute generated templates in MySQL. |
| [LM-012](task-cards/LM-012-cli-foundation.md) | BLOCKED | CLI | LM-000, LM-001 | Establish strict parser, signals, offline command boundary, exit codes; no config loader. |
| [LM-013](task-cards/LM-013-config-source-implementation.md) | BLOCKED | CLI/security | LM-000, LM-003, LM-010, LM-012 | Implement approved YAML/.env/DSN configuration policy and secret-safe config handling. |
| [LM-020](task-cards/LM-020-mysql-connection-capabilities.md) | BLOCKED | MySQL runtime | LM-010, LM-002 | Implement private session lifecycle and pre-mutation probes. |
| [LM-021](task-cards/LM-021-lock-protocol-v1.md) | BLOCKED | MySQL concurrency | LM-020 | Implement advisory lock v1 and bootstrap/scope cleanup behavior. |
| [LM-022](task-cards/LM-022-metadata-v1-bootstrap.md) | BLOCKED | Metadata | LM-021, LM-002 | Implement v1 control/state schema, semantic validation, and safe bootstrap. |
| [LM-023](task-cards/LM-023-planner-status-dry-run.md) | BLOCKED | Planner | LM-010, LM-011, LM-022 | Implement immutable plans, global drift status, dry-run parity. |
| [LM-024](task-cards/LM-024-execution-state-machine.md) | BLOCKED | Execution | LM-021, LM-022, LM-023 | Implement intent states, batch semantics, up/down/reset safety. |
| [LM-025](task-cards/LM-025-golang-migrate-import.md) | BLOCKED | Import | LM-011, LM-022, LM-023, LM-021 | Implement reconciled baseline import, including contention/loss safety. |
| [LM-026](task-cards/LM-026-prototype-adoption.md) | BLOCKED | Metadata migration | LM-011, LM-022, LM-023, LM-021 | Implement explicit prototype adoption and interruption recovery. |
| [LM-027](task-cards/LM-027-repair-recovery.md) | BLOCKED | Operations | LM-024 | Implement explicit dirty-state repair workflow. |
| [LM-030](task-cards/LM-030-operational-cli-json.md) | BLOCKED | CLI / UX | LM-012, LM-013, LM-023, LM-024, LM-025, LM-026, LM-027 | Wire accepted operations, confirmations, JSON, and secret-safe UX. |
| [LM-040](task-cards/LM-040-ci-security-scaffolding.md) | BLOCKED | CI/security | LM-000, LM-002 | Add non-certifying CI/security/release-check scaffolding. |
| [LM-041](task-cards/LM-041-oss-governance-scaffolding.md) | BLOCKED | Documentation/governance | LM-000 | Add governance skeleton and experimental-status documentation only. |
| [LM-044](task-cards/LM-044-ci-supported-matrix-evidence.md) | BLOCKED | CI/security | LM-024, LM-025, LM-026, LM-027, LM-030, LM-040 | Activate full pinned matrix and preserve production-evidence outputs. |
| [LM-045](task-cards/LM-045-technical-docs-support-matrix.md) | BLOCKED | Documentation | LM-003, LM-010, LM-013, LM-030, LM-041, LM-044 | Publish technical docs accurately matching accepted CLI/API/evidence. |
| [LM-042](task-cards/LM-042-release-supply-chain.md) | BLOCKED | Release engineering | LM-044, LM-045 | Create reproducible pre-1.0 release workflow and artifacts. |
| [LM-043](task-cards/LM-043-pre-release-certification.md) | BLOCKED | Release owner/reviewer | LM-024, LM-025, LM-026, LM-027, LM-030, LM-042 | Certify an experimental release candidate; never prematurely certify v1. |

## Parallel waves

### Wave 0 — establish an owned baseline

LM-004 only. No code-changing worker task begins before it establishes a baseline commit/branch or records a maintainer-approved disposition for the current dirty tree.

### Wave 1 — characterize and propose contracts

After LM-004, LM-001 (offline characterization) and LM-003 (configuration-policy proposal) may proceed in separate worktrees. Neither changes runtime behavior. Their evidence informs LM-000.

### Wave 2 — approve contracts and build test infrastructure

LM-000 is the maintainer approval gate after LM-001 and LM-003. After LM-000, LM-002 may build the pinned harness. LM-005 follows LM-001 and LM-002 and runs database characterization only through that isolated harness.

### Wave 3 — establish pure boundaries

LM-010, LM-011, and LM-012 may start after their listed dependencies in separate worktrees. They have overlapping public/API files, so merge one at a time with coordinator review. LM-013 follows the accepted API/CLI boundaries and configuration policy; it is the only card that owns implementation of the current config-loader disposition.

### Wave 4 — database safety chain

LM-020 → LM-021 → LM-022 is serial. It establishes private connections, lock semantics, then metadata. Do not parallelize these cards.

### Wave 5 — planner and operations

LM-023 follows metadata. After LM-023 is accepted, LM-024, LM-025, and LM-026 may be developed in isolated worktrees but must be merged/rebased one at a time with full integration revalidation because they share metadata and lock contracts. LM-027 follows LM-024. LM-030 follows every operational library/config/CLI prerequisite.

### Wave 6 — release evidence

LM-040 and LM-041 are scaffolding cards; they do not certify a release. LM-044 activates final CI evidence after runtime operations exist. LM-045 writes final technical docs from accepted behavior/evidence. LM-042 then builds release machinery, and LM-043 makes the final experimental-release decision.

## Authoritative direct-dependency graph

The following lines are the authoritative direct-dependency graph and must exactly match each card and the queue table. A dependency may also be transitive, but it must not be omitted here when it is listed on a card.

```text
LM-004 <- none
LM-001 <- LM-004
LM-003 <- LM-004
LM-000 <- LM-001, LM-003
LM-002 <- LM-000, LM-004
LM-005 <- LM-001, LM-002
LM-010 <- LM-000, LM-001, LM-005
LM-011 <- LM-000, LM-001, LM-002
LM-012 <- LM-000, LM-001
LM-013 <- LM-000, LM-003, LM-010, LM-012
LM-020 <- LM-002, LM-010
LM-021 <- LM-020
LM-022 <- LM-002, LM-021
LM-023 <- LM-010, LM-011, LM-022
LM-024 <- LM-021, LM-022, LM-023
LM-025 <- LM-011, LM-021, LM-022, LM-023
LM-026 <- LM-011, LM-021, LM-022, LM-023
LM-027 <- LM-024
LM-030 <- LM-012, LM-013, LM-023, LM-024, LM-025, LM-026, LM-027
LM-040 <- LM-000, LM-002
LM-041 <- LM-000
LM-044 <- LM-024, LM-025, LM-026, LM-027, LM-030, LM-040
LM-045 <- LM-003, LM-010, LM-013, LM-030, LM-041, LM-044
LM-042 <- LM-044, LM-045
LM-043 <- LM-024, LM-025, LM-026, LM-027, LM-030, LM-042
```

## Board-consistency check

Before assignment and before release certification, the coordinator must verify:

1. each queue ID has exactly one card;
2. each card lists status, dependencies, goal, acceptance criteria, and verification;
3. queue dependencies equal card dependencies;
4. graph edges do not omit any card dependency;
5. every `READY` card has accepted dependencies and an owned baseline;
6. no card states or implies current production readiness.

A small repository script/check may automate 1–4. A coordinator records its output in the relevant task/PR.

## Resume protocol

A new coordinator reads: `README.md`, `architecture.md`, this board, LM-004 evidence/current worktree status, then the selected task card. It must compare task state to `git status --short --branch`, `git diff`, and current test evidence before assigning work.
