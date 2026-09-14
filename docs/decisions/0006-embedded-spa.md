# ADR-0006: Embed the SPA in the Go binary, served by the API process

Status: Accepted · Date: 2026-09-14 · Supersedes: none

## Context
Phase 1 ships as `docker compose up` with the fewest moving parts
(`requirements.md` §2). The frontend is a static SPA; options are serving it
from the Go process, a separate nginx container, or a CDN/object store.

## Decision
Build the SPA, embed it into the Go binary with `go:embed`, and serve it
from the same HTTP server as the API (same origin, same TLS, same port
8080). `cmd/syslogq` serves hashed assets with long cache headers and a
no-cache `index.html`; the embed is wired in `deploy/docker` build (a
`frontend/dist` stamp file makes `go build` fail with a clear message if
the SPA wasn't built).

## Consequences
**Positive:** one process, one port, one TLS config — the compose file
stays at two services total (syslogq + VictoriaLogs); same-origin removes
CORS entirely; deploys are one artifact; version skew between UI and API
becomes impossible.

**Negative:** UI change requires rebuilding the Go image (mitigated: it's
one docker build, and dev workflow uses Vite's dev server proxying to a
running backend — no rebuild in the loop); binary size grows by the bundle
(~1–2 MB, within budget); can't CDN-scale the static assets (irrelevant for
Phase 1 scale).

**Neutral:** the API remains API-first: the SPA is a client of `/api/v1`,
never granted special in-process privileges; external reverse-proxy
deployments (`deployment.md` §6) still work unchanged.

## Alternatives
- **Separate nginx container serving the SPA** — rejected: third service,
  second TLS/purge config, CORS or dual-origin cookie complexity, more
  compose surface — all for zero Phase 1 benefit.
- **CDN/object-store hosting** — rejected: out-of-band release process,
  version-skew risk, needs public assets or signed URLs — an anti-goal for
  an on-prem NOC product.
