package parser

import (
	"bytes"
	"time"
)

// RFC3164 parses classic BSD syslog messages (docs/log-data-model.md §5).
// It is deliberately lenient: PRI, timestamp, and hostname are all optional
// prefixes; whatever remains is the message (with a best-effort TAG[PID]
// split). It never fails — anything unrecognizable becomes the message body.
type RFC3164 struct{}

func (p *RFC3164) Name() string { return FormatRFC3164 }

func init() { Register(&RFC3164{}) }

func (p *RFC3164) Parse(raw []byte, _ Meta) (Parsed, error) {
	out := Parsed{Format: FormatRFC3164, Raw: raw}
	rest := raw

	if pri, rem, ok := parsePRI(rest); ok {
		fac, sev := pri/8, pri%8
		out.Facility, out.Severity = &fac, &sev
		rest = rem
	}
	if ts, rem, ok := parse3164Timestamp(rest); ok {
		out.Timestamp = ts
		// Skip the single SP separating TIMESTAMP from what follows.
		if len(rem) > 0 && rem[0] == ' ' {
			rem = rem[1:]
		}
		rest = rem
		// HOSTNAME follows TIMESTAMP in the HEADER; without a timestamp the
		// first token is indistinguishable from message text, so only
		// attempt hostname extraction when a timestamp was present.
		if host, rem, ok := parseHostname(rest); ok {
			out.Hostname = host
			rest = rem
		}
	}
	out.AppName, out.ProcessID, out.Message = splitTagMessage(rest)
	return out, nil
}

// parsePRI parses "<ddd>" (PRI ≤ 191). Returns ok=false without consuming.
func parsePRI(b []byte) (int, []byte, bool) {
	if len(b) == 0 || b[0] != '<' {
		return 0, b, false
	}
	v := 0
	i := 1
	for i < len(b) && b[i] >= '0' && b[i] <= '9' {
		v = v*10 + int(b[i]-'0')
		i++
		if i > 4 {
			return 0, b, false
		}
	}
	if i == 1 || i >= len(b) || b[i] != '>' || v > 191 {
		return 0, b, false
	}
	return v, b[i+1:], true
}

// parse3164Timestamp accepts the RFC3164 "Mmm dd hh:mm:ss" stamp (either day
// padding) or an ISO-8601 timestamp followed by a space. The RFC3164 stamp
// has no year: we infer the current year and roll back one year when that
// lands more than 24h in the future.
func parse3164Timestamp(b []byte) (time.Time, []byte, bool) {
	if len(b) >= 15 {
		if t, err := time.Parse("Jan _2 15:04:05", string(b[:15])); err == nil {
			return inferYear(t), b[15:], true
		}
		if t, err := time.Parse("Jan 02 15:04:05", string(b[:15])); err == nil {
			return inferYear(t), b[15:], true
		}
	}
	if i := bytes.IndexByte(b, ' '); i > 9 && i <= 35 {
		if t, err := time.Parse(time.RFC3339Nano, string(b[:i])); err == nil {
			return t, b[i:], true
		}
	}
	return time.Time{}, b, false
}

func inferYear(t time.Time) time.Time {
	now := time.Now().UTC()
	ts := time.Date(now.Year(), t.Month(), t.Day(), t.Hour(), t.Minute(), t.Second(), 0, time.UTC)
	if ts.After(now.Add(24 * time.Hour)) {
		ts = ts.AddDate(-1, 0, 0)
	}
	return ts
}

// parseHostname takes the next whitespace-delimited token as HOSTNAME when it
// looks like one (no colon — a token with a colon is a TAG, not a host).
func parseHostname(b []byte) (string, []byte, bool) {
	i := bytes.IndexByte(b, ' ')
	if i <= 0 || i > 255 {
		return "", b, false
	}
	tok := b[:i]
	for _, c := range tok {
		if c == ':' {
			return "", b, false
		}
	}
	if len(bytes.TrimLeft(tok, "-.")) == 0 {
		return "", b, false
	}
	return string(tok), b[i:], true
}

// splitTagMessage applies the RFC3164 TAG[PID]: CONTENT convention when the
// leading token is tag-like (≤48 chars of [A-Za-z0-9_./-]); otherwise the
// whole remainder is the message.
func splitTagMessage(b []byte) (tag, pid, msg string) {
	b = bytes.TrimLeft(b, " ")
	if len(b) == 0 {
		return "", "", ""
	}
	i := 0
	for i < len(b) && b[i] != ':' && b[i] != '[' && b[i] != ' ' {
		i++
	}
	if i == 0 || i > 48 {
		return "", "", string(b)
	}
	for _, c := range b[:i] {
		if !isTagChar(c) {
			return "", "", string(b)
		}
	}
	rest := b[i:]
	if len(rest) > 0 && rest[0] == '[' {
		j := bytes.IndexByte(rest, ']')
		if j < 2 {
			return "", "", string(b)
		}
		pid = string(rest[1:j])
		rest = rest[j+1:]
	}
	if len(rest) == 0 || rest[0] != ':' {
		return "", "", string(b)
	}
	return string(b[:i]), pid, string(bytes.TrimLeft(rest[1:], " "))
}

func isTagChar(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' ||
		c >= '0' && c <= '9' || c == '_' || c == '.' || c == '/' || c == '-'
}
