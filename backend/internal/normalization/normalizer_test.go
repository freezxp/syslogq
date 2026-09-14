package normalization

import (
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/freezxp/syslogq/internal/model"
	"github.com/freezxp/syslogq/internal/parser"
)

var meta = parser.Meta{
	SourceID:   "src-1",
	SourceType: model.SourceTypeSyslog,
	Protocol:   "udp",
	RemoteIP:   "10.0.0.1",
	RemotePort: 40000,
	ReceivedAt: time.Date(2026, 9, 14, 15, 0, 0, 0, time.UTC),
}

func TestNormalizeRFC5424(t *testing.T) {
	fac, sev := 20, 5
	p := parser.Parsed{
		Format:    parser.FormatRFC5424,
		Timestamp: time.Date(2026, 9, 14, 14, 30, 0, 0, time.UTC),
		Hostname:  "fw01",
		AppName:   "vpn",
		Facility:  &fac,
		Severity:  &sev,
		Message:   "VPN tunnel disconnected",
		Fields:    map[string]string{"vendor": "fortinet"},
		Raw:       []byte("<165>1 ... raw ..."),
	}
	e := New().Normalize(p, meta)

	if e.Hostname != "fw01" || e.AppName != "vpn" {
		t.Errorf("mapping: %+v", e)
	}
	if e.Facility == nil || *e.Facility != 20 || e.FacilityName != "local4" {
		t.Errorf("facility: %+v", e)
	}
	if e.SeverityName != "notice" {
		t.Errorf("severity name (RFC5424 table): %q", e.SeverityName)
	}
	if e.Priority == nil || *e.Priority != 165 {
		t.Errorf("priority: %+v", e.Priority)
	}
	if e.Fields["vendor"] != "fortinet" {
		t.Errorf("fields: %v", e.Fields)
	}
	if e.TenantID != model.TenantDefault || e.SourceID != "src-1" || e.SourceType != "syslog" {
		t.Errorf("platform fields: %+v", e)
	}
	if e.Protocol != "udp" || e.SourceIP != "10.0.0.1" || e.SourcePort != 40000 {
		t.Errorf("origin: %+v", e)
	}
	if e.RawMessage != "<165>1 ... raw ..." {
		t.Errorf("raw: %q", e.RawMessage)
	}
}

func TestNormalizeDefaults(t *testing.T) {
	// No timestamp, no severity: received_at fills in, names stay empty.
	e := New().Normalize(parser.Parsed{Format: parser.FormatRFC3164, Message: "m", Raw: []byte("m")}, meta)
	if !e.Timestamp.Equal(meta.ReceivedAt) {
		t.Errorf("timestamp should default to received_at: %v", e.Timestamp)
	}
	if e.Severity != nil || e.Facility != nil || e.Priority != nil {
		t.Errorf("classification should be absent: %+v", e)
	}
	if e.SeverityName != "" || e.FacilityName != "" {
		t.Errorf("names should be empty")
	}
}

func TestNormalizeRFC3164SeverityNames(t *testing.T) {
	sev := 3
	e := New().Normalize(parser.Parsed{Format: parser.FormatRFC3164, Severity: &sev, Message: "m", Raw: []byte("m")}, meta)
	if e.SeverityName != "err" {
		t.Errorf("RFC3164 severity naming: %q", e.SeverityName)
	}
}

func TestNormalizeUnknownFormat(t *testing.T) {
	e := New().Normalize(parser.Parsed{Format: parser.FormatUnknown, Message: "garbage line", Raw: []byte("garbage line")}, meta)
	if e.Format != "unknown" || e.Message != "garbage line" || e.RawMessage != "garbage line" {
		t.Errorf("unknown: %+v", e)
	}
}

func TestNormalizeFieldHygiene(t *testing.T) {
	p := parser.Parsed{
		Format:  parser.FormatRFC5424,
		Message: "m",
		Raw:     []byte("m"),
		Fields: map[string]string{
			"OK_Key":     "v1",
			"Hostname":   "evil", // reserved: dropped
			"bad key!":   "v2",   // substituted to bad_key_
			"":           "v3",   // dropped
			"SESSION-ID": "v4",   // session_id
		},
	}
	e := New().Normalize(p, meta)
	want := map[string]string{"ok_key": "v1", "bad_key_": "v2", "session_id": "v4"}
	if len(e.Fields) != len(want) {
		t.Fatalf("fields: %v", e.Fields)
	}
	for k, v := range want {
		if e.Fields[k] != v {
			t.Errorf("field %q: got %v", k, e.Fields)
		}
	}
}

func TestNormalizeFieldCaps(t *testing.T) {
	fields := map[string]string{}
	for i := 0; i < 150; i++ {
		fields[string(rune('a'+i%26))+string(rune('a'+i/26))+strings.Repeat("x", 2)] = "v"
	}
	p := parser.Parsed{Format: parser.FormatRFC5424, Message: "m", Raw: []byte("m"), Fields: fields}
	e := New().Normalize(p, meta)
	if len(e.Fields) != DefaultMaxFieldCount {
		t.Errorf("field count cap: got %d want %d", len(e.Fields), DefaultMaxFieldCount)
	}

	// Oversized value truncated.
	big := strings.Repeat("v", DefaultMaxFieldValue+100)
	e = New().Normalize(parser.Parsed{
		Format: parser.FormatRFC5424, Message: "m", Raw: []byte("m"),
		Fields: map[string]string{"big": big},
	}, meta)
	if len(e.Fields["big"]) != DefaultMaxFieldValue {
		t.Errorf("value not truncated: %d", len(e.Fields["big"]))
	}
}

func TestNormalizeMessageTruncation(t *testing.T) {
	big := strings.Repeat("x", DefaultMaxMessageBytes+10)
	e := New().Normalize(parser.Parsed{Format: parser.FormatRFC3164, Message: big, Raw: []byte("r")}, meta)
	if len(e.Message) != DefaultMaxMessageBytes {
		t.Errorf("message truncation: %d", len(e.Message))
	}
	if e.Fields["truncated"] != "true" {
		t.Errorf("truncated flag missing: %v", e.Fields)
	}
}

func TestNormalizeEntrySizeGuard(t *testing.T) {
	n := New()
	n.MaxEntryBytes = 512
	// Big raw + fields: raw is shed first, then fields.
	e := n.Normalize(parser.Parsed{
		Format:  parser.FormatRFC5424,
		Message: strings.Repeat("m", 100),
		Raw:     []byte(strings.Repeat("r", 4000)),
		Fields:  map[string]string{"k": strings.Repeat("v", 100), "k2": strings.Repeat("v", 100)},
	}, meta)
	if e.RawMessage != "" {
		t.Errorf("raw should be shed first: %d bytes", len(e.RawMessage))
	}
	if entrySize(&e) > n.MaxEntryBytes {
		t.Errorf("entry over cap: %d", entrySize(&e))
	}
}

func TestNormalizeNoPanicOnNilFields(t *testing.T) {
	e := New().Normalize(parser.Parsed{Format: parser.FormatRFC3164, Message: "m", Raw: []byte("m")}, parser.Meta{ReceivedAt: time.Now()})
	if e.Fields != nil {
		t.Errorf("no fields expected: %v", e.Fields)
	}
	_ = slog.Default()
}
