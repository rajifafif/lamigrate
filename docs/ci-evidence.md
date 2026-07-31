# CI Evidence Map — LM-044

Status: Experimental scaffolding
Last updated: 2026-07-31

> **Pre-1.0 disclaimer.** No CI job result constitutes release certification.
> All jobs are evidence scaffolding. Until an independent reviewer verifies a
> release candidate against §22, lamigrate remains experimental and must not
> claim production safety.

## 1. §18.3 Gate → CI Job Mapping

Every gate from §18.3 ("Target CI gates") maps to exactly one workflow job.
The full matrix runs on every push and PR to `main`.

| §18.3 Command | CI Job Name | Go Version | Notes |
|---|---|---|---|
| `test -z "$(gofmt -l .)"` | §18.3 Format | 1.24 | Rejects unformatted source. |
| `go vet ./...` | §18.3 Vet | 1.24 | Reports suspicious constructs. |
| `go mod tidy -diff` | §18.3 Tidy | 1.24 | Ensures go.mod/go.sum are clean; fails if tidy produces diffs. |
| `go test -count=1 ./...` | §18.3 Unit Tests | 1.24 | All non-integration unit tests. |
| `go test -race -count=1 ./...` | §18.3 Race Detector | 1.24 | Same tests with `-race` (ThreadSanitizer). |
| `staticcheck ./...` | §18.3 Staticcheck | 1.24 | honnef.co/go/tools static analysis. |
| `govulncheck ./...` | §18.3 Govulncheck | 1.24 | golang.org/x/vuln known-vulnerability scan. |
| `go test -tags=integration -count=1 -v ./integration/` (MySQL 8.4) | §18.3 Integration MySQL 8.4 | 1.24 | Pinned `mysql:8.4` service container. |
| `go test -tags=integration -count=1 -v ./integration/` (MySQL 8.0) | §18.3 Integration MySQL 8.0 | 1.24 | Pinned `mysql:8.0.35` service container. |
| Cross-build (linux/darwin/windows × amd64/arm64) | §18.3 Cross-build | 1.24 | Compiles `./cmd/lamigrate` for all 6 OS/arch pairs. |

### Pinned versions

| Component | Pinned Value |
|---|---|
| Go toolchain | `1.24` (matches `go.mod` `go 1.24.0`) |
| MySQL 8.0 image | `mysql:8.0.35` |
| MySQL 8.4 image | `mysql:8.4` |

Unpinned `latest` images are not used.

### Job classification

| Classification | Jobs |
|---|---|
| Release-certifying (when §22 is met) | Format, Vet, Tidy, Unit Tests, Race Detector, Staticcheck, Govulncheck, Integration MySQL 8.0, Integration MySQL 8.4, Cross-build |
| Informational only | _(none — all §18.3 gates are structural prerequisites)_ |

> **Current state:** Since lamigrate is pre-1.0, all jobs are classified as
> scaffolding evidence. None are release-certifying yet. The "Release-certifying"
> column shows what each job would become once §22 is satisfied.

## 2. §22 Production-Ready Condition → Evidence Map

§22 defines 16 conditions for production readiness. The table maps each
condition to the CI evidence (or documents manual/code evidence).

| §22 Condition | CI Job(s) | Evidence Type |
|---|---|---|
| Unresolved critical/high independent-review findings absent | _(manual)_ | Independent review gate — not automatable in CI. |
| Canonical module installation works from clean environment | §18.3 Cross-build | Cross-compilation for all target platforms validates the module builds. Full install-from-registry test is manual (§19). |
| Constructors/offline commands have no unexpected DB side effects | §18.3 Unit Tests + §18.3 Integration MySQL 8.0/8.4 | Unit tests cover side-effect-free construction; integration tests verify no mutation during `NewMySQL`/`OpenMySQL`/preview. |
| Invalid input cannot broaden an operation | §18.3 Unit Tests | Fail-closed limit/argument tests reject invalid StepLimit, counts, and flags. |
| Custom metadata identifiers validated and initialized | §18.3 Integration MySQL 8.0/8.4 | Integration tests cover lock-v1 fixed vectors, semantic metadata validation on both MySQL lines. |
| Advisory-lock contention and concurrent runners tested | §18.3 Integration MySQL 8.0/8.4 | Integration tests cover lock contention, simultaneous up attempts, connection-ID continuity. |
| Every crash window leaves observable dirty state | §18.3 Integration MySQL 8.0/8.4 | Integration failpoint tests verify dirty states after crashes, failed rollback, lost commit acknowledgements. |
| Unacknowledged metadata commit reported outcome-unknown | §18.3 Integration MySQL 8.0/8.4 | Integration tests cover lost-commit scenarios and post-restoration ownership checks. |
| up/down/reset/import/repair have real MySQL integration coverage | §18.3 Integration MySQL 8.0 + §18.3 Integration MySQL 8.4 | Both MySQL lines exercise full migration lifecycle, import, adoption, and repair paths. |
| Status detects dirty state, missing files, and checksum drift | §18.3 Integration MySQL 8.0/8.4 + §18.3 Unit Tests | Integration tests cover drift detection; unit tests cover status classification. |
| Dry-run and execution share one plan | §18.3 Integration MySQL 8.0/8.4 | Integration tests verify preview/execution parity for up, down, and reset. |
| Release binaries use patched Go toolchain and pass vuln scan | §18.3 Govulncheck + pinned `go-version: "1.24"` | govulncheck scans for known CVEs; Go 1.24 pinning ensures patched toolchain. |
| Unit, race, static-analysis, integration, cross-build gates pass in CI | §18.3 Unit Tests, §18.3 Race Detector, §18.3 Staticcheck, §18.3 Integration MySQL 8.0/8.4, §18.3 Cross-build | All §18.3 gates — this is the direct CI evidence line from §22. |
| Backup/failure/recovery/compatibility/security limitations documented | §18.3 Govulncheck + manual | govulncheck flags known vulnerabilities; limitation documentation is manual. |
| Release artifacts include checksums, SBOM, and provenance | _(manual — not yet implemented)_ | §19 requires GoReleaser/checksums/SBOM/provenance. Not part of this CI matrix. |
| Independent reviewer verifies release candidate against §22 | _(manual)_ | Human review gate — not automatable in CI. |

## 3. Matrix dimensions

### MySQL version matrix

| Image | Version | Purpose |
|---|---|---|
| `mysql:8.0.35` | 8.0.35 | Oldest supported MySQL 8.0 patch |
| `mysql:8.4` | 8.4 | Latest supported MySQL 8.x line |

### Cross-build matrix

| OS | Arch | Combinations |
|---|---|---|
| linux | amd64, arm64 | 2 |
| darwin | amd64, arm64 | 2 |
| windows | amd64, arm64 | 2 |
| **Total** | | **6** |

### Total job count

| Category | Jobs |
|---|---|
| Static analysis | Format, Vet, Tidy, Staticcheck, Govulncheck (5) |
| Unit tests | Unit Tests, Race Detector (2) |
| Integration | Integration MySQL 8.0, Integration MySQL 8.4 (2) |
| Cross-build | Cross-build (6 matrix entries) |
| **Total distinct jobs** | **15** |

## 4. What this document does NOT claim

- This CI evidence does not constitute production-readiness (§22).
- No automated job certifies a release.
- Manual gates (independent review, artifact publishing, SBOM, provenance)
  remain unfulfilled and are documented as blockers in §19.
- The integration tests record SQL modes, timezones, TLS, privileges,
  character sets, and case-policy inputs as evidence artifacts; those
  artifacts are test output, not release binaries.
