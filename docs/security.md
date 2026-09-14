# Security Design

Date: 2026-09-14 · Status: Phase 0 baseline
Security is a product requirement (SOC/NOC use case), not a hardening pass.
This document defines the threat model, controls, and the security section of
the Definition of Done. Implementation detail lives in the owning docs
(`api.md`, `ingestion.md`, `architecture.md`).

## 1. Threat Model (STRIDE-lite, scoped to syslogq)

| # | Threat | Vector | Primary controls |
|---|--------|--------|------------------|
| T1 | Unauthorized log access | Logs contain credentials, PII, network topology | AuthN (bcrypt + server-side sessions), RBAC middleware, TLS |
| T2 | Query injection | User input compiled into backend queries (LogsQL now, SQL later) | Central compiler, strict escaping, AST re-validation |
| T3 | Log data forgery / spoofed source | Syslog has no sender authentication | Source IP capture, TLS + mutual-TLS option, per-source config |
| T4 | DoS via ingest | UDP flood, TCP connection flood, huge payloads | Bounded queues, max message size, conn limits, rate limits |
| T5 | DoS via query | Wide time ranges, high-cardinality facets, export abuse | Max query duration, result window caps, export row caps, rate limits |
| T6 | Credential attacks | Login brute force, session theft/fixation | bcrypt cost, login rate limit + lockout, token entropy, expiry, Secure/HttpOnly/SameSite cookies |
| T7 | Privilege escalation | Admin-only endpoints reached by lower roles | Permission-table RBAC middleware on every route (deny by default) |
| T8 | Secret leakage | Secrets in config files, logs, error messages | Env/file-only secret loading, no payloads in app logs, generic API errors |
| T9 | Supply chain | Malicious Go/npm deps, poisoned images | Pinned versions, `go.sum`/lockfile commits, minimal images, Dependabot/CI audit |
| T10 | Container/OS escape surface | Overprivileged container | Non-root user, read-only FS where possible, no extra capabilities |

Trust boundaries: (a) network → listeners (untrusted by default; TLS peers
semi-trusted), (b) HTTP client → API (untrusted until session validated),
(c) API/storage → VictoriaLogs (trusted, internal network).

## 2. Transport Security

- **HTTPS for the web/API**: served when TLS config present (`api.tls`); plain
  HTTP allowed only when `api.tls.allow_insecure` explicitly opts in (dev).
  TLS 1.2 minimum, modern cipher suites, HTTP/1.1+H2.
- **Syslog TLS** (`syslog_tls` sources): TLS 1.2+, server cert required;
  optional client-cert (mutual TLS) per source (`tls.client_ca_file`) so
  known forwarders can be authenticated (mitigates T3 for willing peers).
- Plain UDP/TCP syslog is accepted (protocol reality) but treated as
  untrusted input: never parsed with trust, always attributed with
  `source_ip`.
- WebSocket (`/api/v1/logs/tail`) inherits the API's TLS; token passed as
  query param is **forbidden** (leaks into logs) — authenticate via the
  first client frame or `Sec-WebSocket-Protocol` header, then drop it.
- HSTS when TLS on; internal redirects never downgrade.

## 3. Authentication & Session Management

- Passwords: bcrypt (cost ≥ 12). No plaintext at rest, no reversible hashes.
  Minimum length policy; no composition theater.
- Sessions: opaque 256-bit random tokens (base64url), stored server-side
  (SQLite, ADR-0004) with user, created_at, last_seen, expires_at, and
  origin fingerprint (UA hash + optional IP pin when enabled).
  Default TTL 12h idle / 24h absolute (config).
- Cookie-based for the SPA: `HttpOnly`, `Secure` (when TLS), `SameSite=Lax`,
  `Path=/`; the token also works as `Authorization: Bearer` for API clients.
- Login protection: per-IP and per-account rate limit
  (`auth.login_rate` default 10/min), exponential lockout backoff after 5
  failures, generic error message, failures audited + counted
  (`auth_failures_total`).
- Logout invalidates server-side session (not just cookie clearing).
  Session listing + forced logout is an Admin UI capability (Phase 5).
- First boot: bootstrap admin from `SYSLOGQ_ADMIN_PASSWORD` env (or generated
  and printed once to stderr); force documented password change flow later
  (Phase 5) — Phase 1 documents the env requirement.

## 4. Authorization (RBAC)

- Roles: Admin / Operator / Viewer with a **permission table**
  (`map[Role]map[Permission]bool`) enforced by middleware — no scattered
  `if role == …` checks (see matrix in `api.md` §3).
- Every `/api/v1/*` route declares a required permission; default-deny for
  anything undeclared (router wiring fails closed at startup if a route has
  no permission).
- Object-level: saved searches are owner-scoped (Viewer/Operator manage own;
  Admin manages all). Log data itself is not row-filtered in Phase 1
  (single-tenant; `tenant_id` reserved for Phase 7).

## 5. Input Handling & Query Injection (T2 — highest engineering risk)

- **One compiler**: UI filters → `query.Expr` AST → backend compiler
  (`internal/query`). Raw LogsQL (Advanced mode) is parsed/validated server-
  side. No handler, service, or UI code builds query strings by
  concatenation. Lint rule + review gate.
- Field names validated against `^[a-zA-Z0-9_.]+$` before compilation;
  values escaped with backend-specific rules (quoting, backslash escaping,
  regex inputs validated or rejected).
- All HTTP input validated at the boundary (limits, types, ranges) before
  entering services; parse errors → `bad_request`, never a panic.
