# lamigrate Release Process

> **Status:** Pre-1.0 experimental workflow.
> All releases prior to v1.0.0 are labeled **experimental** and must not claim
> production safety. See [architecture.md §19](../architecture.md) for the
> full release and supply-chain architecture specification.

---

## Overview

This document defines the step-by-step procedure for producing a reproducible,
checksummed, auditable release of lamigrate. It is intended for the maintainer
and any contributor with write access.

---

## Pre-release Checklist

Complete every item before tagging a release. Nothing here is optional.

- [ ] All CI checks pass on the release candidate commit
      (unit, race, integration, static analysis, cross-builds)
- [ ] `go vet ./...` reports no issues
- [ ] `staticcheck ./...` reports no issues (if installed)
- [ ] `gofmt -l .` produces no output
- [ ] `govulncheck ./...` reports no known vulnerabilities in dependencies
- [ ] `go version -m lamigrate` confirms pinned toolchain and module path
- [ ] `go test -tags=integration ./integration/...` passes against a clean
      MySQL 8.0 and MySQL 8.4 instance
- [ ] README install instructions are correct and testable from a clean
      environment (`go install github.com/rajifafif/lamigrate/cmd/lamigrate@<tag>`)
- [ ] CHANGELOG.md is updated with all notable changes since the last tag
- [ ] `architecture.md` and all task cards are current
- [ ] No hardcoded credentials, DSNs, or secrets in any tracked file
- [ ] The release branch or tag commit is on the protected default branch
      with required CI approval

---

## Version Tagging

lamigrate uses [Semantic Versioning 2.0.0](https://semver.org/).

### Pre-1.0 format

Before a stable v1.0.0, use the following pattern:

```
v0.X.Y-experimental
```

- `v0.1.0-experimental` — first public pre-release
- `v0.2.0-experimental` — next feature release
- `v0.1.1-experimental` — patch release

### Stable v1.0.0 criteria

A stable v1.0.0 is **not** appropriate until:
- the public Go API has demonstrated backward compatibility through at least
  one complete pre-1.0 release cycle
- the metadata schema has not required breaking changes
- the security policy, support matrix, and contribution guide are published
- an independent architecture and security review has been completed

### Tagging steps

```bash
# 1. Ensure working tree is clean
git status

# 2. Verify CI is green on the target commit
#    (check GitHub Actions or Jenkins dashboard)

# 3. Create the annotated tag
git tag -a v0.X.Y-experimental -m "lamigrate v0.X.Y-experimental"

# 4. Push the tag (do NOT push release branches without review)
git push origin v0.X.Y-experimental
```

---

## GoReleaser Usage

GoReleaser automates cross-compilation, archiving, checksumming, and
GitHub release creation.

### Dry-run (no publish)

```bash
make release
```

This runs `goreleaser release --snapshot --clean --skip=publish` and
produces artifacts in `dist/` without creating a GitHub release.

### Full release

```bash
# Ensure GITHUB_TOKEN is set with repo + contents permissions
export GITHUB_TOKEN=ghp_...

# GoReleaser creates a draft GitHub release with checksums and SBOMs
goreleaser release --clean
```

### Build matrix

| OS    | Arch  | Archive format |
|-------|-------|----------------|
| linux | amd64 | tar.gz         |
| linux | arm64 | tar.gz         |
| darwin| amd64 | tar.gz         |
| darwin| arm64 | tar.gz         |
| windows| amd64| zip            |
| windows| arm64| zip            |

All binaries are built with `CGO_ENABLED=0` for static linking.

---

## Checksum Verification

Every release includes a `SHA256SUMS` file signed with SHA-256.

### Verify a downloaded release

```bash
# 1. Download the archive and SHA256SUMS from the GitHub release page
# 2. Verify
sha256sum -c SHA256SUMS

# 3. Expected output for each file:
#    lamigrate_v0.X.Y-experimental_linux_amd64.tar.gz: OK
```

### Using the Makefile

```bash
make checksum
```

This reads `dist/SHA256SUMS` and verifies all listed artifacts.

---

## SBOM Generation

GoReleaser generates a CycloneDX SBOM for each release archive as part of
the release pipeline. The SBOM is attached to the GitHub release as:

```
lamigrate_v0.X.Y-experimental_<os>_<arch>.sbom.json
```

To inspect an SBOM locally:

```bash
# Using syft (https://github.com/anchore/syft)
syft lamigrate_v0.X.Y-experimental_linux_amd64.tar.gz
```

SBOM generation requires GoReleaser Pro or the open-source SBOM plugins
(`cyclonedx-gomod`). If the plugin is not available, the SBOM step is
skipped with a warning; the release is still valid without it but the
release notes should note the omission.

---

## Changelog Maintenance

### Format

Follow [Keep a Changelog](https://keepachangelog.com/en/) conventions:

```markdown
# Changelog

## [v0.X.Y-experimental] - 2026-XX-XX

### Added
- Feature description (issue #N)

### Fixed
- Bug fix description (issue #N)

### Changed
- Breaking or notable change description

### Removed
- Removed feature description
```

### GoReleaser changelog

GoReleaser auto-generates a changelog from git commit messages, grouped
by conventional commit prefixes (feat, fix, etc.). This is appended to the
GitHub release notes. The hand-maintained CHANGELOG.md serves as the
authoritative human-readable record.

---

## Experimental Status Disclaimer

**Every** release artifact, GitHub release, and documentation page must
clearly state that lamigrate is experimental pre-1.0 software.

### Required language

> This is an experimental pre-1.0 release. The public API, metadata schema,
> and CLI interface may change without notice. It does not claim production
> safety. See architecture.md for the target architecture and known gaps.

### Where it appears

- `.goreleaser.yaml` — release header (automated)
- GitHub release body (automated via `.goreleaser.yaml` header)
- README.md — prominently near the top
- `go install` output — the version string includes `-experimental`

---

## Security Hardening

- No secrets (tokens, credentials, DSNs) appear in `.goreleaser.yaml`,
  Makefile, or any tracked configuration file.
- `CGO_ENABLED=0` produces static binaries with no dynamic library
  dependencies.
- `-ldflags="-s -w"` strips debug symbols to reduce binary size and
  surface area.
- `govulncheck` must pass before tagging.
- `go version -m` output is captured as release evidence to verify
  toolchain and module provenance.

---

## References

- [architecture.md §19](../architecture.md#19-release-and-supply-chain-architecture) — release and supply-chain architecture
- [architecture.md §22](../architecture.md#22-definition-of-production-ready) — definition of production ready
- [GoReleaser docs](https://goreleaser.com)
- [Semantic Versioning](https://semver.org/)
- [Keep a Changelog](https://keepachangelog.com/)
