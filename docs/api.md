# API Design

Date: 2026-09-14 · Status: Phase 3 as-built (design baseline below)
REST-first, versioned (`/api/v1`), OpenAPI 3.x published at `docs/openapi.yaml`
(validated in CI). JSON in/out; errors use one shape.

## 1. Conventions

- Auth: `Authorization: Bearer <session-token>` for all `/api/v1/*` except
  `/auth/login`, `/health`, `/ready`, `/metrics` (and `/ingest` unless
  `ingestion.http.require_auth` is set).
- Time: all timestamps RFC3339 UTC; ranges are `start`/`end` (inclusive/exclusive).
- Limits: search `limit` ≤ 1000 (default 100), `offset` ≤ 100 000; export
  ≤ 10 000 rows per request; query string ≤ 8 KiB; volume ≤ 2000 buckets.
- Errors (stable codes, HTTP status + machine code + human message):
```json
{ "error": { "code": "invalid_query", "message": "…" } }
```
  Codes: `bad_request`, `unauthorized`, `forbidden`, `invalid_query`,
  `storage_unavailable`, `storage_timeout`, `internal`.
- Query syntax (`q` parameter): the LogsQL subset — phrases,
  `field:value`, `field:!=value`, `field:~"regex"`, `field:>n`/`>=`/`<`/`<=`,
  `field:*`, AND/OR/NOT, parentheses. The server parses and **recompiles**
  every query before it reaches storage; escaping is central and verified
  (see `security.md`). Values needing quotes are double-quoted with `\"`/`\\`
  escapes.

## 2. Endpoints — as built (Phases 1–3)

| Method | Path | Auth | Description |
|---|---|---|---|
| GET | `/health` | – | liveness |
| GET | `/ready` | – | readiness (storage, listeners, queue) |
| GET | `/metrics` | – | Prometheus text |
| POST | `/api/v1/auth/login` | – | `{username,password}` → `{token, expires_at, user}` |
| POST | `/api/v1/auth/logout` | ✓ | invalidate session |
| GET | `/api/v1/auth/me` | ✓ | current user |
| GET | `/api/v1/system/info` | ✓ | version |
| POST | `/api/v1/ingest` | opt | single/array/NDJSON ingest → `{accepted, rejected}` |
| GET | `/api/v1/logs/search` | ✓ | `q,start,end,limit,offset` → `{logs, next_offset?}` (newest first) |
| GET | `/api/v1/logs/count` | ✓ | `q,start,end` → `{count}` |
| GET | `/api/v1/logs/volume` | ✓ | `+bucket=5m\|1h\|1d` → `{bucket, buckets[]}` zero-filled |
| GET | `/api/v1/logs/export` | ✓ | `+format=ndjson\|json\|csv` → streamed download |
| GET | `/api/v1/fields` | ✓ | field names + row counts |
| GET | `/api/v1/fields/{field}/values` | ✓ | top values + counts (facet source) |

### Phase 3 design deviations (documented)

- **Offset pagination, not cursors** (§5 below): VictoriaLogs `sort by
  (_time desc) | offset N | limit M` supports deep paging adequately for
  the Phase 4 UI; cursor search-after lands with Live Tail (Phase 5).
- **`/logs/count` + `/logs/volume`** replace the designed `/logs/stats`;
  grouped facet tables come from `/fields/{field}/values`, so the dashboard
  needs are covered without a separate stats grammar.
- Export caps 10 000 rows/request (chunked client-side for larger).
- `/facets`, `/dashboard/*`, `/sources`, `/saved-searches`, `/logs/tail`
  (WebSocket) are Phase 5+ — see the baseline below.

## 2.1 Design baseline — endpoints (future phases)

### Auth
| Method | Path | Description |
|---|---|---|
| POST | `/api/v1/auth/login` | `{username,password}` → `{token, expires_at, user}` |
| POST | `/api/v1/auth/logout` | invalidate session |
| GET | `/api/v1/auth/me` | current user + roles/permissions |

### Logs
| Method | Path | Description |
|---|---|---|
| POST | `/api/v1/ingest` | single/array/NDJSON ingest → `{accepted, rejected{reason:count}}` |
| GET | `/api/v1/logs/search` | search (§4) |
| GET | `/api/v1/logs/stats` | volume/series for charts (§6) |
| GET | `/api/v1/logs/export` | streaming export `format=json|csv|ndjson` (§7) |
| GET | `/api/v1/logs/tail` | WebSocket upgrade (§8) |

### Fields & facets
| Method | Path | Description |
|---|---|---|
| GET | `/api/v1/fields` | field names + types/usage hints |
| GET | `/api/v1/fields/{field}/values` | top values + counts (facet), `filter` prefix search |
| GET | `/api/v1/facets` | multi-field facet snapshot for explorer sidebar |

