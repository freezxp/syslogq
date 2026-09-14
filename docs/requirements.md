# Requirements — Syslog Server & Log Analytics Platform

Project codename: **syslogq** (working title). Status: Phase 0 — approved scope baseline.
This document distills the product requirements into engineering terms. It is the
reference against which the Definition of Done (§11) is evaluated.

---

## 1. Goals

Build a production-grade, web-based syslog server and log analytics platform as a
modern alternative to traditional syslog servers:

1. High-performance log ingestion (multi-protocol).
2. Normalization of heterogeneous log formats into one structured model.
3. Scalable, efficient storage with configurable retention.
4. Fast search, filtering, facets, and time-range analytics.
5. Real-time log tailing.
6. Modern dark-mode web UI suitable for NOC/SOC operations.
7. API-first architecture (every UI feature is available via REST/WebSocket).
8. Architecture prepared for future AI-assisted analysis.

## 2. Success Criteria (Definition of Done — Phase 1 complete)

A user can:

```bash
docker compose up -d
logger --server 127.0.0.1 --udp --port 514 "Test syslog message"
```

Then open the web UI and: search the log; filter by hostname, severity, facility;
select a custom time range; view dynamic fields and log details; see log volume
over time and ingestion rate; live-tail logs; export results; save a search; view
system health and ingestion metrics.

## 3. In-scope for Phase 1

| Area | Requirement |
|---|---|
| Syslog | RFC3164, RFC5424 over UDP and TCP; TLS listener supported via config |
| HTTP ingestion | `POST /api/v1/ingest` — single JSON, JSON array, NDJSON, batched |
| Normalization | One internal `LogEntry` model; arbitrary extra fields preserved |
| Storage | VictoriaLogs via storage abstraction; ClickHouse adapter designed-for |
| Query API | Search, time range, field filters, facets, field discovery, stats, pagination, export |
| Real-time | Live tail over WebSocket with pause/resume/clear/filter |
| Web UI | Login, dashboard, log explorer, query builder (visual + advanced), log detail, live tail, sources, saved searches, settings |
| AuthN/AuthZ | Username/password login, session tokens, roles Admin/Operator/Viewer |
| Metrics | Prometheus `/metrics`; ingestion/parser/storage/query metrics |
| Health | `/health`, `/ready`, liveness/readiness semantics |
| Config | YAML + env vars + CLI flags; no hardcoded infra config |
| Deployment | `docker-compose.yml` one-command bring-up |
| Tooling | `loggen` synthetic load generator (RFC3164/RFC5424/JSON) |

## 4. Out of scope for Phase 1 (architecture must accommodate later)

- CEF, LEEF, Windows Event Log, Apache/Nginx/firewall-specific parsers
- Kubernetes/OpenTelemetry ingestion
- Multi-tenancy enforcement (schema must reserve `tenant_id`)
- Kubernetes/Helm manifests (directory prepared only)
- ClickHouse adapter implementation (interface-level only)
- Clustering/HA of the Go service, alerting, AI assistant

## 5. Functional Requirements

### 5.1 Ingestion
- FR-I1: Configurable listeners (UDP/TCP/TLS syslog, HTTP). No hardcoded ports.
- FR-I2: Parser registry so new formats are plugins, not rewrites.
- FR-I3: Parse failures are counted, logged (sampled), and the raw message is
  storable under format `unknown` — never silently dropped unless configured.
- FR-I4: Batched writes; never one DB round-trip per log line.
- FR-I5: Backpressure with bounded queues; a slow backend must not cause
  unbounded memory growth. Drops are counted and reported.

### 5.2 Query
- FR-Q1: Free-text search plus field filters: `=`, `!=`, contains, starts-with,
  exists, `>`, `<`, AND/OR/NOT.
- FR-Q2: Visual query builder that compiles to the native backend language
  (LogsQL for VictoriaLogs); an Advanced mode exposes native syntax directly.
- FR-Q3: Time ranges: presets (5m…30d) + custom start/end with timezone.
  The range applies uniformly to results, charts, stats, facets, export.
- FR-Q4: Field discovery: names, value counts/facets, value search,
  include/exclude filters.
