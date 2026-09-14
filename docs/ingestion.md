# Ingestion Pipeline

Date: 2026-09-14 · Status: Phase 1 as-built (RFC3164/5424 parsers, RFC6587
framing, UDP/TCP/TLS listeners, bounded queues, batcher, writer pool; HTTP
ingest arrives Phase 2). Design of the log path from network to storage.
Priorities: reliability, bounded memory, observability, format extensibility.

## 1. Pipeline Stages

```text
listener ──► decoder ──► detect ──► parser ──► normalizer ──► [bounded queue]
   UDP/TCP/TLS/HTTP         │          │            │             │
                            ▼          ▼            ▼             ▼
                       parse counters      LogEntry        batcher ──► worker pool
                        (per format)    (model.go)      (size/time)      │
                                                                    storage.WriteLogs
```

1. **Listener** (`internal/ingestion/listen`): owns the socket lifecycle.
   - UDP: read datagrams; one datagram = one message (RFC3164 tradition; RFC5424
     over UDP = one datagram per message).
   - TCP: RFC6587 framing — supports both octet-counting (`123 <msg>`) and
     non-transparent framing (newline/NULL). Max message size enforced.
   - TLS: TCP + `tls.Config` from config (cert/key/CA, min TLS 1.2).
   - HTTP: `/api/v1/ingest` in the API server, fed through the same pipeline.
2. **Detect** (`internal/parser`): first bytes decide the format.
   Order: RFC5424 (`<PRI>1 `) → RFC3164 (`<PRI>` + date heuristic) → JSON
   (`{`/`[` with valid prefix) → unknown. Detection is cheap and never throws.
3. **Parse** → parser-specific result (keeps raw). Parsers are pure functions,
   fully unit-tested with RFC examples + hostile inputs.
4. **Normalize** → `model.LogEntry` (see `log-data-model.md`), enrichment,
   size guards, reserved-key protection.
5. **Queue** — per-pipeline bounded channel (default cap 50_000 entries;
   `ingestion.queue_capacity`). Full-queue policy per source config:
   `drop_newest` (default) | `drop_oldest` | `block` (TCP/HTTP only; UDP would
   just lose at the socket). Every drop increments counters with reason label.
6. **Batcher** — accumulates until `batch.max_entries` (default 1000),
   `batch.max_bytes` (default 4 MiB), or `batch.flush_interval` (default 500ms),
   whichever first. Flush also on queue-idle timeout to bound latency.
7. **Writer pool** — N workers (default = min(8, CPU/2)) calling
   `storage.WriteLogs` with bounded retry: 3 attempts, exp. backoff, jitter;
   persistent failure → batch dropped after drain deadline, counted.

## 2. Backpressure & Resource Bounds

| Knob | Default | Purpose |
|---|---|---|
| `ingestion.queue_capacity` | 50_000 | hard ceiling of in-flight entries |
| `ingestion.batch.max_entries/bytes/flush_interval` | 1000 / 4MiB / 500ms | write efficiency vs latency |
| `ingestion.workers` | 8 | parallel storage writes |
| `ingestion.max_message_bytes` | 256 KiB | oversized datagrams dropped+counted |
| `ingestion.active_connections` | 1000 | TCP/TLS conn limit, overflow rejected |
| HTTP ingest limits | 1000 entries / 8 MiB per request | 413 on breach |

A slow backend manifests as queue depth ↑, flush interval ↑, drops ↑ — all
metrics below. Nothing grows without a configured ceiling.

## 3. Graceful Shutdown

`SIGTERM/SIGINT` → stop listeners (stop reading) → stop batcher timers → drain
queue with `shutdown_drain_timeout` (default 5s) → flush partial batches →
writer retries → close storage → exit 0. Anything undrained is counted as
`dropped{reason="shutdown"}` and logged at warn with counts.

## 4. Parser Registry (extensibility)

```go
type Parser interface {
    Name() string                       // "rfc5424"
    Parse(raw []byte, meta ParseMeta) (Parsed, error)
}
// Registry: ordered slice; Detect picks first parser whose Try returns ok.
```
Adding CEF/LEEF/OTel = new package + `registry.Register`. No changes to
listeners, normalizer, or storage. Registry order and enable-list are config.

## 5. Source Configuration (drives UI "Sources")

```yaml
ingestion:
  sources:
    - id: syslog-udp-514
      type: syslog_udp            # syslog_udp | syslog_tcp | syslog_tls | http_json
      enabled: true
      address: ":514"
      parse: [rfc5424, rfc3164]   # detection order
      queue_policy: drop_newest
    - id: syslog-tls-6514
      type: syslog_tls
      address: ":6514"
      tls: {cert_file: …, key_file: …, ca_file: …}
```
Runtime reload: add/update/delete sources without process restart (Phase 5);
Phase 1 starts with boot-time config + SIGHUP reload.

## 6. HTTP JSON Ingestion

`POST /api/v1/ingest` — auth optional per config (`ingestion.http.require_auth`,
default false for syslog-forwarder ergonomics; recommend token auth in prod).
Bodies: single object, array, or NDJSON. Content sniffed by first byte.
Each entry: timestamps parsed (RFC3339; missing → now), unknown keys → fields,
max limits per §2. Response: `{"accepted": n, "rejected": m}` + per-reason
counts (never detailed error leakage).

## 7. Delivery Semantics

At-least-once while storage is healthy. We ack (counters) after VL returns 2xx;
VL buffers to disk, so acknowledged data survives a VL crash. Duplicates are
accepted (idempotency via `ID` hash is a Phase 7 enhancement).

## 8. Metrics (Prometheus, namespace `syslogq_`)

Ingestion: `messages_received_total{source,protocol}`,
`bytes_received_total{source,protocol}`,
`messages_parsed_total{source,format}`, `messages_parse_errors_total{source,format}`,
`messages_unknown_format_total{source}`, `messages_stored_total{source}`,
`messages_dropped_total{source,reason}`, `bytes_stored_total`,
`queue_depth{source}`, `queue_capacity{source}`, `batch_flush_seconds`,
`batch_size_entries`, `storage_write_seconds`, `storage_write_errors_total`,
`active_connections{source}`, `logs_per_second` (rate derived),
`bytes_per_second` (rate derived).
Parser health: the `messages_parsed_total{format}` vector powers the
format-distribution chart.

## 9. Structured Logging

Every stage logs with `slog`: parse failures at debug (sampled 1/1000 at info),
drops at warn (always with counts), flushes at debug. Include `source_id`,
`remote_ip` (truncated), `msg_len`. No log payloads in application logs
(secrets/pii hygiene) unless `debug.capture_raw: true` (dev only).
