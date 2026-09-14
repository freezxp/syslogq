# Testing Strategy

Date: 2026-09-14 · Status: Phase 0 baseline
Principle: the test suite must make the risky things safe — parsers over
hostile bytes, query compilation over injection, backpressure under overload
— and CI must run everything a laptop can run. Performance numbers come only
from Phase 6 methodology (§8), never from CI timings.

## 1. Test Pyramid

```text
        ┌─────────────┐  E2E (Playwright): login→ingest→search→tail→export
        │   manual    │  phase walkthroughs (Definition-of-Done items)
       ┌┴─────────────┴┐ Integration: API + real VictoriaLogs (docker),
       │               │ compose smoke, WS tail, auth flows
      ┌┴───────────────┴┐ Unit: parsers, normalizer, batcher, query AST/
      │                 │ compiler, RBAC matrix, config, cursor codec
     └┴─────────────────┴┘ Fuzz: parsers, JSON ingest (corpus in repo)
```

Fast feedback order: unit (<10s) → integration (<5min, needs docker) →
e2e smoke (<10min) → load/perf (manual or nightly, Phase 6).

## 2. Unit Tests (per package)

| Package | What is tested | Notable cases |
|---|---|---|
| `parser/rfc3164` | every RFC example + kitchen-sink corpus | missing PRI, year-2038/old dates, weird HOSTNAME, NILVALUE, midnight wrap, 64 KiB messages, non-UTF8 bytes |
| `parser/rfc5424` | RFC examples incl. SD-ELEMENTS edge cases | nested/escaped SD params `[brackets]`, unicode, empty MSG, `1 ` version variants, malformed SD |
| `parser/json` | single/array/NDJSON, key mapping | timestamp variants (RFC3339 + epoch), unknown keys→fields, numeric severity, reserved-key collisions, depth bombs |
| `parser/detect` | format sniffing order | ambiguity corpus (JSON that starts `{` but isn't, RFC3164 without year) |
| `normalization` | → `LogEntry` mapping | reserved-key protection, field caps (100/4 KiB), truncation flag, severity/facility naming, collision merge policy |
| `ingestion` (batcher/queue) | flush triggers, drop policies, shutdown drain | size/time/idle triggers, drop_newest/drop_oldest/block, drain timeout drops counted |
| `query` (AST/compiler) | AST→LogsQL, round-trip visual↔advanced | **injection corpus** (§4), escaping of quotes/backslashes/regex chars, field-name validation |
| `storage` (memory fake) | contract tests baseline | full `LogStorage` semantics without VL |
| `auth` | bcrypt verify, session create/expiry/fingerprint, permission table | expired token, wrong fingerprint, default-deny route table |
| `api` handlers | via memory storage + httptest | param validation, error shapes (`api.md` §1), cursor codec, rate limit |
| `config` | precedence defaults<yaml<env<flags | invalid config fails fast with field path |

Parser corpora live in `testdata/` as line-delimited `input\tpexpected-json`
files so new hostile samples are one-line additions. Every parser test
asserts **no panic** on hostile input (parse error is fine; crash is a bug).

## 3. Integration Tests (`backend/tests/`)

Run against real components via `docker compose -f tests/compose.test.yml`
(CI service containers; local `make test-integration`):

- **VL round-trip**: write via `WriteLogs` → query/facet/stats/tail through
  the adapter; asserts the `log-data-model.md` §6 mapping both directions.
- **Auth flow**: login → me → protected route 200 → logout → 401; wrong
  password 401 + audit row; rate-limit 429 after N failures.
- **Route table scan**: enumerate every registered route × (no token,
  viewer, operator, admin) → assert exact status per RBAC matrix
  (`api.md` §3). This test reads the router registration so new routes
  can't skip declarations (fails-closed, `security.md` §4).
- **HTTP ingest**: single/array/NDJSON; 413 on oversize; malformed counts;
  unknown format stored when configured.
- **WebSocket tail**: subscribe → ingest → frame received; pause/resume;
  buffer overflow drops oldest + `stats` notice.
- **Export streaming**: JSON/CSV/NDJSON byte-exact golden files, row cap.
- **Compose smoke** (the Phase 1 exit gate): `docker compose up -d` →
  `/ready` → `logger` UDP round-trip → search via API → down. Runs in CI on
  every PR (this is the "Docker-in-dev friction" mitigation from
  `requirements.md` §9).

## 4. Injection Corpus (`query` + integration)

Checked-in list of hostile inputs run against the compiler and the live
search endpoint; assert output is either a rejected `bad_request` or a
compiled query that treats the input as a **literal value**:

```
value with "quotes"            value_with\\backslash
field:"a) OR severity:debug    * | stats count() by x
${jndi:ldap://x}               '; DROP TABLE logs;--
(a]                             _time:{bad…range
```

Also property tests: for random byte-strings as values, compiled LogsQL
round-trips through VL search returning exactly the literal. SQL-grammar
inputs stay in the corpus now so the Phase 7 ClickHouse compiler inherits
the suite unchanged.

## 5. Fuzzing

`go-fuzz`/native fuzz targets for: RFC3164 parse, RFC5424 parse, JSON ingest
prefix sniffing, cursor codec. Seed corpus from `testdata/`. Runs: locally
on demand (`make fuzz TIME=10m`) and a short CI fuzz (30s/target) to catch
regressions; crashers auto-filed as failing tests with the input committed.

## 6. Frontend Tests

- **Unit (vitest)**: query builder AST↔URL codec (shareable-link contract),
  visual↔advanced mode conversion, table virtualization math, time-picker
  presets → concrete instants.
- **Component**: explorer table interactions (facet click → filter chip),
  detail drawer per-value actions, live-tail controls.
- **Type drift**: `openapi-typescript` regen in CI — diff against checked-in
  types fails on drift (`frontend.md` §1).
- **E2E (Playwright, chromium)**: the smoke path — login → ingest (seeded)
  → search → open detail → tail receives a live entry → export downloads.
  Runs against the compose stack; one browser, no visual assertions beyond
  smoke (keeps CI stable).
- Manual walkthrough remains the gate for UX items (`roadmap.md` Phase 4
  exit 1–14) — automation covers regressions, not judgment.

## 7. CI Pipeline (`.github/workflows/ci.yml`)

| Job | Runs on | Steps |
|---|---|---|
| backend | PR+push | `go vet`, `staticcheck`, `gofmt -l`, `go test ./... -race -count=1` |
| security | PR+push | `gosec` (≥medium fails), `govulncheck`, injection corpus (part of unit), dependency pinning check |
| frontend | PR+push | `pnpm i --frozen-lockfile`, `tsc --noEmit`, eslint, vitest, bundle-size budget (350 KB gzip) |
| integration | PR+push | services: docker (VL) → `go test ./tests/... -tags=integration` |
| compose-smoke | PR+push (main only for speed) | build images → compose up → ready-wait → logger round-trip → search assert → down |
| e2e | main + release | compose stack + playwright |
| fuzz | nightly | 10m/target |
| docs | PR | markdown lint, internal doc link check, OpenAPI lint (Phase 3+) |

Rules: race detector always on for backend tests; no `t.Skip` without an
issue link; test logs never committed as fixtures.

## 8. Load & Performance Methodology (Phase 6 gate)

- Tool: `cmd/loggen` — RFC3164/5424/JSON mix, randomized fields
  (realistic cardinality), UDP/TCP/HTTP transports, rate control, client-
  side drop accounting.
- Matrix: 1K / 10K / 50K / 100K logs/sec × 30 min each, on the reference
  host (specs recorded in the report), storage pre-loaded to 100M rows for
  query-latency measurement.
- Metrics captured: sustained throughput, CPU/RSS (process + container),
  parse p99, storage write p99, end-to-end visibility latency (ingest
  timestamp → searchable), query p95/p99 (typical dashboard queries) under
  load, dropped count (**must be 0** at sustained target or explained),
  queue depth over time (must not grow unboundedly).
- Abuse profile (from `security.md` §11): connection floods, oversized
  datagrams, hostile parse inputs at rate — asserts health stays green and
  drops are counted.
- Output: `docs/performance.md` with methodology, environment, raw numbers,
  and the tuning changes made as a result (batch size, workers, queue
  capacity). **No performance claim in any doc cites an unmeasured number**
  (`requirements.md` §8.3).

## 9. Manual Phase Walkthroughs

Each phase exit runs its Definition-of-Done checklist in the real app
(`requirements.md` §11 for the full list). Result recorded in the phase
summary (what passed, what was fixed, what was deferred). Screenshots for
UI phases. This is deliberately human — it's where "the feature works but
the product is wrong" gets caught.
