package normalization

import (
	"log/slog"
	"sort"
	"strings"

	"github.com/freezxp/syslogq/internal/model"
	"github.com/freezxp/syslogq/internal/parser"
)

// Size guards from docs/log-data-model.md §2.
const (
	DefaultMaxMessageBytes = 64 << 10 // 64 KiB
	DefaultMaxFieldCount   = 100
	DefaultMaxFieldValue   = 4 << 10   // 4 KiB
	DefaultMaxEntryBytes   = 256 << 10 // 256 KiB
)

// reservedKeys maps the standard LogEntry JSON keys; parser output may never
// overwrite them (extras go to Fields, which is where they already are).
var reservedKeys = map[string]bool{
	"id": true, "timestamp": true, "received_at": true, "message": true,
	"hostname": true, "source_ip": true, "source_port": true,
	"facility": true, "facility_name": true, "severity": true,
	"severity_name": true, "priority": true, "protocol": true,
	"format": true, "app_name": true, "process_id": true,
	"message_id": true, "source_id": true, "source_type": true,
	"tenant_id": true, "fields": true, "labels": true, "raw_message": true,
}

type Normalizer struct {
	MaxMessageBytes int
	MaxFieldCount   int
	MaxFieldValue   int
	MaxEntryBytes   int
	Log             *slog.Logger
}

func New() *Normalizer {
	return &Normalizer{
		MaxMessageBytes: DefaultMaxMessageBytes,
		MaxFieldCount:   DefaultMaxFieldCount,
		MaxFieldValue:   DefaultMaxFieldValue,
		MaxEntryBytes:   DefaultMaxEntryBytes,
		Log:             slog.Default(),
	}
}

// Normalize maps a parser result to model.LogEntry, applying field-key
// hygiene, size guards, and severity/facility name resolution.
func (n *Normalizer) Normalize(p parser.Parsed, meta parser.Meta) model.LogEntry {
	e := model.LogEntry{
		ReceivedAt: meta.ReceivedAt.UTC(),
		Hostname:   p.Hostname,
		SourceIP:   meta.RemoteIP,
		SourcePort: meta.RemotePort,
		Protocol:   meta.Protocol,
		Format:     p.Format,
		AppName:    p.AppName,
		ProcessID:  p.ProcessID,
		MessageID:  p.MessageID,
		SourceID:   meta.SourceID,
		SourceType: meta.SourceType,
		TenantID:   model.TenantDefault,
		Message:    p.Message,
		RawMessage: string(p.Raw),
	}
	if p.Timestamp.IsZero() {
		e.Timestamp = e.ReceivedAt
	} else {
		e.Timestamp = p.Timestamp.UTC()
	}
	if p.Facility != nil {
		fac := *p.Facility
		e.Facility = &fac
		e.FacilityName = model.FacilityName(fac)
	}
	if p.Severity != nil {
		sev := *p.Severity
		e.Severity = &sev
		if p.Format == parser.FormatRFC3164 {
			e.SeverityName = model.SeverityNameRFC3164(sev)
		} else {
			e.SeverityName = model.SeverityNameRFC5424(sev)
		}
	}
	if e.Facility != nil && e.Severity != nil {
		pri := *e.Facility*8 + *e.Severity
		e.Priority = &pri
	}

	n.applyFields(&e, p.Fields)
	n.enforceSize(&e)
	return e
}

func (n *Normalizer) applyFields(e *model.LogEntry, fields map[string]string) {
	if len(fields) == 0 {
		return
	}
	count := 0
	for k, v := range fields {
		key := normalizeKey(k)
		if key == "" || reservedKeys[key] {
			n.Log.Warn("dropping unusable field key", "field", k, "source_id", e.SourceID)
			continue
		}
		if len(v) > n.MaxFieldValue {
			v = v[:n.MaxFieldValue]
		}
		if e.Fields == nil {
			e.Fields = make(map[string]string, len(fields))
		}
		if _, exists := e.Fields[key]; exists {
			// Distinct raw keys can normalize to the same key; later wins.
			n.Log.Warn("field key collision, later value wins", "field", key, "source_id", e.SourceID)
		}
		e.Fields[key] = v
		count++
		if count >= n.MaxFieldCount {
			n.Log.Warn("field count cap reached, dropping rest", "cap", n.MaxFieldCount, "source_id", e.SourceID)
			break
		}
	}
}

func normalizeKey(k string) string {
	k = strings.TrimSpace(strings.ToLower(k))
	if k == "" {
		return ""
	}
	// Keep the documented field charset: [a-z0-9_.]; anything else is
	// substituted so dynamic field names stay query-friendly.
	var b strings.Builder
	for _, r := range k {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '_', r == '.':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	return b.String()
}

// enforceSize sheds data, in order of least value, until the entry fits:
// raw message first, then fields (deterministically), then message tail.
func (n *Normalizer) enforceSize(e *model.LogEntry) {
	if len(e.Message) > n.MaxMessageBytes {
		e.Message = e.Message[:n.MaxMessageBytes]
		if e.Fields == nil {
			e.Fields = make(map[string]string, 1)
		}
		e.Fields["truncated"] = "true"
	}
	for entrySize(e) > n.MaxEntryBytes {
		switch {
		case e.RawMessage != "":
			budget := n.MaxEntryBytes - len(e.Message) - 1024
			if budget < 0 {
				budget = 0
			}
			if budget < len(e.RawMessage) {
				e.RawMessage = e.RawMessage[:budget]
				e.Fields["truncated"] = "true"
				continue
			}
			e.RawMessage = ""
		case len(e.Fields) > 0:
			keys := make([]string, 0, len(e.Fields))
			for k := range e.Fields {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			delete(e.Fields, keys[0])
		default:
			// Message alone exceeds the cap; hard-truncate.
			if len(e.Message) > n.MaxEntryBytes {
				e.Message = e.Message[:n.MaxEntryBytes]
			}
			return
		}
	}
}

func entrySize(e *model.LogEntry) int {
	size := len(e.Message) + len(e.RawMessage) + len(e.Hostname) + 256
	for k, v := range e.Fields {
		size += len(k) + len(v)
	}
	return size
}