- FR-Q5: Cursor-based pagination; the UI never pages by OFFSET over large sets.
- FR-Q6: Export JSON/CSV/NDJSON via streaming (no full materialization in memory).

### 5.3 UI
- FR-U1: Pages: `/login`, `/`, `/dashboard`, `/logs`, `/logs/live`, `/sources`,
  `/sources/:id`, `/searches`, `/searches/:id`, `/settings`, `/settings/storage`,
  `/settings/retention`, `/settings/users`, `/settings/system`.
- FR-U2: URL-addressable search state (shareable links).
- FR-U3: Virtualized log table; dense but readable; dark mode first.
- FR-U4: Log detail drawer with all standard fields, dynamic fields, and
  copy/filter actions per value.
- FR-U5: Dashboard: totals, logs/sec, volume-over-time, severity distribution,
  top hosts/apps/source IPs/facilities/formats — all range-aware.
- FR-U6: Live tail: pause/resume/clear, auto-scroll, filter, highlight,
  max buffered records, search while tailing.
- FR-U7: Saved searches with name, description, query, default time range.

### 5.4 Administration
- FR-A1: Source management CRUD + enable/disable/status.
- FR-A2: Retention configuration (days) surfaced in UI; enforced by storage backend.
- FR-A3: User management (Admin role).
- FR-A4: Audit log for auth events, config changes, exports.

## 6. Non-Functional Requirements

| Category | Requirement |
|---|---|
| Throughput | Target single-instance ingestion ≥ 100K logs/sec at benchmark (Phase 6); 10K/50K checkpoints measured and reported |
| Latency | Log visible in search ≤ 2s at 10K logs/sec steady state |
| Query | P95 search first-page ≤ 1s over 24h/100M-log datasets (measured, not claimed) |
| Resource | Ingestion path memory bounded by configured queue/batch limits; documented ceilings |
| Availability | Graceful shutdown drains queues; readiness reflects storage dependency |
| Durability | No data loss while storage is healthy; drops only under sustained overload and always counted |
| Security | See `security.md`; OWASP baseline; escaped backend queries |
| Observability | Structured JSON logs; `/health` `/ready` `/metrics`; per-stage ingestion metrics |
| Compatibility | API versioned (`/api/v1`); OpenAPI 3.x published at `docs/openapi.yaml` |

## 7. Constraints & Assumptions

- Go ≥ 1.25 for backend; TypeScript + React + Vite + Tailwind for frontend.
- VictoriaLogs single-node in Phase 1; storage engine behind an interface.
- Time is stored UTC; UI renders in user-selected timezone.
- Logs may contain arbitrary fields; schema-on-read, not schema-on-write.
- The service must run as non-root; privileged ports (<1024) handled via
  deployment config (setcap or container port mapping).

## 8. Quality Gates (per phase, before marking complete)

1. `go test ./...` and frontend type-check/lint pass.
2. Definition-of-done walkthrough executed manually for the phase's features.
3. Load/perf numbers published in `docs/performance.md` from actual runs.
4. Docs updated to reflect as-built behavior.

## 9. Risks

| Risk | Mitigation |
|---|---|
| VictoriaLogs behavior leaks into the app | Adapter owns all LogsQL generation; conformance tests against the interface |
| Unbounded memory under load spikes | Bounded queues everywhere; drop policy + metrics; load tests at 1–4× target |
| Query injection via user input | Central query compiler with strict escaping; no string concatenation in handlers |
| Docker-in-dev friction | Compose file is the single bring-up path; CI validates it |

## 10. Dependencies

- VictoriaLogs (pinned version) — storage.
- Go module dependencies limited to: HTTP router, structured logging (slog),
  config (viper or koanf), syslog parsing (hand-written in-house, see
  `architecture.md` §9), bcrypt/argon2.
- Frontend: React 19, TanStack Query, TanStack Table, Recharts, Tailwind,
  react-router, a headless component primitive set (e.g. Radix).

## 11. Definition of Done — full list

1. Search the log. 2. Filter by hostname. 3. Filter by severity. 4. Filter by
facility. 5. Custom time range. 6. Dynamic fields visible. 7. Log detail view.
8. Log volume over time. 9. Ingestion rate. 10. Live tail. 11. Export.
12. Save a search. 13. System health. 14. Ingestion metrics.