### Sources
| Method | Path | Description |
|---|---|---|
| GET | `/api/v1/sources` | list configured sources + runtime status |
| POST | `/api/v1/sources` | create |
| GET/PATCH/DELETE | `/api/v1/sources/{id}` | read/update/delete |
| POST | `/api/v1/sources/{id}/test` | bind-check without persisting |
| POST | `/api/v1/sources/{id}/enable` · `/disable` | runtime toggle |

### Dashboard & analytics
| Method | Path | Description |
|---|---|---|
| GET | `/api/v1/dashboard/overview` | totals: logs, logs/sec, today, errors, sources, storage-used |
| GET | `/api/v1/dashboard/volume` | time-bucketed counts for area chart |

### Saved searches
| Method | Path | Description |
|---|---|---|
| GET/POST | `/api/v1/saved-searches` | list/create |
| GET/PATCH/DELETE | `/api/v1/saved-searches/{id}` | manage |

### System
| Method | Path | Description |
|---|---|---|
| GET | `/api/v1/system/info` | version, uptime, storage engine, config digest |
| GET | `/api/v1/system/health` | component health detail (for System → Health page) |
| GET/PUT | `/api/v1/system/retention` | retention days get/set |

### Meta
| Method | Path | Description |
|---|---|---|
| GET | `/health` | liveness (process up) |
| GET | `/ready` | readiness (storage ping, listeners, queue saturation) |
| GET | `/metrics` | Prometheus text |
| GET | `/api/v1/openapi.yaml` | served OpenAPI document |

## 3. RBAC Matrix (enforced in middleware via permission table)

| Permission | Viewer | Operator | Admin |
|---|---|---|---|
| logs:read, logs:search, fields:read | ✓ | ✓ | ✓ |
| logs:export | – | ✓ | ✓ |
| tail:connect | ✓ | ✓ | ✓ |
| sources:manage | – | ✓ | ✓ |
| searches:manage (own) | ✓ | ✓ | ✓ |
| searches:manage (all) | – | – | ✓ |
| retention:manage, users:manage, system:read-config | – | – | ✓ |

## 4. `GET /api/v1/logs/search`

```
?start=2026-09-14T10:00:00Z        (required, RFC3339)
&end=2026-09-14T12:00:00Z          (required)
&query=severity:error AND hostname:fw01   (compiled LogsQL; optional)
&limit=100&cursor=eyJ0aW1lc3RhbXAiOi…   (opaque, returned by API)
&fields=timestamp,hostname,severity_name,message   (projection; default standard set)
&sort=desc                        (desc|asc)
```
Response:
```json
{
  "data": [ {…LogEntry…} ],
  "meta": {
    "returned": 100, "has_more": true,
    "next_cursor": "…",
    "took_ms": 42,
    "stats": { "total_matched_estimate": 152342 }
  }
}
```

## 5. Cursor Model

Opaque base64 JSON: `{timestamp, entry_offset_within_ms}`. Ties broken by
VictoriaLogs `_time` + result position (VL search-after semantics). Cursors
expire after `query.cursor_ttl` (default 5 min); re-querying with a stale
cursor returns `bad_request` and the UI silently restarts from the top of the
range. Deep paging capped by `query.max_result_window` (default 10_000 rows).

## 6. `GET /api/v1/logs/stats`

```
?start&end&query   (same as search)
&metric=count&step=auto|30s|1m|5m|1h
&group_by=severity_name|hostname|facility_name|format|source_ip   (optional, multi)
```
Response: series `[{t, v}]` and/or grouped tables `[{key, count}]` — sized for
charts, computed by the backend hits/stats APIs (never by fetching rows).

## 7. Export

Same params as search + `format`. HTTP 200 `Content-Type: text/csv` /
`application/x-ndjson` / `application/json`, streamed in chunks
(`Transfer-Encoding: chunked`), server caps 50k rows/request; larger exports
are time-sliced client-side. Audit event logged with query + row count.

## 8. Live Tail (WebSocket)

`GET /api/v1/logs/tail?query=…` → `101 Switching Protocols`.
Frames (server→client): `{type:"log", entry:LogEntry}`,
`{type:"stats", rate:1234, buffered:42}`, `{type:"error", code}`,
`{type:"ping", ts}` (≤ every 30s). Client→server: `{type:"pause"|"resume"|"update_query", query?}`,
`{type:"pong"}`. Server enforces per-conn buffered cap (default 1000) — overflow
drops oldest with a `stats` notice. Idle timeout 10 min (client reconnects).

## 9. API Metrics

`http_requests_total{method,path_template,status}`, `http_request_duration_seconds{path_template}`,
`api_errors_total{code}`, `active_queries`, `query_duration_seconds`,
`query_result_rows`, `active_ws_connections`, `export_rows_total`, plus auth
counters (`auth_logins_total`, `auth_failures_total`) and rate-limit hits.
