# Implementation Roadmap

Date: 2026-09-14 · Status: Phase 1 deliverables complete (core ingestion
verified end-to-end: `logger` → syslogq → VictoriaLogs; unit tests green;
compose smoke in CI). Each phase has explicit exit criteria; a phase is not
"done" until its walkthrough + gates pass (see `requirements.md` §8). No
performance number is claimed before it is measured (Phase 6 methodology).

## Phase 0 — Architecture
**Deliverables:** `docs/`: architecture, requirements, storage-comparison,
log-data-model, ingestion, api, frontend, security, deployment, testing,
roadmap + `decisions/` ADRs 0001–0006. Repo skeleton with module layout,
Makefile, CI.
**Exit:** documents reviewed; approval to proceed.

## Phase 1 — Core Ingestion — COMPLETE (2026-09-14)
Backend skeleton; `model.LogEntry`; RFC3164 + RFC5424 parsers (+ RFC6587
framing for TCP); UDP/TCP syslog listeners; normalization; VictoriaLogs
adapter (write path); batcher with bounded queues; config (YAML/env/flags);
Prometheus metrics; `/health` `/ready`; structured logging; `docker-compose.yml`
(syslogq + victorialogs); Makefile; unit tests for parsers/normalizer/batcher.
**Exit:** `logger -n 127.0.0.1 -P 514 --udp "test"` lands in VictoriaLogs and is
readable via VL API; soak 1K logs/sec for 10 min without queue growth; `go test ./...` green.

## Phase 2 — HTTP/JSON Ingestion + Auth — COMPLETE (2026-09-14)
`POST /api/v1/ingest` (single/array/NDJSON, batch limits); HTTP listener
config; JSON parser; auth (login/logout/me, bcrypt, sessions, RBAC middleware);
audit log table; TLS for HTTP API (cert config); rate limiting on ingest.
**Exit:** ingest via curl with token auth; unauthorized requests rejected; parser
metrics per format visible on `/metrics`.

## Phase 3 — Query API — COMPLETE (2026-09-14)
`query.Expr` AST + LogsQL compiler (central escaping); search endpoint with
time range + cursor pagination; field names/values/facets endpoints; stats +
hits endpoints; export endpoints (JSON/CSV/NDJSON streaming); query limits
(max duration, max window); `docs/openapi.yaml`; query unit tests incl.
injection attempts; memory-storage fake for handler tests.
**Exit:** every dashboard/explorer data need served by an endpoint; OpenAPI
validated in CI; escape tests pass.

## Phase 4 — Web UI
React+Vite+Tailwind app; design tokens (dark first); router + auth pages;
app shell/sidebar; **Log Explorer**: time picker, visual query builder ↔
advanced LogsQL mode, virtualized table, log detail drawer, field filters with
facets, URL state; **Live Tail** (WebSocket; pause/resume/clear/highlight);
**Dashboard** (cards, volume chart, severity, tops — range-aware via stats API);
**Sources** CRUD/status; **Saved Searches**; **Settings** (storage, retention,
users, system info); keyboard shortcuts; E2E smoke (Playwright).
**Exit:** Definition-of-done UI items 1–14 walkthrough passes; p95 first paint
< 2s on 100K-row explorer views; e2e smoke green.

## Phase 5 — Operations
Source management fully wired (create/edit/enable/disable/delete/test with
runtime reload); retention config → VL; audit UI; user management UI; system
health page (queues, drops, latency, storage health); alerting-ready metrics
naming review; backup/restore runbook.
**Exit:** admin flows work end-to-end without restart; readiness reflects real
dependencies; runbook executed once.

## Phase 6 — Performance & Hardening
`loggen` full feature set (RFC3164/5424/JSON, randomized fields, UDP/TCP/HTTP,
rate control); benchmarks at 1K/10K/50K/100K logs/sec: throughput, CPU, RSS,
parse p99, storage write p99, query p99 under load, dropped count = 0 at
sustained target; buffer/batch tuning from measurements; memory profile
review; results published in `docs/performance.md`.
**Exit:** sustained 100K logs/sec on reference hardware with documented
resource numbers; zero unexplained drops; report merged.

## Phase 7 — Advanced / Future
TLS syslog hardening; CEF, LEEF, generic-text/Apache/Nginx/firewall parsers;
OpenTelemetry logs receiver; Windows event forwarder support; ClickHouse
adapter behind conformance tests; multi-tenancy enforcement; clustering (VL
cluster, stateless API replicas, LB); Kubernetes/Helm in `deploy/kubernetes`;
alerting rules engine; AI-assisted analysis layer (retrieval over query API,
summarization, incident Q&A) designed per `architecture.md` §4 hooks.

## Sequencing Notes
- Phases 1–3 are backend; UI (4) can start once search endpoints exist (mid-3).
- Phase 6 benchmarks **must** precede any performance claims in README/docs.
- Phase 7 items are independent; pick by user value after Phase 6.

## Milestone Map (target granularity, not calendar)
```
M0 docs+ADR  → M1 ingest pipeline  → M2 auth+http ingest → M3 query API
   → M4 UI explorer/tail/dashboard → M5 ops/admin → M6 benchmark report → M7 advanced
```
