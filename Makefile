# syslogq — developer entrypoints. Phase 0 skeleton: backend build/test/lint
# only. Compose, integration, e2e, and fuzz targets land with their phases
# (docs/roadmap.md; docs/testing.md §7).
#
# The Go module lives in backend/; all Go targets cd there first.

SHELL := /bin/bash
GO    ?= go

VERSION ?= $(shell git describe --tags --always 2>/dev/null || echo dev)

.PHONY: help build build-tools web run test lint security fmt tidy clean \
	docker-build compose-up compose-down compose-logs compose-smoke

help: ## Show available targets
	@grep -E '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) | \
		awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-16s\033[0m %s\n", $$1, $$2}'

build: web ## Build the syslogq binary (with embedded web UI)
	cd backend && $(GO) build -ldflags "-X main.version=$(VERSION)" -o bin/syslogq ./cmd/syslogq

web: ## Build the SPA into backend/internal/web/dist (embedded by go:embed)
	cd frontend && npm ci && npm run build

build-tools: ## Build the loggen load generator
	cd backend && $(GO) build -o bin/loggen ./cmd/loggen

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

docker-build: ## Build the syslogq container image
	docker build -f deploy/docker/Dockerfile --build-arg VERSION=$(VERSION) -t syslogq:$(VERSION) .

compose-up: ## Start the dev stack (syslogq + VictoriaLogs)
	docker compose up -d --build

compose-down: ## Stop the dev stack (keep volumes)
	docker compose down

compose-logs: ## Tail dev stack logs
	docker compose logs -f

compose-smoke: ## End-to-end check: logger → syslogq → VictoriaLogs
	docker compose up -d --build
	@set -euo pipefail; \
	marker="smoke-$$(date +%s)"; \
	for i in $$(seq 1 60); do \
		curl -fsS http://localhost:8080/ready >/dev/null && break; \
		sleep 2; \
	done; \
	logger -n 127.0.0.1 -P 514 --udp "$$marker udp compose smoke"; \
	logger -n 127.0.0.1 -P 514 --tcp "$$marker tcp compose smoke"; \
	for i in $$(seq 1 30); do \
		hits=$$(curl -fsS --get 'http://127.0.0.1:9428/select/logsql/query' \
			--data-urlencode "query=~\"$$marker\"" 2>/dev/null | grep -c "$$marker" || true); \
		[ "$${hits:-0}" -ge 2 ] && { echo "smoke OK: $$hits entries in VictoriaLogs"; exit 0; }; \
		sleep 1; \
	done; \
	echo "smoke FAILED"; docker compose logs; exit 1
