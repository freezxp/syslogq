# Log Data Model

Date: 2026-09-14 · Status: Phase 0 baseline
Defines the single normalized internal representation: `model.LogEntry`.
Schema-on-read: any field may exist; nothing requires predefined columns.

## 1. `LogEntry` (Go)

```go
package model

type LogEntry struct {
    // Identity / time
    ID         string    // content hash for dedup (Phase 7); optional
    Timestamp  time.Time // event time (from message), UTC
    ReceivedAt time.Time // ingest time at our listener, UTC

    // Core content
    Message    string            // normalized message text

    // Network origin
    Hostname   string            // reported host (syslog HOSTNAME / json host)
    SourceIP   string            // peer IP (when known)
    SourcePort int               // peer port (when known)

    // Syslog classification
    Facility      *int           // 0–23, nil if unknown
    FacilityName  string         // e.g. "local4"
    Severity      *int           // 0–7, nil if unknown
    SeverityName  string         // e.g. "warning"
    Priority      *int           // facility*8 + severity

    // Origin classification
    Protocol    string           // "udp" | "tcp" | "tls" | "http"
    Format      string           // "rfc3164" | "rfc5424" | "json" | "unknown"
    AppName     string           // syslog APP-NAME / json service
    ProcessID   string           // syslog PROCID
    MessageID   string           // syslog MSGID

    // Platform extension points
    SourceID    string           // configured source (listener) id, e.g. "syslog-udp-514"
    SourceType  string           // e.g. "syslog" | "http-json"
    TenantID    string           // Phase 1: "default"

    // Arbitrary data (schema-on-read)
    Fields      map[string]string // extracted dynamic fields (all string-valued at rest)
    Labels      map[string]string // operator-added labels (NOT for parsed content)

    // Raw preservation (always stored, UI-togglable)
    RawMessage  string
}
```

JSON wire shape (snake_case, omitempty for absent optional fields):
```json
{
  "timestamp": "2026-09-14T14:30:00Z",
  "received_at": "2026-09-14T14:30:00.041Z",
  "hostname": "fw01",
  "source_ip": "10.10.1.1",
  "source_port": 41022,
  "facility": 20, "facility_name": "local4",
  "severity": 4, "severity_name": "warning",
  "priority": 164,
  "protocol": "udp",
  "format": "rfc5424",
  "app_name": "vpn", "process_id": "", "message_id": "",
  "source_id": "syslog-udp-514", "source_type": "syslog",
  "tenant_id": "default",
  "message": "VPN tunnel disconnected",
  "fields": { "vendor": "fortinet", "device_type": "firewall", "vpn_name": "HQ-VPN", "interface": "wan1", "policy_id": "1234" },
  "labels": {},
  "raw_message": "<165>1 2026-09-14T14:30:00Z fw01 vpn - - - VPN tunnel disconnected"
}
```

## 2. Field Conventions

| Rule | Detail |
|---|---|
| Case | Field keys snake_case; parsers lowercase unknown keys (first-writer wins within a message) |
| Values | Everything in `Fields` is a string at rest; numeric comparisons happen in the backend (VL compares typed values natively) |
| Reserved keys | The standard set above is reserved; parser output never overwrites standard keys, extras go to `fields` |
| Collisions | Two parsers produce `fields.vendor` — later merge wins, first occurrence logged (sampled) |
| Size guards | `message` ≤ 64 KiB (truncated, `fields.truncated=true`), `fields` ≤ 100 entries / 4 KiB per key, entry ≤ 256 KiB total |
| Time | Always UTC internally; presentation converts to viewer timezone |

## 3. Well-known dynamic fields (conventions, not schema)

Documented so UI can offer friendly labels when present:
`device_type`, `vendor`, `interface`, `policy_id`, `vpn_name`, `user`,
`http_status`, `request_uri`, `src_ip`, `dst_ip`, `src_port`, `dst_port`,
`protocol_name`, `action`, `session_id`.

## 4. Severity & Facility Enumerations

Severity (RFC5424): 0 emergency, 1 alert, 2 critical, 3 error, 4 warning,
5 notice, 6 info, 7 debug. RFC3164 maps: 0 panic, 1 alert, 2 crit, 3 err,
4 warning, 5 notice, 6 info, 7 debug.
Facility: standard RFC5424 names (kern, user, mail, daemon, auth, syslog, lpr,
news, uucp, cron, authpriv, ftp, ntp, security, console, local0–local7…).

## 5. Examples

### 5.1 Syslog RFC5424 → LogEntry
Input: `<165>1 2026-09-14T14:30:00Z fw01 vpn 1234 ID47 [example vendor="fortinet" policy_id="1234"] VPN tunnel disconnected`
→ `severity=5? no: 165 = facility 20 (local4), severity 5 (notice)` — parser
computes severity/priority; SD params merge into `fields` (`vendor`,
`policy_id`); `app_name=vpn`, `process_id=1234`, `message_id=ID47`.

### 5.2 JSON ingest → LogEntry
Input: `{"timestamp":"2026-09-14T14:30:00Z","host":"server01","level":"error","service":"nginx","message":"connection refused","source_ip":"10.10.10.20"}`
→ standard keys mapped (`host→hostname`, `level→severity_name`, `service→app_name`),
remaining keys → `fields.source_ip=10.10.10.20`. Numeric `severity` also accepted.

### 5.3 Unparseable input
→ `format="unknown"`, `message=<raw line>`, `severity/facility=nil`, raw
preserved; parser error counter incremented. Optionally stored per config
(`ingestion.store_unknown: true`, default true).

## 6. Storage Mapping (VictoriaLogs adapter)

| LogEntry | VictoriaLogs |
|---|---|
| Timestamp | `_time` |
| Message | `_msg` |
| all standard keys | same-name fields |
| Fields (map) | flattened same-name fields |
| Labels | `labels.*` prefix (queryable, excluded from facets by default) |
| TenantID | VL `account_id` (enforced adapter-side) |
| RawMessage | `raw_message` field |

The adapter owns this mapping and its inverse (result row → `LogEntry`).
No other package may import VL client types.
