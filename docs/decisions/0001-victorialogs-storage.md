# ADR-0001: VictoriaLogs as the Phase 1 storage engine

Status: Accepted · Date: 2026-09-14 · Supersedes: none

## Context
The platform needs append-heavy, time-ordered storage with ad-hoc filtering,
high-cardinality fields, schema-on-read dynamic fields, facets, live tail,
and retention. Candidates: VictoriaLogs and ClickHouse (full comparison in
`docs/storage-comparison.md`). Phase 1 has no clustering requirement.

## Decision
Adopt **VictoriaLogs (single-node)** as the storage engine, accessed only
through the `storage.LogStorage` interface defined in `architecture.md` §2.4.
`internal/storage/victorialogs` is the only package permitted to know LogsQL
or VL HTTP endpoints — enforced by review and a lint rule. LogsQL strings
appearing anywhere else is an architectural violation.

## Consequences
**Positive:** zero schema management (new fields appear with no migrations);
native field/facet/stats/tail APIs map 1:1 to our API surface; very low
operational footprint (one small binary); retention built in.

**Negative:** aggregation expressiveness narrower than SQL; younger
ecosystem; vendor coupling contained but real (query compilation shaped by
LogsQL semantics).

**Neutral:** ClickHouse remains a Phase 7 candidate behind the same
interface; a conformance test suite over `LogStorage` is the gate for any
future adapter.

## Alternatives
- **ClickHouse** — rejected for Phase 1: forces up-front schema engineering
  (columns vs Map, ORDER BY, tokenizers) for capabilities early phases don't
  use. Revisit on the migration triggers in `storage-comparison.md` §3.
- **Elasticsearch/OpenSearch** — rejected: resource footprint and ops cost
  disproportionate to a single-binary goal.
- **Loki** — rejected: label-first model fights schema-on-read dynamic
  fields.
