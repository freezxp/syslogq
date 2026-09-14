# ADR-0004: Opaque server-side sessions in SQLite, bcrypt, permission-table RBAC

Status: Accepted · Date: 2026-09-14 · Supersedes: none

## Context
Phase 1 needs username/password auth for the SPA and API clients, three
roles (Admin/Operator/Viewer), and an audit trail — without standing up
Postgres or a Redis. Single syslogq instance in Phase 1; multi-instance is
a Phase 7 concern. Security requirements in `security.md` §3–§4.

## Decision
1. **Passwords**: bcrypt, cost ≥ 12. No account recovery in Phase 1 (admin
   reset via CLI flag `syslogq admin reset-password`).
2. **Sessions**: opaque 256-bit random tokens (base64url), stored server-
   side in **SQLite** (`sessions` table: token hash, user, created_at,
   last_seen, expires_at, fingerprint). The SPA uses an HttpOnly cookie;
   API clients use `Authorization: Bearer`. Token is stored **hashed** —
   DB compromise does not yield usable tokens.
3. **RBAC**: `map[Role]map[Permission]bool` permission table consulted by
   middleware; routes declare a required permission and fail closed at
   startup if undeclared (`security.md` §4).
4. **Audit**: append-only SQLite table for auth events, config changes,
   exports (`security.md` §8).
5. SQLite accessed with WAL mode, single-writer discipline via the app's
   serial audit/session repository.

## Consequences
**Positive:** zero external dependencies for auth; revocation is real
(server-side state — delete the row); works identically for cookie and
Bearer clients; permission table makes the RBAC matrix (`api.md` §3)
executable as a test (route-table scan, `testing.md` §3).

**Negative:** session lookups are a DB read per request (mitigated: in-
process LRU cache with 5s TTL invalidation on logout — correctness of
revocation within 5s accepted); SQLite is a second stateful thing to back
up (`deployment.md` §5); multi-instance deployment needs sticky sessions or
shared cache — explicitly deferred with the interface already isolating the
session store.

**Neutral:** JWT remains a candidate for Phase 7 multi-instance (stateless
verification), trading revocation; the API contract (Bearer token, expiry)
does not change, so the switch is internal.

## Alternatives
- **JWT access tokens** — rejected for Phase 1: revocation requires a
  denylist (reintroducing state) or accepting stale revocation; unnecessary
  complexity with one instance.
- **Redis session store** — rejected: another service to run/backup for
  no Phase-1 benefit.
- **Basic auth** — rejected: no logout, password on every request, ugly
  with browsers.
- **OAuth2/OIDC delegation** — deferred: enterprise feature; interface
  isolated enough to add an OIDC identity provider later.
