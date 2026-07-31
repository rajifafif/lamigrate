# Contributing to lamigrate

Thank you for considering contributing to lamigrate.

## Project status

**lamigrate is experimental software.** It has not reached v1.0.0 and does not
claim production-safety guarantees. See `architecture.md` for the target
production architecture and `docs/TASKS.md` for the implementation backlog.

Do not use lamigrate in production until an independent reviewer certifies a
release candidate against the production architecture.

## Getting started

1. Fork the repository.
2. Create a feature branch from `main`.
3. Make your changes with tests.
4. Run the full test suite:
   ```bash
   go test -count=1 ./...
   go test -race -count=1 ./...
   go vet ./...
   ```
5. Integration tests (requires Docker MySQL):
   ```bash
   go test -tags=integration -count=1 ./...
   ```
6. Submit a pull request.

## Development setup

- Go 1.24+
- Docker with MySQL 8.0 and 8.4 images for integration tests
- No live database credentials in commits, tests, or logs

## Code style

- Follow the conventions in existing files.
- Library code must not write to stdout/stderr.
- All public API types and functions must have doc comments.
- Use `errors.Is`/`errors.As` compatible error patterns.

## Pull request guidelines

- One logical change per PR.
- Include tests for new behavior.
- Reference the relevant task card (e.g., `LM-012`) if applicable.
- Do not weaken architecture requirements from `architecture.md`.

## Reporting issues

Use GitHub Issues. For security vulnerabilities, see SECURITY.md.

## License

By contributing, you agree that your contributions will be licensed under the
MIT License.
