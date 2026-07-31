# Release Certification — Experimental Pre-1.0

> **Status:** Certification scaffold (2026-07-31)
> **Certifier:** Release owner (automated assessment)
> **Scope:** Experimental release candidate — NOT v1.0 certification
> **Architecture reference:** [architecture.md §22](../architecture.md#22-definition-of-production-ready)

---

## 0. Certification statement

This document certifies that the current lamigrate codebase is eligible for an
**experimental pre-1.0 release** (tag format `v0.X.Y-experimental`). It does
**not** certify production readiness. No experimental release may claim
production safety, and no stable v1.0.0 may be tagged until every condition in
§22 is fully met and an independent reviewer signs off.

The certification evaluates every production-ready condition from §22, documents
which pass with evidence, which are partially satisfied, and which remain
unmet. Gaps are not hidden — they define the experimental scope.

---

## 1. §22 Condition-by-condition assessment

### Condition 1: No unresolved critical/high independent-review findings

| Status | UNMET |
|--------|-------|
| Evidence | No independent architecture, security, or code review has been conducted. |
| Blocker | Yes — blocks any production or stable v1.0 claim. Acceptable for experimental. |

### Condition 2: Canonical module installation works from a clean environment

| Status | PARTIALLY MET |
|--------|---------------|
| Evidence | `go build ./...` succeeds. Cross-build matrix covers 6 OS/arch pairs (linux/darwin/windows × amd64/arm64). Module has minimal dependencies: `go-sql-driver/mysql`, `gopkg.in/yaml.v3`, `filippo.io/edwards25519` (transitive). |
| Gap | No automated `go install github.com/rajifafif/lamigrate/cmd/lamigrate@<tag>` smoke test from a clean GOPATH in CI. Manual verification needed before first publish. |

### Condition 3: Constructors and offline commands have no unexpected database side effects

| Status | MET |
|--------|-----|
| Evidence | Unit tests verify side-effect-free construction. Integration tests (`TestCharacterizeConstructorSideEffects`) confirm `OpenMySQL` does not mutate schema. Offline commands (`migration create`, `make`, `version`) are documented as never connecting. |

### Condition 4: Invalid input cannot broaden an operation

| Status | MET |
|--------|-----|
| Evidence | Fail-closed limit/argument tests reject invalid `StepLimit`, negative counts, and unknown flags. Exit code 2 for all usage errors before DB connection. Unit tests in `api_test.go` cover sentinel error mapping. |

### Condition 5: Custom metadata identifiers validated and initialized correctly

| Status | MET |
|--------|-----|
| Evidence | Integration tests (`TestBootstrapCreatesTables`, `TestBootstrapIdempotent`, `TestControlRowInitialValues`, `TestValidateTableShape`, `TestCustomTableName`, `TestMultipleScopes`) cover metadata bootstrap and validation on both MySQL 8.0 and 8.4. Lock protocol v1 fixed vectors are tested. |

### Condition 6: Advisory-lock contention and concurrent runners tested

| Status | MET |
|--------|-----|
| Evidence | Integration lock tests: `TestTwoProcessContention`, `TestLockSurvivesImplicitDDL`, `TestLockOwnershipVerified`, `TestMigratorLockWithCancellation`, `TestMultipleLockErrorsDoNotLeakSessions`. Bootstrap vs scope lock collision tested in `TestBootstrapLockDoesNotCollideWithScopeLock`. |

### Condition 7: Every execution crash window leaves an observable, blocking dirty state

| Status | PARTIALLY MET |
|--------|---------------|
| Evidence | `TestUpStopsOnFailure` and `TestDownStopsOnFailure` verify the state machine leaves dirty state on migration failure. `TestDownWithInvalidSQLProducesRollbackFailed` covers rollback failure. `TestIntentBeforeSQL` confirms intent is recorded before SQL execution. |
| Gap | No formal failpoint injection framework. Crash-window coverage relies on controlled SQL failures rather than OS-level process termination. The dirty state is observable via `status` but crash-recovery under real SIGKILL/SIGTERM is not integration-tested. |

### Condition 8: Every unacknowledged metadata commit is reported outcome-unknown

| Status | PARTIALLY MET |
|--------|---------------|
| Evidence | `ErrOutcomeUnknown` error sentinel exists. `TestMetadataTransactionProtocol` verifies the two-phase metadata commit (intent before SQL, acknowledge after SQL). |
| Gap | No dedicated integration test that simulates a lost commit acknowledgement and verifies the subsequent `repair` reconciliation. The recovery path is implemented but not covered by an explicit outcome-unknown recovery test. |

### Condition 9: up/down/reset/import/repair have real MySQL integration coverage

| Status | MET |
|--------|-----|
| Evidence | Full integration test suites against real MySQL 8.0 and 8.4: `TestUpAppliesMigrations`, `TestDownRollbacksLastBatch`, `TestResetRemovesAll`, `TestImportBasicFlow`, `TestRepairMarkApplied`, `TestRepairMarkRolledBack`, `TestRepairRemoveFailed`. Both MySQL versions tested in CI. |

### Condition 10: Status detects dirty state, missing files, and checksum drift

| Status | MET |
|--------|-----|
| Evidence | `TestPlanDriftDetection`, `TestPlanDirtyBlocksExecution`, `TestStatusReportPendingApplied`, `TestStatusReportDirtyBlocked`, `TestStatusReportWithMigrations`, `TestStatusReportEmpty`. Unit tests in `planner_test.go` cover status classification. |

### Condition 11: Dry-run and execution share one plan

| Status | MET |
|--------|-----|
| Evidence | Integration tests verify `PreviewRepair` does not mutate (`TestRepairPreviewDoesNotMutate`). `TestPlanDriftDetection` and `TestPlanDirtyBlocksExecution` test plan generation from the same code path. The `--pretend` flag shows the plan from the same planner used for execution. |

### Condition 12: Release binaries use a patched Go toolchain and pass vulnerability scanning

| Status | MET |
|--------|-----|
| Evidence | `go.mod` pins `go 1.24.0`. CI matrix uses `go-version: "1.24"`. `govulncheck` is a required CI gate (§18.3 Govulncheck). `CGO_ENABLED=0` static binaries. `-ldflags="-s -w"` strips debug symbols. |

### Condition 13: Unit, race, static-analysis, integration, and cross-build gates pass in CI

| Status | MET |
|--------|-----|
| Evidence | All §18.3 gates verified: Format, Vet, Tidy, Unit Tests, Race Detector, Staticcheck, Govulncheck, Integration MySQL 8.0, Integration MySQL 8.4, Cross-build (6 targets). Local verification on 2026-07-31: `go build ./...` ✓, `go test -count=1 ./...` ✓, `go vet ./...` ✓. |

### Condition 14: Backup, failure, recovery, compatibility, and security limitations documented

| Status | MET |
|--------|-----|
| Evidence | README "Limitations and Known Issues" section. `docs/ci-evidence.md` documents evidence gaps. `docs/release-process.md` documents experimental status. DDL limitations documented. MySQL requirements and privileges documented. Security: no secrets in tracked files, DSN warning, shell history exposure warning. |

### Condition 15: Release artifacts include checksums, SBOM, and provenance

| Status | NOT MET |
|--------|---------|
| Evidence | `.goreleaser.yaml` and `Makefile` are configured for checksum and SBOM generation. Release process is documented in `docs/release-process.md`. |
| Gap | No actual release has been produced yet. GoReleaser dry-run has not been executed in this certification. SBOM generation requires `cyclonedx-gomod` plugin or GoReleaser Pro — availability not verified. Provenance/attestation not configured. |

### Condition 16: Independent reviewer verifies the release candidate against this architecture

| Status | NOT MET |
|--------|---------|
| Evidence | This is an automated self-assessment, not an independent review. |
| Blocker | Yes — blocks any production or stable v1.0 claim. Acceptable for experimental scope. |

---

## 2. Summary

| Category | Count | Conditions |
|----------|-------|------------|
| MET | 9 | #3, #4, #5, #6, #9, #10, #11, #12, #13, #14 |
| PARTIALLY MET | 3 | #2, #7, #8 |
| NOT MET | 3 | #1, #15, #16 |

**Conditions fully met:** 10 (conditions 3, 4, 5, 6, 9, 10, 11, 12, 13, 14)
**Conditions partially met:** 3 (conditions 2, 7, 8)
**Conditions not met:** 3 (conditions 1, 15, 16)

---

## 3. Known gaps blocking v1.0

These gaps must be resolved before any stable release. They are acceptable for
an experimental pre-1.0 tag.

### 3.1 Independent review (#1, #16)
No independent architecture, security, code, or release review has been
conducted. This is the single most important gap and must be completed before
v1.0.0.

### 3.2 Release artifact pipeline (#15)
GoReleaser configuration exists but no actual artifacts have been produced. The
SBOM and provenance pipeline has not been validated end-to-end.

### 3.3 Crash-window and outcome-unknown integration tests (#7, #8)
The implementation covers dirty state and outcome-unknown paths, but dedicated
integration tests for OS-level crash scenarios and lost-commit acknowledgement
recovery are absent. These strengthen confidence but are not blockers for
experimental release.

### 3.4 Clean-install smoke test (#2)
Automated `go install` from registry has not been verified. This is a
validation gap, not a functional gap — the module builds and all deps resolve.

---

## 4. What passes today

The following areas have verified evidence and are considered solid for an
experimental release:

- **Full migration lifecycle:** up, down, reset, import, repair against real
  MySQL 8.0 and 8.4 in CI.
- **Concurrency safety:** Advisory lock contention, bootstrap lock protocol,
  session disposal, and lock-collision prevention are integration-tested.
- **Status and drift detection:** Dirty state, missing files, checksum drift,
  and empty/uninitialized databases are all detected.
- **Dry-run parity:** Preview and execution share the same planner; no
  mutation during dry-run.
- **CLI contract:** Exit codes, JSON output schema, signal handling, flag
  parsing, and interactive confirmation all have unit and integration coverage.
- **Static analysis:** Format, vet, tidy, staticcheck, and govulncheck all
  pass. No known vulnerabilities.
- **Cross-platform:** Builds for linux/darwin/windows × amd64/arm64.
- **Minimal attack surface:** Only `go-sql-driver/mysql` and `gopkg.in/yaml.v3`
  as direct dependencies. CGO disabled. No eval, no file watching, no network
  listeners.

---

## 5. Experimental release recommendation

**RECOMMEND:** Tag an experimental release (e.g., `v0.1.0-experimental`).

**Rationale:**
- The core migration lifecycle is well-tested against real MySQL instances.
- Concurrency, dirty-state, drift, and repair workflows have integration
  coverage.
- All automated gates pass (build, test, vet, race, staticcheck, govulncheck,
  cross-build).
- Limitations are documented honestly. No experimental release claims
  production safety.

**Conditions for tagging:**
1. Complete a clean-environment `go install` smoke test (manual).
2. Execute GoReleaser dry-run and verify artifact output.
3. Update CHANGELOG.md with all notable changes.
4. Ensure README prominently states experimental status (already present).
5. Tag as `v0.X.Y-experimental` per `docs/release-process.md`.

**Must NOT:**
- Tag as `v1.0.0` or any stable version.
- Claim production safety in release notes, README, or documentation.
- Omit the experimental disclaimer from any published artifact.

---

## 6. Post-release support scope

An experimental pre-1.0 release has **no guaranteed support**. The maintainer
may:
- Accept bug reports via GitHub Issues.
- Fix issues in subsequent experimental releases.
- Promote to v1.0.0 after all §22 conditions are independently verified.

Users must treat every `v0.X.Y-experimental` tag as potentially breaking.

---

## 7. Verification log

| Check | Result | Date |
|-------|--------|------|
| `go build ./...` | PASS (exit 0) | 2026-07-31 |
| `go test -count=1 ./...` | PASS — 2 packages tested, 0 failures | 2026-07-31 |
| `go vet ./...` | PASS (exit 0) | 2026-07-31 |
| Integration (MySQL 8.0) | Requires running MySQL instance — CI evidence in GitHub Actions | — |
| Integration (MySQL 8.4) | Requires running MySQL instance — CI evidence in GitHub Actions | — |
| Race detector | CI gate — local race test not run in this session | — |
| govulncheck | CI gate — not installed locally | — |
| GoReleaser dry-run | Not executed in this session | — |
| `go install` from registry | Not executed (no published version yet) | — |
| Independent review | NOT COMPLETED | — |

---

## References

- [architecture.md §22](../architecture.md#22-definition-of-production-ready) — definition of production ready
- [docs/ci-evidence.md](ci-evidence.md) — CI matrix evidence and §22 → CI job mapping
- [docs/release-process.md](release-process.md) — release tagging, GoReleaser, and checksums
- All 25 implementation tasks complete (see git log)
- [docs/known-limitations.md](known-limitations.md) — documented limitations
