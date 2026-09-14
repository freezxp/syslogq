# syslogq — developer entrypoints. Phase 0 skeleton: backend build/test/lint
# only. Compose, integration, e2e, and fuzz targets land with their phases
# (docs/roadmap.md; docs/testing.md §7).
#
# The Go module lives in backend/; all Go targets cd there first.

SHELL := /bin/bash
GO    ?= go

VERSION ?= $(shell git describe --tags --always 2>/dev/null || echo dev)

.PHONY: help build run test lint security fmt tidy clean

help: ## Show available targets
	@grep -E '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) | \
		awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-12s\033[0m %s\n", $$1, $$2}'

build: ## Build the syslogq binary
	cd backend && $(GO) build -ldflags "-X main.version=$(VERSION)" -o bin/syslogq ./cmd/syslogq

run: ## Run syslogq locally (dev config)
	cd backend && $(GO) run ./cmd/syslogq $(ARGS)

test: ## Run unit tests with race detector
	cd backend && $(GO) test -race -count=1 ./...

lint: ## go vet, gofmt check, staticcheck
	cd backend && $(GO) vet ./...
	@files=$$(cd backend && gofmt -l .); \
		[ -z "$$files" ] || { echo "gofmt needed: $$files"; exit 1; }
	cd backend && $(GO) run honnef.co/go/tools/cmd/staticcheck@latest ./...

security: ## govulncheck + gosec
	cd backend && $(GO) run golang.org/x/vuln/cmd/govulncheck@latest ./...
	cd backend && $(GO) run github.com/securego/gosec/v2/cmd/gosec@latest -quiet -severity medium ./...

fmt: ## Format Go sources
	cd backend && $(GO) fmt ./...

tidy: ## go mod tidy
	cd backend && $(GO) mod tidy

clean: ## Remove build artifacts
	rm -rf backend/bin
