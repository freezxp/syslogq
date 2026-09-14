# Deployment & Operations

Date: 2026-09-14 · Status: Phase 4 as-built
Target: one-command bring-up via Docker Compose in Phase 1; single-node
VictoriaLogs; Kubernetes arrives in Phase 7 (directory prepared only).

## 1. Compose Topology (Phase 4, as built)

See `docker-compose.yml` at the repo root — the canonical file. Summary:

```yaml
services:
  syslogq:            # Go app: API + embedded web UI + syslog listeners
    build: {context: ., dockerfile: deploy/docker/Dockerfile}
    environment: [SYSLOGQ_STORAGE__URL=http://victorialogs:9428]
    ports: ["8080:8080", "514:5140/udp", "514:5140/tcp"]
    volumes: [syslogq-data:/var/lib/syslogq]     # auth DB (users/sessions)
    depends_on: [victorialogs]
    restart: unless-stopped
  victorialogs:
    image: victoriametrics/victoria-logs:v1.52.0   # PINNED; never :latest
    command: -storageDataPath=/storage -retentionPeriod=30d -httpListenAddr=:9428
    ports: ["127.0.0.1:9428:9428"]                 # dev stack only; never expose publicly
    volumes: [vl-data:/storage]
    restart: unless-stopped
```

As-built notes: the server binds unprivileged **5140 in-container** (the
image runs as a non-root user) and the compose file maps host 514 → 5140;
config is defaults + `SYSLOGQ_*` env overrides rather than a mounted file in
the dev stack. TLS source (6514) is defined in `config/syslogq.yaml` but
disabled by default. The image pre-creates `/var/lib/syslogq` owned by the
runtime user (UID 65532) so the non-root server can create its SQLite auth
DB; the `syslogq-data` volume persists it (seeded from the image with
ownership preserved on first mount).

VL is bound to localhost on the host: all external access flows through
syslogq's API; CI runs the full smoke (logger → syslogq → VL query) via
`make compose-smoke`.

## 2. Ports & Privileged Ports

Syslog's canonical port 514 is <1024. The container listens on unprivileged
**5140** (required: the image runs as a non-root user) and compose maps host
514 → 5140, where the **docker-proxy (root)** does the privileged bind, so no
setcap/capabilities are needed. Bare-metal (no Docker) alternative documented:

```bash
setcap 'cap_net_bind_service=+ep' /usr/local/bin/syslogq
# or a systemd socket / iptables REDIRECT 514→5140 with listeners on :5140
```

Port map: 8080 HTTP API+UI · 514→5140/udp+tcp syslog · 6514 syslog TLS ·
9428 VictoriaLogs (internal only) · `/metrics` on 8080 (bind-scoped, §
`security.md` §9).

## 3. Images & Build (as built)

- `deploy/docker/Dockerfile` (context: repo root): three stages —
  `node:24-alpine` builds the SPA (`npm ci && npm run build`), then
  `golang:1.27-alpine` builds the Go binary (`CGO_ENABLED=0`, `-trimpath`)
  with the SPA `go:embed`ded from the node stage output (ADR-0006), then
  distroless/static `nonroot` runtime (uid 65532), no shell, one
  `/syslogq` binary serving API + UI on 8080.
- Local builds: `make web` (SPA → `backend/internal/web/dist`) then
  `make build`; or `make docker-build` for the full container image.
- Images tagged `vX.Y.Z` + git SHA; CI builds and pushes on tag; compose
  pins versions, never `latest` for VL.
- Update policy: VL upgrades go through the migration-trigger checklist
  (`storage-comparison.md` §3); syslogq upgrades are stateless (all state in
  SQLite volume + VL volume).

## 4. Configuration

- File at `/etc/syslogq/syslogq.yaml` (path overridable via `SYSLOGQ_CONFIG`
  or `-config` flag). Precedence: defaults < YAML < env (`SYSLOGQ_*`,
  `__` for nesting). Full schema in `config/syslogq.yaml` (tracked example).
- Secrets only via env/file (`security.md` §7): `SYSLOGQ_ADMIN_PASSWORD`
  (first boot), TLS key/cert **paths** not values.
