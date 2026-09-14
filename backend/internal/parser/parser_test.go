package parser

import (
	"strings"
	"testing"
	"time"
)

func mustParse(t *testing.T, format, input string) Parsed {
	t.Helper()
	p, ok := Lookup(format)
	if !ok {
		t.Fatalf("parser %q not registered", format)
	}
	out, err := p.Parse([]byte(input), Meta{ReceivedAt: time.Date(2026, 9, 14, 15, 0, 0, 0, time.UTC)})
	if err != nil {
		t.Fatalf("parse %q: %v", input, err)
	}
	return out
}

func TestDetect(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"<165>1 2026-09-14T14:30:00Z fw01 vpn - - - msg", FormatRFC5424},
		{"<34>123 2026-09-14T14:30:00Z h a p m - x", FormatRFC5424}, // multi-digit version
		{"<13>Sep 14 15:30:00 myhost tag: msg", FormatRFC3164},
		{"<13>1x", FormatRFC3164},     // version not followed by space
		{"<13>1234 x", FormatRFC3164}, // 4-digit "version" → not 5424
		{"<0>", FormatRFC3164},
		{"13 no pri", FormatUnknown},
		{"<abc>1 x", FormatUnknown},
		{"{\"json\":true}", FormatJSON},
		{"[1,2]", FormatUnknown},
		{"", FormatUnknown},
		// Oversized PRI + version: sniffs 5424, fails PRI validation there,
		// and is stored as format "unknown" via the parse-error path.
		{"<1234>1 x", FormatRFC5424},
	}
	for _, c := range cases {
		if got := Detect([]byte(c.in)); got != c.want {
			t.Errorf("Detect(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestRFC3164Standard(t *testing.T) {
	// bsdutils logger(1) shape.
	out := mustParse(t, FormatRFC3164, "<13>Sep 14 15:30:00 myhost app: hello world")
	if out.Hostname != "myhost" || out.AppName != "app" || out.Message != "hello world" {
		t.Errorf("got %+v", out)
	}
	if out.Facility == nil || *out.Facility != 1 || out.Severity == nil || *out.Severity != 5 {
		t.Errorf("pri split wrong: %+v", out)
	}
	// Year inferred (message has none).
	if out.Timestamp.Month() != time.September || out.Timestamp.Day() != 14 ||
		out.Timestamp.Hour() != 15 || out.Timestamp.Minute() != 30 {
		t.Errorf("timestamp wrong: %v", out.Timestamp)
	}
}

func TestRFC3164TagWithPid(t *testing.T) {
	out := mustParse(t, FormatRFC3164, "<34>Oct 11 22:14:15 fw01 sshd[4123]: Accepted password for root")
	if out.AppName != "sshd" || out.ProcessID != "4123" {
		t.Errorf("tag/pid: %+v", out)
	}
	if out.Message != "Accepted password for root" {
		t.Errorf("message: %q", out.Message)
	}
	if out.Severity == nil || *out.Severity != 2 || out.Facility == nil || *out.Facility != 4 {
		t.Errorf("pri: %+v", out)
	}
}

func TestRFC3164Variants(t *testing.T) {
	cases := []struct {
		name, in       string
		host, tag, msg string
	}{
		{"no tag", "<13>Sep 14 15:30:00 myhost Connection reset by peer", "myhost", "", "Connection reset by peer"},
		{"no hostname", "<13>Sep 14 15:30:00 kernel: usb device attached", "", "kernel", "usb device attached"},
		{"no pri", "Sep 14 15:30:00 myhost su: auth failure", "myhost", "su", "auth failure"},
		{"bare message", "<13>just a message", "", "", "just a message"},
		{"no timestamp", "<13>myhost app: started", "", "", "myhost app: started"},
		// Leading "token:" is conventionally a TAG (same as rsyslog).
		{"colon in message", "<13>Sep 14 15:30:00 h error: http://x:8080 failed", "h", "error", "http://x:8080 failed"},
		{"ip hostname", "<13>Sep 14 15:30:00 10.0.0.1 tag: m", "10.0.0.1", "tag", "m"},
		{"fqdn host", "<13>Sep 14 15:30:00 host.example.com tag: m", "host.example.com", "tag", "m"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out := mustParse(t, FormatRFC3164, c.in)
			if out.Hostname != c.host || out.AppName != c.tag || out.Message != c.msg {
				t.Errorf("got host=%q tag=%q msg=%q, want %q/%q/%q",
					out.Hostname, out.AppName, out.Message, c.host, c.tag, c.msg)
			}
		})
	}
}

func TestRFC3164Hostile(t *testing.T) {
	// Hostile or degenerate inputs must not panic; message body absorbs them.
	inputs := []string{
		"",
		"<",
		"<>",
		"<0",
		"<000>",
		"<192>1 x",
		"<99999>msg",
		"<-1>msg",
		strings.Repeat("A", 100_000),
		"<13>\x00\x01\x02\xff\xfe binary \x00 payload",
		"<13>Sep 99 99:99:99 host tag: msg",
		"<13>Sep 14 15:30:00 " + strings.Repeat("h", 300) + " tag: msg",
	}
	p := &RFC3164{}
	for _, in := range inputs {
		out, err := p.Parse([]byte(in), Meta{})
		if err != nil {
			t.Errorf("RFC3164 must never error, got %v for %q", err, truncate(in))
		}
		_ = out
	}
}

func TestRFC3164ISOTimestamp(t *testing.T) {
	out := mustParse(t, FormatRFC3164,
		"<13>2026-09-14T15:30:00Z myhost app: iso time")
	want := time.Date(2026, 9, 14, 15, 30, 0, 0, time.UTC)
	if !out.Timestamp.Equal(want) {
		t.Errorf("timestamp: got %v want %v", out.Timestamp, want)
	}
}

func truncate(s string) string {
	if len(s) > 40 {
		return s[:40] + "…"
	}
	return s
}

func TestRFC5424Full(t *testing.T) {
	// RFC 5424 §6.5 examples + docs/log-data-model.md §5.1.
	out := mustParse(t, FormatRFC5424,
		`<165>1 2026-09-14T14:30:00.000Z fw01 vpn 1234 ID47 [example vendor="fortinet" policy_id="1234"] VPN tunnel disconnected`)
	if out.Hostname != "fw01" || out.AppName != "vpn" || out.ProcessID != "1234" || out.MessageID != "ID47" {
		t.Errorf("header: %+v", out)
	}
	if out.Facility == nil || *out.Facility != 20 || out.Severity == nil || *out.Severity != 5 {
		t.Errorf("pri: %+v", out)
	}
	if out.Message != "VPN tunnel disconnected" {
		t.Errorf("message: %q", out.Message)
	}
	if out.Fields["vendor"] != "fortinet" || out.Fields["policy_id"] != "1234" {
		t.Errorf("fields: %v", out.Fields)
	}
	want := time.Date(2026, 9, 14, 14, 30, 0, 0, time.UTC)
	if !out.Timestamp.Equal(want) {
		t.Errorf("timestamp: %v", out.Timestamp)
	}
}

func TestRFC5424Variants(t *testing.T) {
	cases := []struct {
		name, in       string
		host, app, msg string
	}{
		{"minimal", "<34>1 2026-09-14T14:30:00Z h a p m - hello", "h", "a", "hello"},
		{"no sd no msg", "<34>1 2026-09-14T14:30:00Z h a p m -", "h", "a", ""},
		{"nilvalue header", "<34>1 - h a p m - x", "h", "a", "x"},
		{"no timestamp", "<34>1 - h a p m - x", "h", "a", "x"},
		{"multi sd", `<34>1 2026-09-14T14:30:00Z h a p m [a k="1"][b k="2"] msg`, "h", "a", "msg"},
		{"empty sd el", `<34>1 2026-09-14T14:30:00Z h a p m [] msg`, "h", "a", "msg"},
		{"empty msg", "<34>1 2026-09-14T14:30:00Z h a p m - ", "h", "a", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out := mustParse(t, FormatRFC5424, c.in)
			if out.Hostname != c.host || out.AppName != c.app || out.Message != c.msg {
				t.Errorf("got %+v", out)
			}
		})
	}
}

