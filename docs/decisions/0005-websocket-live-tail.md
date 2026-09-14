# ADR-0005: WebSocket for live tail

Status: Accepted · Date: 2026-09-14 · Supersedes: none

## Context
Live Tail (`/logs/live`) needs a server→client push channel with per-conn
flow control (pause/resume, buffered-record caps) and client→server control
messages (query update). HTTP API is otherwise plain REST.

## Decision
Use **one WebSocket endpoint** (`GET /api/v1/logs/tail`) with JSON frames
(protocol in `api.md` §8): server→client `log`/`stats`/`error`/`ping`,
client→server `pause`/`resume`/`update_query`/`pong`. Authentication
reuses the session (token via first frame or `Sec-WebSocket-Protocol`,
never a URL query param — `security.md` §2). Server-side fanout subscribes
to the write path plus VL's tail endpoint for catch-up/multi-instance.

## Consequences
**Positive:** bidirectional control on one connection (pause/resume/query
update without reconnect); browser-native API with auto reconnect handled
in our client; single auth model shared with REST.

**Negative:** WebSocket complicates load balancers/proxies (upgrade
headers, idle timeouts — documented in `deployment.md` §6); JSON frames are
not the most compact (accepted: tail is UI-scoped, capped at ~1000 buffered
entries); server must implement ping/pong keepalive and idle teardown.

**Neutral:** frame protocol is versioned by a `v` field on connect, so
evolution doesn't need a new endpoint.

## Alternatives
- **SSE** — rejected as primary: unidirectional (pause/query-update would
  need a second REST channel + coordination); kept as a fallback idea if a
  proxy Wall blocks WS in some deployment (would be a new ADR).
- **HTTP/2 server push** — rejected: dead browser support.
- **Polling `/logs/search` with cursor** — rejected: 2s visibility
  requirement and rate budgets make polling wasteful; no clean pause
  semantics.
