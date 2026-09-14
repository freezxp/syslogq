# Deployment & Operations

Date: 2026-09-14 · Status: Phase 0 baseline
Target: one-command bring-up via Docker Compose in Phase 1; single-node
VictoriaLogs; Kubernetes arrives in Phase 7 (directory prepared only).

## 1. Compose Topology (Phase 1)

```yaml
services:
  syslogq:            # Go app: API + UI + syslog listeners
    image: ghcr.io/freezxp/syslogq:latest        # or build: ./deploy/docker
    ports: ["8080:8080", "514:514/udp", "514:514/tcp", "6514:6514"]
    environment: [SYSLOGQ_CONFIG=/etc/syslogq/syslogq.yaml]
    volumes:
      - ./config/syslogq.yaml:/etc/syslogq/syslogq.yaml:ro
      - syslogq-data:/var/lib/syslogq             # SQLite (sessions, audit, saved searches)
    depends_on: [victorialogs]
    restart: unless-stopped
  victorialogs:
    image: victoriametrics/victorialogs:v1.0.0    # PINNED in compose; never :latest
    command: -storageDataPath=/data -retentionPeriod=30d -httpListenAddr=:9428
    volumes: [vl-data:/data]
    ports: ["127.0.0.1:9428:9428"]                # internal; never expose publicly
    restart: unless-stopped
volumes: { syslogq-data: {}, vl-data: {} }
```

Decisions baked in: VL is **not** exposed publicly (localhost bind) — all
access flows through syslogq's authenticated API; SQLite lives on a named
volume so sessions/audit survive restarts; both services have healthchecks
and `restart: unless-stopped`.

## 2. Ports & Privileged Ports

Syslog's canonical port 514 is <1024. Inside containers this is irrelevant
(container userland can bind); the mapping above publishes it on the host
where the **docker-proxy (root)** does the bind, so no setcap/capabilities
are needed. Bare-metal (no Docker) alternative documented:

```bash
setcap 'cap_net_bind_service=+ep' /usr/local/bin/syslogq
# or an systemd socket / iptables REDIRECT 514→1514 with listeners on :1514
```

Port map: 8080 HTTP API+UI · 514/udp+tcp syslog · 6514 syslog TLS ·
9428 VictoriaLogs (internal only) · `/metrics` on 8080 (bind-scoped, §
`security.md` §9).

## 3. Images & Build

- `deploy/docker/Dockerfile` (backend): multi-stage — `golang:1.25` build
  (vendor or module-cache mounts, `CGO_ENABLED=0`), distroless/static
  runtime, `USER 10001`, pinned digests, no shell.
- `deploy/docker/Dockerfile.frontend` → built artifacts embedded into the Go
  binary via `go:embed` (ADR-0006): one serving artifact, no nginx layer.
- Images tagged `vX.Y.Z` + git SHA; CI builds and pushes on tag; compose
  pins versions, never `latest` for VL.
- Update policy: VL upgrades go through the migration-trigger checklist
  (`storage-comparison.md` §3); syslogq upgrades are stateless (all state in
  SQLite volume + VL volume).

## 4. Configuration

- File at `/etc/syslogq/syslogq.yaml` (path overridable via `SYSLOGQ_CONFIG`
  or `-config` flag). Precedence: defaults < YAML < env (`SYSLOGQ_*`, `.`→`_`)
  < flags. Full schema in `config.example.yaml` (tracked; generated docs in
  Phase 1).
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