- Untracked `.env` + tracked `.env.example` for compose; `.gitignore` covers
  `*.env`, `certs/`, `data/`.
- Reload: `SIGHUP` or `POST /api/v1/system/reload` re-reads listener/source
  config (Phase 5 makes it hot; Phase 1 restarts listeners only).

## 5. Volumes, Data & Backup

| Volume | Contents | Backup |
|---|---|---|
| `vl-data` | all log data | VL `vmbackup`/snapshot of data dir; nightly cron → S3/NFS |
| `syslogq-data` | SQLite: sessions, users, audit, saved searches | `sqlite3 .backup` nightly (safe online) |

Restore drill (runbook, executed once in Phase 5 exit): stop ingest →
restore VL data dir (or vmrestore) → restore SQLite file → start → verify
counts via `/api/v1/dashboard/overview`. RPO/RTO are **user-configured by
cron frequency**, not claimed by the product.

## 6. TLS Termination

- API/UI TLS: terminate at syslogq itself (`api.tls` cert/key paths) — no
  external proxy required for the reference deployment; a reverse-proxy
  variant (nginx/caddy, HTTP→8080 upstream) documented for orgs that own
  PKI centrally. WebSocket upgrade headers included in the example config.
- Syslog TLS: cert/key (+ optional client CA for mTLS) mounted read-only
  into the container; certificate rotation = replace files + SIGHUP.

## 7. Health & Readiness (deployment semantics)

- `GET /health` → 200 while process serving (liveness probe).
- `GET /ready` → 200 only when: VL `Health()` ok, configured listeners
  bound, queue saturation < threshold (readiness probe; VL crash → not
  ready, but syslogq process stays alive and keeps counting).
- Compose healthchecks use `/health` + VL `/health`; CI smoke job runs
  compose up → wait ready → `logger` round-trip → compose down.

## 8. Resource Sizing (starting points, re-measured Phase 6)

| Scale | syslogq | VictoriaLogs | Disk |
|---|---|---|---|
| ≤ 1K logs/s | 0.5 CPU / 256 MiB | 0.5 CPU / 512 MiB | ~25 GB/30d at ~250 B/entry |
| ≤ 10K logs/s | 2 CPU / 1 GiB | 2 CPU / 2 GiB | ~250 GB/30d |
| ≤ 100K logs/s | TBD Phase 6 | TBD Phase 6 | measured |

These are deployment defaults for requests/limits — not performance claims
(`roadmap.md` gates all such numbers behind Phase 6).

## 9. Upgrades & Rollback

1. Pin compose versions; upgrade = change tag → `docker compose up -d`.
2. Order: VL first (backward-compatible storage), syslogq second.
3. Rollback: revert tag; SQLite is additive-migration-only (never destructive
   migrations without a documented export path).
4. Graceful shutdown drains queues (`ingestion.md` §3) — wait for exit 0;
   `stop_grace_period` in compose ≥ drain timeout (default 5s + margin → 30s).

## 10. Kubernetes (Phase 7 placeholder)

`deploy/kubernetes/` reserved. Design constraints now so the port is cheap:
stateless syslogq Deployment (N replicas behind a LB; sessions in shared
store or sticky-less via token auth), VL single StatefulSet with PVC (or
VictoriaCluster), ConfigMap for YAML, Secrets for certs/bootstrap. No
manifests written before Phase 7.

## 11. Post-Deployment Checklist (operator runbook)

```bash
docker compose up -d
curl -fs localhost:8080/ready                     # storage + listeners ok
docker compose exec syslogq syslogq -version      # record version
logger -n 127.0.0.1 -P 514 --udp "smoke test $(date -Is)"
# UI: login → Logs → search "smoke test" → verify visible ≤ 2s
curl -fs localhost:8080/metrics | grep syslogq_messages_stored_total
```

Then: change the bootstrap admin password (if generated), mount TLS certs
if prod, set retention days (`PUT /api/v1/system/retention`), verify
backup cron fired once. Production readiness sign-off = this checklist
executed and archived.
