# lamigrate Makefile
# Build, test, lint, and release targets.
# Architecture ref: architecture.md §19

.PHONY: build test test-integration lint lint-gofmt lint-vet lint-staticcheck release checksum clean help

BINARY   := lamigrate
PKG      := ./cmd/lamigrate
VERSION  ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")

# ---------- build ----------

build: ## Build for the current platform
	CGO_ENABLED=0 go build -ldflags="-s -w" -o $(BINARY) $(PKG)

# ---------- test ----------

test: ## Run all unit tests
	go test -race -count=1 ./...

test-integration: ## Run integration tests (requires running MySQL)
	go test -race -count=1 -tags=integration ./integration/...

# ---------- lint ----------

lint: lint-gofmt lint-vet lint-staticcheck ## Run all lint checks

lint-gofmt: ## Check gofmt compliance
	@echo "==> gofmt"
	@test -z "$$(gofmt -l .)" || { gofmt -l . ; echo "Run 'gofmt -w .' to fix"; exit 1; }

lint-vet: ## Run go vet
	@echo "==> go vet"
	@go vet ./...

lint-staticcheck: ## Run staticcheck (install: go install honnef.co/go/tools/cmd/staticcheck@latest)
	@echo "==> staticcheck"
	@which staticcheck >/dev/null 2>&1 || { echo "SKIP: staticcheck not installed"; exit 0; }
	@staticcheck ./...

# ---------- release ----------

release: ## GoReleaser snapshot build (dry-run, no publish)
	goreleaser release --snapshot --clean --skip=publish

checksum: ## Verify release checksums against SHA256SUMS
	@test -f dist/SHA256SUMS || { echo "No dist/SHA256SUMS found. Run 'make release' first."; exit 1; }
	@echo "==> Verifying checksums"
	@cd dist && sha256sum -c SHA256SUMS
	@echo "==> All checksums verified"

# ---------- clean ----------

clean: ## Remove build artifacts
	rm -f $(BINARY)
	rm -rf dist/

# ---------- help ----------

help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | sort | \
		awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-22s\033[0m %s\n", $$1, $$2}'