func TestRFC5424SDEscapes(t *testing.T) {
	out := mustParse(t, FormatRFC5424,
		`<34>1 2026-09-14T14:30:00Z h a p m [x k="a\"b\\c\]d"] msg`)
	if out.Fields["k"] != `a"b\c]d` {
		t.Errorf("escape handling: %q", out.Fields["k"])
	}
}

func TestRFC5424FractionalAndOffset(t *testing.T) {
	out := mustParse(t, FormatRFC5424, "<34>1 2026-09-14T14:30:00.123456+02:00 h a p m - x")
	want := time.Date(2026, 9, 14, 12, 30, 0, 123456000, time.UTC)
	if !out.Timestamp.Equal(want) {
		t.Errorf("timestamp: got %v want %v", out.Timestamp, want)
	}
}

func TestRFC5424Malformed(t *testing.T) {
	cases := []string{
		"",                                       // nothing
		"no pri",                                 // no PRI
		"<34>x 2026-09-14T14:30:00Z h a p m - x", // version not digit
		"<34>1 2026-09-14T14:30:00Z",             // truncated header
		"<34>1 notatime h a p m - x",             // bad timestamp
		"<34>1 2026-09-14T14:30:00Z h a p m",     // no SD
		"<34>1 2026-09-14T14:30:00Z h a p m [unterminated",           // bad SD
		`<34>1 2026-09-14T14:30:00Z h a p m [x k="unterminated] msg`, // bad value
		`<34>1 2026-09-14T14:30:00Z h a p m [x k=noquote] msg`,       // unquoted value
		"<34>1 2026-09-14T14:30:00Z h a p m -junk",                   // junk after SD
		"<34>1 2026-09-14T14:30:00Z h a p m -  extra",                // two spaces ok? (MSG may contain spaces — valid)
	}
	// The last case is actually valid: MSG = " extra" (leading space part of msg).
	invalid := cases[:len(cases)-1]
	p := &RFC5424{}
	for _, in := range invalid {
		if _, err := p.Parse([]byte(in), Meta{}); err == nil {
			t.Errorf("expected error for %q", truncate(in))
		}
	}
}

func TestRFC5424HostileNoPanic(t *testing.T) {
	inputs := []string{
		"<34>1 ",
		"<34>1 - - - - - ",
		"<34>1 - - - - - [",
		"<34>1 - - - - - [][][]",
		`<34>1 - - - - - [x k="` + strings.Repeat("\\", 10_000),
		strings.Repeat("<34>1 - - - - - [x k=\"v\"] ", 1000),
		"<34>1 - - - - - - \xef\xbb\xbfBOM message",
	}
	p := &RFC5424{}
	for _, in := range inputs {
		out, _ := p.Parse([]byte(in), Meta{})
		_ = out
	}
}

func TestRFC5424BOMStripped(t *testing.T) {
	out := mustParse(t, FormatRFC5424, "<34>1 - h a p m - \xef\xbb\xbfbonjour")
	if out.Message != "bonjour" {
		t.Errorf("BOM not stripped: %q", out.Message)
	}
}
