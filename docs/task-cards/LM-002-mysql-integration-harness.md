# LM-002 — Build isolated pinned MySQL integration harness

- Status: DONE
- Suggested owner: QA infrastructure / DevOps
- Depends on: LM-004, LM-000
- Architecture: §§17–18, Phase 1

## Goal

Provide a reproducible harness for the approved MySQL 8.0 and 8.4 lines. It supports later runtime, SQL-template, crash/failpoint, and database-characterization tests without touching developer or production data.

## Requirements

- Use exact MySQL image versions approved in LM-000; never `latest` as evidence.
- Create a unique isolated database per test/run and assert a test-only database naming policy before destructive setup/cleanup.
- Expose safe test configuration through documented environment variables; no live credentials in files/logs.
- Configure/document SQL modes, UTC/non-UTC sessions, utf8mb4, TLS path where available, least-privilege failure fixture, and `lower_case_table_names=0` policy.
- Make integration tests opt-in and safe by default.
- Provide a documented failpoint/crash simulation strategy usable by LM-024/025/026.

## Acceptance criteria

- One documented command runs a harmless smoke test against each pinned line.
- Harness refuses a non-test database target before destructive actions.
- CI-ready local configuration exists without making a release support claim.
- The harness includes an integration assertion that generated `create_<table>_table` up/down templates can create and drop an isolated table; LM-011 consumes this test after source implementation changes.

## Verification

Run the tagged harness against both approved pinned lines; preserve server/image/database-isolation evidence without secrets.

## Do not

- Do not connect to remote maintainer-provided databases.
- Do not implement lock or metadata semantics.
