# LM-042 — Build reproducible release and supply-chain workflow

- Status: DONE
- Suggested owner: release engineer
- Depends on: LM-044, LM-045
- Architecture: §19, Phase 5

## Goal

Create a repeatable pre-1.0 release pipeline with verifiable binaries and release metadata.

## Requirements

- Produce semantic-versioned tagged archives through GoReleaser or equivalent reproducible workflow.
- Publish platform archives, SHA-256 checksums, SBOM, and GitHub provenance/attestations.
- Embed/version binary metadata and inspect artifacts with `go version -m`.
- Test documented `go install github.com/rajifafif/lamigrate/...@<version>` from a clean environment.
- Use the pinned patched toolchain and final LM-044 matrix gates.

## Acceptance criteria

- A dry-run release from a clean tag produces only intended artifacts and no local secrets/configuration.
- Artifact checksums/SBOM/provenance are generated and documented.
- Release notes/changelog/support status are synchronized with LM-045.

## Verification

Clean-environment release dry run, install smoke test, artifact inspection, SBOM review, and independent supply-chain review.
