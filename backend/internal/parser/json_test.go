package parser

import (
	"testing"
	"time"
)

func TestJSONParserStandardKeys(t *testing.T) {
	raw := []byte(`{"timestamp":"2026-09-14T14:30:00Z","host":"server01","level":"error","service":"nginx","message":"connection refused","source_ip":"10.10.10.20"}`)
	parsed, err := MapToParsed(decode(t, raw), raw, Meta{})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if !parsed.Timestamp.Equal(time.Date(2026, 9, 14, 14, 30, 0, 0, time.UTC)) {
		t.Errorf("timestamp: %v", parsed.Timestamp)
	}
	if parsed.Hostname != "server01" {
		t.Errorf("hostname: %q", parsed.Hostname)
	}
	if parsed.Severity == nil || *parsed.Severity != 3 {
		t.Errorf("severity: %v", parsed.Severity)
	}
	if parsed.AppName != "nginx" {
		t.Errorf("app_name: %q", parsed.AppName)
	}
	if parsed.Message != "connection refused" {
		t.Errorf("message: %q", parsed.Message)
	}
	if parsed.Fields["source_ip"] != "10.10.10.20" {
		t.Errorf("fields: %v", parsed.Fields)
	}
}

func TestJSONParserNumericSeverityAndPriority(t *testing.T) {
	raw := []byte(`{"severity":5,"facility":20,"message":"m"}`)
	parsed, err := MapToParsed(decode(t, raw), raw, Meta{})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if parsed.Severity == nil || *parsed.Severity != 5 || parsed.Facility == nil || *parsed.Facility != 20 {
		t.Fatalf("sev/fac: %v %v", parsed.Severity, parsed.Facility)
	}

	raw = []byte(`{"priority":164,"message":"m"}`)
	parsed, err = MapToParsed(decode(t, raw), raw, Meta{})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if parsed.Priority == nil || *parsed.Priority != 164 || parsed.Severity == nil || *parsed.Severity != 4 {
		t.Fatalf("priority-derived: %+v", parsed)
	}
}

func TestJSONParserSeverityAliases(t *testing.T) {
	cases := map[string]int{"warn": 4, "warning": 4, "critical": 2, "fatal": 2, "debug": 7, "info": 6, "err": 3}
	for in, want := range cases {
		raw := []byte(`{"level":"` + in + `","message":"m"}`)
		parsed, err := MapToParsed(decode(t, raw), raw, Meta{})
		if err != nil {
			t.Fatalf("parse %q: %v", in, err)
		}
		if parsed.Severity == nil || *parsed.Severity != want {
			t.Errorf("level %q → sev %v, want %d", in, parsed.Severity, want)
		}
	}
}

func TestJSONParserNestedAndTypedValues(t *testing.T) {
	raw := []byte(`{"message":"m","http_status":503,"dur_ms":12.5,"ok":true,"tags":{"env":"prod"},"count":3}`)
	parsed, err := MapToParsed(decode(t, raw), raw, Meta{})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if parsed.Fields["http_status"] != "503" {
		t.Errorf("int field: %q", parsed.Fields["http_status"])
	}
	if parsed.Fields["dur_ms"] != "12.5" {
		t.Errorf("float field: %q", parsed.Fields["dur_ms"])
	}
	if parsed.Fields["ok"] != "true" {
		t.Errorf("bool field: %q", parsed.Fields["ok"])
	}
	if parsed.Fields["tags"] != `{"env":"prod"}` {
		t.Errorf("nested field: %q", parsed.Fields["tags"])
	}
	if parsed.Fields["count"] != "3" {
		t.Errorf("count: %q", parsed.Fields["count"])
	}
}

func TestJSONParserNoMessageKeepsRaw(t *testing.T) {
	raw := []byte(`{"just":"fields"}`)
	parsed, err := MapToParsed(decode(t, raw), raw, Meta{})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if parsed.Message != string(raw) {
		t.Errorf("message fallback: %q", parsed.Message)
	}
}

func TestJSONParserHostAliases(t *testing.T) {
	for _, key := range []string{"host", "hostname"} {
		raw := []byte(`{"` + key + `":"h1","message":"m"}`)
		parsed, err := MapToParsed(decode(t, raw), raw, Meta{})
		if err != nil {
			t.Fatalf("parse: %v", err)
		}
		if parsed.Hostname != "h1" {
			t.Errorf("%s → hostname: %q", key, parsed.Hostname)
		}
	}
}

func TestJSONParserUnixSecondsTimestamp(t *testing.T) {
	raw := []byte(`{"timestamp":1789400000,"message":"m"}`)
	parsed, err := MapToParsed(decode(t, raw), raw, Meta{})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if parsed.Timestamp.IsZero() {
		t.Error("unix timestamp not parsed")
	}
}

func decode(t *testing.T, raw []byte) map[string]any {
	t.Helper()
	m, err := JSONDecode(raw)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	return m
}
