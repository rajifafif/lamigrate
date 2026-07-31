# Changelog

All notable changes to lamigrate will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/),
and this project adheres to [Semantic Versioning](https://semver.org/).

## [Unreleased]

### Added
- Task board and multi-agent execution backlog (docs/TASKS.md)
- Architecture target document (architecture.md)
- Offline migration creation with timestamp naming
- MySQL integration via go-sql-driver/mysql
- CLI with up/down/reset/status/import/make commands
- YAML and .env configuration support

### Known limitations (experimental)
- No advisory lock or dirty-state protocol
- No metadata state machine
- No drift detection or checksums
- No signal handling
- No structured JSON output
- No integration test suite
