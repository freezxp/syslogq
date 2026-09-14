# Architecture — syslogq

Date: 2026-09-14 · Status: Phase 0 baseline. Supersedes nothing.

## 1. Overview

A three-tier log analytics platform: **ingest tier** (syslog UDP/TCP/TLS + HTTP
JSON), a **Go application tier** (API + query + real-time), and a **storage tier**
(VictoriaLogs now, ClickHouse later). All storage access flows through one
interface so the engine is swappable. Every UI capability is backed by a
versioned HTTP API.

```text
                         ┌──────────────┐
                         │   Web UI     │  React + TS, dark-first
                         └──────┬───────┘
                                │ HTTPS, REST + WebSocket
              ┌─────────────────┼──────────────────┐
              │                 │                  │
     ┌────────▼────────┐ ┌──────▼───────┐ ┌────────▼────────┐
     │ syslog UDP :514 │ │ syslog TCP   │ │ HTTP /api/v1    │
     │ syslog TLS:6514 │ │ TLS :6514    │ │ /ingest         │
     └────────┬────────┘ └──────┬───────┘ └────────┬────────┘
              │                 │                  │
              └────────────┬────┴──────────────────┘
                           │  parse → normalize → enrich
              ┌────────────▼─────────────┐
              │   Ingestion Pipeline      │ bounded queues, batcher, workers
              └────────────┬─────────────┘
                           │
              ┌────────────▼─────────────┐
              │   Go Application Tier     │
              │  ┌─────────┐ ┌─────────┐ │
              │  │ REST API │ │ WS tail │ │
              │  └────┬────┘ └────┬────┘ │
              │       └──────┬─────┘      │
              │   ┌──────────▼─────────┐  │
              │   │ Storage Abstraction │  │  storage.LogStorage
              │   └──────────┬─────────┘  │
              │  ┌────────┐ ┌┴─────────┐ ┌┴──────────┐
              │  │ metrics│ │ query    │ │ auth/audit│
              │  └────────┘ └──────────┘ └───────────┘
              └────────────┬─────────────┘
                           │
                    ┌──────▼───────┐        ┌─────────────┐
                    │ VictoriaLogs │  ADR-  │  ClickHouse │ (future, Phase 7,
                    │  :9428       │  0001  │  (adapter)  │  same interface)
                    └──────────────┘        └─────────────┘
```

## 2. Component Responsibilities

