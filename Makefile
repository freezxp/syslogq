# syslogq — developer entrypoints. Phase 0 skeleton: backend build/test/lint
# only. Compose, integration, e2e, and fuzz targets land with their phases
# (docs/roadmap.md; docs/testing.md §7).

SHELL      := /bin/bash
GO         ?= go
GOFLAGS    ?=
FRONTEND_DIR := frontend
BIN_DIR    := backend/bin

.PHONY: help build run test lint fmt security tidy clean

help: ## Show available targets
	@grep -E '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) | \
		awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-12s\033[0m %s\n", $$1, $$2}'

build: ## Build the syslogq binary
	$(GO) build $(GOFLAGS) -ldflags "-X main.version=$$(git describe --tags --always 2>/dev/null || echo dev)" \
		-o $(BIN_DIR)/syslogq ./backend/cmd/syslogq

run: ## Run syslogq locally (dev config)
	$(GO) run ./backend/cmd/syslogq $(ARGS)

test: ## Run unit tests with race detector
	$(GO) test -race -count=1 ./backend/...

lint: ## go vet, gofmt check, staticcheck
	@out=$$($(GO) vet ./backend/...) && echo "vet ok" || { echo "$$out"; exit 1; }
	@files=$$(gofmt -l backend) && [ -z "$$files" ] || { echo "gofmt needed: $$files"; exit 1; }
	$(GO) run honnef.co/go/tools/cmd/staticcheck@latest ./backend/...

security: ## govulncheck + gosec
	$(GO) run golang.org/x/vuln/cmd/govulncheck@latest ./backend/...
	$(GO) run github.com/securego/gosec/v2/cmd/gosec@latest -quiet -severity medium ./backend/...

fmt: ## Format Go sources
	$(GO) fmt ./backend/...

tidy: ## go mod tidy
	cd backend && $(GO) mod tidy

clean: ## Remove build artifacts
	rm -rf $(BIN_DIR)