- Ingest JSON: depth-limited parsing, entry/size caps (§ FR limits), unknown
  keys → string fields (never evaluated).
- Syslog payloads are data, never executed; parsers are total functions over
  bytes with no regexes vulnerable to backtracking blowups (tested with
  hostile inputs).
- Path-style params (`/sources/{id}`, `/fields/{field}`) validated against
  allow-lists/patterns; template routing prevents traversal.

## 6. Resource Limits & Abuse Controls

| Control | Default | Threat |
|---|---|---|
| `api.rate_limit.requests_per_min` | 600 (auth), separate ingest + login buckets | T4/T5 |
| `query.max_duration_ms` | 10_000 | T5 |
| `query.max_result_window` | 10_000 rows | T5 |
| search `limit` ≤ 1000, export ≤ 50_000 rows/request (streamed) | — | T5 |
| `ingestion.max_message_bytes` | 256 KiB | T4 |
| `ingestion.active_connections` | 1000 | T4 |
| HTTP body cap on `/ingest` | 8 MiB | T4 |
| WS per-connection buffer | 1000 entries (drop-oldest + notice) | T4 |
| Request header/URL read timeouts on all listeners | configured | T4 |

## 7. Secrets Management

- Secrets (DB is not one; TLS keys, admin bootstrap password, future ingest
  tokens) enter only via **env vars or file paths** referenced in YAML —
  never inline in config files, never in flags (visible in `ps`).
- `.gitignore` guards `*.env`, `certs/`, `data/`, config-local overrides.
- App logs never contain payloads or secrets (`ingestion.md` §9); error
  responses use stable codes + generic messages; stack traces go to logs,
  not to clients.
- Docker images contain no secrets; compose reads env from `.env` (untracked)
  with a tracked `.env.example`.

## 8. Audit Trail

Immutable-append audit table (SQLite) + structured log stream for:
login/logout (success + failure), user/role changes, source CRUD,
retention changes, exports (with query + row count), config reloads.
Admin UI surfaces it in Phase 5. Audit records include actor, action,
target, request_id, timestamp, and source IP. No log-payload content in
audit records.

## 9. HTTP & Container Hardening

- Security headers on all API/UI responses: `X-Content-Type-Options:
  nosniff`, `Referrer-Policy: no-referrer`, `Content-Security-Policy`
  (SPA: `default-src 'self'`, no inline scripts after build), `X-Frame-Options: DENY`
  (or CSP `frame-ancestors 'none'`), cache-control no-store on API responses.
- Slowloris defenses: `ReadHeaderTimeout`, idle/read/write timeouts on HTTP
  and TCP listeners; graceful connection caps.
- Container: distroless/minimal base, non-root UID (fixed, e.g. 10001),
  read-only root filesystem + tmpfs where feasible, no `--privileged`,
  dropped capabilities, pinned image digests.
- Exposed surface by default: HTTP API (8080), syslog UDP/TCP 514, TLS 6514,
  Prometheus `/metrics` **bound to localhost / internal interface by
  default**; enable external exposure explicitly.

## 10. OWASP Top-10 (2021) Mapping

| OWASP risk | Coverage here |
|---|---|
| A01 Broken access control | §4 RBAC default-deny, permission table, object-level checks |
| A02 Cryptographic failures | §2 TLS everywhere, §3 bcrypt, §7 secrets handling |
| A03 Injection | §5 central query compiler + escaping, input validation |
| A04 Insecure design | §1 threat model; bounded-resource design (`ingestion.md`) |
| A05 Security misconfiguration | §9 headers, non-root container, secure defaults, deny-first routes |
| A06 Vulnerable components | §9 supply chain: pinned deps, lockfiles, CI audit job |
| A07 Auth failures | §3 session design, lockout, entropy, rate limits |
| A08 Data integrity failures | TLS, pinned digests, immutable audit append, `go sum` verification |
| A09 Logging/monitoring failures | §8 audit, structured logs, metrics on auth + drops |
| A10 SSRF | No outbound request surface driven by user input in Phase 1 (no webhooks/alerts); document gate before adding any |

## 11. Security Verification (how we prove it)

- **Unit**: escaping/injection corpus tests (hostile LogsQL/SQL fragments),
  parser fuzzing (go-fuzz corpus), auth middleware matrix (role × route ×
  expected status), session expiry/fingerprint tests.
- **Integration**: unauthenticated/route-table scan asserting 401/403 for
  every protected route; rate-limit behavior; oversized payload rejection;
  TLS-only cookie flags.
- **CI**: `gosec` (severity ≥ medium fails), `govulncheck`, `npm audit`
  (high+), dependency pinning checks, Dockerfile lint (hadolint).
- **Manual review gate**: any PR touching `internal/query`, `internal/auth`,
  or query compilation requires review with the injection checklist (§5).
- Phase 6 adds an external abuse-profile load test (connection floods,
  oversized datagrams) verifying drops are counted and the process stays
  healthy.

## 12. Known Accepted Risks (Phase 1)

- Plain UDP/TCP syslog is unauthenticated by protocol design (T3 partially
  accepted; source IP + TLS options mitigate where peers cooperate).
- Single VictoriaLogs node: availability, not confidentiality, risk;
  mitigated by ops runbook (`deployment.md`).
- `require_auth=false` on HTTP ingest is possible for forwarder ergonomics —
  an explicit opt-in, documented as prod-unsafe.
- No log-data row-level access control (single-tenant Phase 1).
