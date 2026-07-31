# LM-043 — Certify first experimental release candidate

- Status: DONE
- Suggested owner: release owner + independent reviewer
- Depends on: LM-024, LM-025, LM-026, LM-027, LM-030, LM-042
- Architecture: §§18–22

## Goal

Perform the final evidence-based assessment for an explicitly experimental pre-1.0 release. This task does not authorize `v1.0.0`.

## Required evidence

- All target CI gates passing on the pinned supported matrix.
- Real MySQL coverage for up/down/reset/import/adoption/repair, concurrency, dirty states, drift, crash/failpoint behavior, and release builds.
- Clean module installation/import test under `github.com/rajifafif/lamigrate`.
- Release artifacts with checksums, SBOM, provenance, patched toolchain evidence, and vulnerability scan.
- Documentation/governance complete and accurately scoped.
- Independent architecture, security, code, and release reviews with no unresolved critical/high finding.

## Acceptance criteria

- Release is labeled experimental/pre-1.0 and does not claim production safety.
- Every production-ready condition in §22 has a concrete evidence reference or is explicitly unmet.
- Any unmet condition blocks release or is reflected in a narrower experimental scope, not hidden.
- A post-release support/rollback/incident contact path exists.

## Verification

Release owner produces a certification report mapped line-by-line to architecture §22; an independent reviewer signs off or returns blockers.
