# ADR-0003: API versioning, cursor pagination, and streaming export

Status: Accepted · Date: 2026-09-14 · Supersedes: none

## Context
Every UI capability must be API-first (`requirements.md` §1.7). Log search
returns time-ordered, potentially huge result sets; offset pagination
degrades linearly and is hostile to VL's storage access patterns. Exports
can be tens of thousands of rows and must not be materialized in memory.

## Decision
1. **Versioned paths** `/api/v1/*`. Breaking changes mint `/api/v2/…` with
   an overlap window; additive changes stay in v1. The version is a path
   prefix only (no header negotiation).
2. **Cursor pagination** for all log-list endpoints: opaque base64 cursors
   encoding `{timestamp, tie-breaker}`, TTL'd (5 min default), with a
   `max_result_window` (10_000 rows) deep-paging cap. No OFFSET paging over
   logs anywhere. Cursor semantics defined in `api.md` §5.
3. **Chunked streaming export** (`Transfer-Encoding: chunked`) for
   JSON/CSV/NDJSON with a 50_000-row per-request cap; larger exports are
   time-sliced by the client. Never buffered fully server-side.
4. Errors use one machine-readable envelope (code + message + request_id,
   `api.md` §1) from day one so clients can switch versions programmatically.

## Consequences
**Positive:** stable client contract; pagination cost is O(page) not
O(offset+page); export memory bounded regardless of row count; UI deep links
shareable and bookmark-stable.

**Negative:** cursors are backend-semantic and expire — clients must handle
`bad_request` on stale cursor by restarting from range top (documented,
UI does this silently); streaming responses complicate error reporting
mid-stream (mitigated: errors before first byte are normal JSON errors;
mid-stream failures terminate the chunk with a trailing error record).

**Neutral:** facet/stats endpoints return bounded result sets by design and
need no pagination.

## Alternatives
- **Offset/limit pagination** — rejected: O(n) deep pages, encourages
  "scrape everything" clients, fights the storage engine.
- **GraphQL** — rejected: single consumer (our SPA), adds complexity
  without a second consumer; REST+OpenAPI gives generated types anyway.
- **SSE for export** — rejected: plain chunked HTTP is simpler, cacheable,
  and trivially consumable via curl.