### 2.1 Ingestion (`internal/ingestion`)
- **Listeners**: UDP, TCP (with framing: octet-counted + non-transparent
  framing per RFC6587), TLS (same TCP stack + config'd certs). Each listener is
  started/stopped/reloaded independently — this is what Source Management drives.
- **Parser registry**: format sniffing (`format.Detect`) → parser. Phase 1:
  RFC3164, RFC5424, JSON. Registry entry points make CEF/LEEF/etc. additive.
- **Normalizer**: parser output → `model.LogEntry`. Enrichment: defaults
  (received_at, tenant), severity/facility name resolution, size guards.
- **Batcher/Writer**: channel-based bounded queue per destination; worker pool
  flushes on size, interval, or shutdown. Drop policy + Prometheus counters.
  Backpressure: when the queue is full, listeners apply the configured policy
  (`block`, `drop_newest`, `drop_oldest`) — default `drop_newest` with counters.

### 2.2 API (`internal/api`)
- REST handlers, versioned under `/api/v1`. Thin: validate → call service → map errors.
- No storage knowledge above the service layer; no LogsQL outside the VL adapter.
- WebSocket endpoint for live tail: server subscribes to new writes (and VL
  `/select/logsql/tail` for catch-up), pushes JSON frames, ping/pong keepalive.
- Middleware chain: request-id → structured access log → authn → authz (RBAC) →
  rate limit → recover → security headers → metrics.

### 2.3 Query (`internal/query`)
- **AST-first design**: UI filters compile into a vendor-neutral `query.Expr`
  AST. Backend compilers translate AST → LogsQL (today) or SQL (future).
  Advanced mode accepts raw LogsQL from the user, validated/quoted centrally.
- **Escaping is the compiler's job**: field names `[a-zA-Z0-9_.]`-validated;
  values quoted with backend-specific rules. No string concatenation anywhere else.

### 2.4 Storage abstraction (`internal/storage`)
```go
type LogStorage interface {
    WriteLogs(ctx context.Context, batch []model.LogEntry) error
    Query(ctx context.Context, q Query) (Page, error)          // cursor pagination
    Stats(ctx context.Context, q StatsQuery) (StatsResult, error) // hit counts / series
    Facet(ctx context.Context, q FacetQuery) ([]FacetValue, error)
    FieldNames(ctx context.Context, q FieldQuery) ([]FieldInfo, error)
    FieldValues(ctx context.Context, q FieldValueQuery) ([]string, error)
    Tail(ctx context.Context, q TailQuery) (<-chan model.LogEntry, error)
    Health(ctx context.Context) error
    Retention(ctx context.Context, days int) error
}
```
- Errors are typed (`ErrBackendUnavailable`, `ErrQueryTimeout`, …) and mapped to
  stable API error codes.
- `internal/storage/victorialogs` is the only package that knows LogsQL/HTTP params.

### 2.5 Metrics & health (`internal/metrics`, `internal/health`)
- Prometheus registry; ingestion/parser/storage/query/HTTP metrics per
  `requirements.md` §FR and `ingestion.md` §8.
- `/health` (process liveness), `/ready` (storage ping + listeners up + queue
  saturation below threshold). Kubernetes-ready semantics.

### 2.6 Auth & audit (`internal/auth`)
- Session tokens (opaque, stored server-side in SQLite for Phase 1 — see ADR-0004),
  bcrypt password hashes, RBAC roles Admin/Operator/Viewer enforced by middleware
  with a permission table, not ad-hoc checks. Audit events to a dedicated table
  and structured log stream.

### 2.7 Config (`internal/config`)
- Load order: defaults → YAML file → env vars → CLI flags (later wins).
- Validated at boot; listeners/sources are config-reloadable via SIGHUP or API.

## 3. Request Flows

**Ingest (UDP syslog):**
`listener.read → decode → format.Detect → parser.Parse → normalize → queue →
batcher → vl.WriteLogs → ack accounting (counters only; UDP has no acks)`

**Search (UI):**
`GET /api/v1/logs/search → authz → validate(time range, cursor, limit≤1000) →
query.Service: AST→LogsQL → storage.Query → map Page → JSON`
(also emits query-latency histogram)

**Live tail:**
`WS /api/v1/logs/tail?query=… → auth → subscribe(new-writes fanout + VL tail) → frames`
Server-side fanout exists so tail shows writes from *any* API instance; the VL
`/tail` endpoint covers the multi-instance case.

## 4. Data & Scaling Model

- Phase 1: single Go process + single VictoriaLogs node; horizontal scale-out is
  stateless by design (sessions in shared store, tail fanout via VL).
- Hot path allocates zero per message where practical: pooled buffers, ring
  queues, one bulk write per batch (target batch 500–5000 entries / ≤8MB, tuned
  in Phase 6 benchmarks).
- Memory ceilings are config values (`ingestion.queue_capacity`, `query.max_result_window`),
  not implicit defaults.
- Multi-tenancy hook: `LogEntry.TenantID` populated from auth/ingest token;
  Phase 1 always `default`. Storage adapter injects tenant as VL account
  (`/insert/jsonline?account_id=…`) so Phase 7 needs no re-ingest.

## 5. Failure Modes & Reliability

| Failure | Behavior |
|---|---|
| Storage slow/down | Queues fill → drop policy with counters; `/ready` degrades; writes retry with bounded backoff; no busy-loop |
| Storage process crash after ack | At-least-once; duplicates possible — `entry_id` (hash) allows client-side dedup in Phase 7 |
| Parser error | Counted by format; raw line optionally stored with `format=unknown` |
| API process crash | Stateless except queues (loss of buffered-in-flight logs accepted & counted at shutdown drain) |
| Graceful shutdown | Stop listeners → drain queues with timeout → flush batches → close WS → exit 0 |

## 6. Security Architecture (summary — see `security.md`)

TLS everywhere on the wire (syslog TLS + HTTPS), bcrypt auth, RBAC middleware,
central query escaping, rate limits + max query duration/result caps, audit
log, secure headers, non-root container, secret values only via env/file refs.

## 7. Observability of the Platform Itself

- Structured JSON logs via `slog`; request-id propagation into storage spans.
- Prometheus metrics listed in `ingestion.md` §8 and `api.md` §9.
- `/health`, `/ready`, `/metrics`, `/api/v1/system/info`.

## 8. Repository Layout

```text
syslogq/
├── backend/
│   ├── cmd/syslogq/            # main: compose, run, signal handling
│   ├── cmd/loggen/             # synthetic load generator (see testing.md)
│   ├── internal/
│   │   ├── api/                # handlers, middleware, ws, router
│   │   ├── auth/               # users, sessions, rbac, audit
│   │   ├── config/             # yaml/env/flags + validation
│   │   ├── ingestion/          # listeners, pipeline, batcher, registry
│   │   ├── parser/             # rfc3164, rfc5424, json, detect, registry
│   │   ├── normalization/      # → model.LogEntry
│   │   ├── model/              # LogEntry, field types
│   │   ├── query/              # AST, compiler interface, limits
│   │   ├── storage/            # LogStorage iface, errors, memory (tests)
│   │   │   └── victorialogs/   # VL adapter (only LogsQL-aware package)
│   │   ├── metrics/            # prometheus collectors
│   │   ├── health/             # health/readiness
│   │   └── system/             # version, build info
│   └── tests/                  # integration + load tests
├── frontend/
│   ├── src/
│   │   ├── app/                # router, providers, layout
│   │   ├── components/         # ui primitives + domain components
│   │   ├── features/           # logs/, dashboard/, sources/, searches/, settings/
│   │   ├── api/                # typed http client (generated from openapi)
│   │   ├── query/              # query builder state, AST↔URL serialization
│   │   └── hooks/
│   └── e2e/                    # playwright smoke tests
├── deploy/
│   ├── docker/                 # Dockerfiles
│   └── kubernetes/             # Phase 7 placeholder (kustomize/helm later)
├── docs/                       # this directory + openapi.yaml (Phase 3)
├── docker-compose.yml
└── Makefile
```

## 9. Key Technology Choices (each has an ADR)

| Concern | Choice | ADR |
|---|---|---|
| Storage engine (Phase 1) | VictoriaLogs behind `LogStorage` | 0001 |
| Monorepo layout + Go/React | Single repo, `backend/` + `frontend/` | 0002 |
| API versioning/pagination/streaming | `/api/v1`, cursor pages, chunked export | 0003 |
| Auth | Opaque sessions in SQLite, bcrypt | 0004 |
| Real-time transport | WebSocket (tail), SSE only if a need appears | 0005 |
| SPA delivery | Frontend embedded in the Go binary (`go:embed`), same-origin | 0006 |
| Syslog parsing | Hand-written parsers (no cgo dep), RFC3164+5424+6587 | — |
| Config | koanf (YAML+env+flags) | — |
| HTTP | stdlib `net/http` (Phase 1: health/ready/metrics); chi evaluated at Phase 3 REST build-out; slog; prometheus client | — |

## 10. Explicit Non-Goals (Phase 1)

No clustering, no ClickHouse adapter code, no CEF/OTel parsers, no alerting,
no AI features, no multi-tenant enforcement. All are extension points, not
skeletons-with-stubs.
