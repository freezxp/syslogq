# Storage Backend Comparison — ClickHouse vs VictoriaLogs

Date: 2026-09-14 · Status: **DECIDED — VictoriaLogs for Phase 1** (ADR-0001)
Evaluation dimensions required by the project brief, scored for *a log-analytics
workload* (append-heavy, time-ordered, high-cardinality fields, ad-hoc filtering).

## 1. Summary Matrix

| Dimension | VictoriaLogs | ClickHouse | Winner for logs |
|---|---|---|---|
| Ingestion performance | Very high; purpose-built for logs, native syslog/HTTP ingest | Very high; columnar batch inserts | Tie (both ≫ requirements) |
| Query performance (log patterns: time range + filters + facets) | Excellent on log access paths | Excellent on aggregations; log-style full-row retrieval needs careful schema | VL for exploration |
| Full-text search | Built-in ( LogsQL `*` filters, word tokens ) | Token-based indexes exist but second-class; often paired with external search | VL |
| Structured fields | Native: arbitrary `field:value`, schema-on-read | Requires `Map`/JSON columns or predefined columns for efficiency | VL |
| High-cardinality fields | First-class (per-field value indexes) | Pain point; needs `LowCardinality`/projection tuning | VL |
| Storage efficiency / compression | Purpose-built compression for log data (~10–30× typical) | Industry-leading columnar compression (ZSTD) | CH slightly, both excellent |
| Time-range queries | Native `_time` filtering, primary sort | `ORDER BY (timestamp, …)` required at table design | VL (zero schema work) |
| Aggregations | StatsQL/hits APIs for dashboards | Best-in-class SQL aggregations, materialized views | CH |
| Retention | Built-in per-account/partition TTL | `TTL` clauses on tables | Tie |
| Horizontal scalability | VictoriaCluster (open source) | Sharding/replication mature, proven at scale | CH |
| Operational complexity | One small binary, almost no tuning | Powerful but many knobs; schema design expertise needed | VL by far |
| Backup/restore | Snapshots of data dir; vmbackup tooling | `BACKUP`/`RESTORE` SQL, clickhouse-backup ecosystem | CH |
| Resource consumption | Very low baseline RAM/CPU per GB | Higher baseline; sizing matters | VL |
| Deployment ease | Single container, ~zero config usable | Single container but schema/bootstrap required | VL |
| Query arbitrary fields | LogsQL: any field immediately | Only if captured in schema/Map at ingest | VL |
| Long-term scalability | Cluster mode scales to PB-scale logs | Proven to exabyte scale | CH (proven) |
| Query language ergonomics for logs | LogsQL designed for logs | SQL powerful but verbose for exploration | VL |

## 2. Analysis

### 2.1 VictoriaLogs
Purpose-built log storage. Every requirement in the brief — LogsQL, field
discovery APIs (`/select/logsql/field_names`, `field_values`, `facets`),
hits/stats endpoints for charts, live-tail endpoint, arbitrary fields without
predefined schema — ships as native HTTP APIs. This collapses the amount of
backend code we must write and more importantly removes schema-management
code paths entirely: new fields appear in the UI with zero migrations.

Weaknesses: aggregation expressiveness is narrower than SQL; ecosystem and
operational track record are younger than ClickHouse; cluster mode is newer.
LogsQL generation must be centrally escaped (same injection discipline as SQL).

### 2.2 ClickHouse
Best-in-class columnar OLAP engine; unmatched aggregations and proven
horizontal scale. But a log platform on ClickHouse forces up-front design
decisions: which fields get real columns vs `Map(String,String)` (which is
slow for filtering), how to handle high-cardinality attributes, tokenizer setup
for full-text, and per-workload `ORDER BY` tuning. That is significant schema
engineering and ongoing tuning cost in Phase 1, for capabilities (deep OLAP
aggregations) the log UI barely uses in early phases.

## 3. Decision

**Phase 1 storage: VictoriaLogs**, single-node, behind the `storage.LogStorage`
interface (see `architecture.md`). The interface is deliberately shaped around
capabilities both engines can provide (query/stats/fields/tail/write), so a
ClickHouse adapter can be added in Phase 7 without touching API or UI code.
No LogsQL strings may appear outside the VictoriaLogs adapter package —
enforced by code review and a lint rule.

### Migration triggers (revisit the decision when any becomes true)
- Dashboards require rich cross-dimensional OLAP beyond StatsQL.
- Clustering needs exceed VictoriaCluster maturity.
- Retention/backup compliance requirements outgrow VL tooling.
- Measured VictoriaLogs query latency misses FR targets on real data.
