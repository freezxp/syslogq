package parser

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/freezxp/syslogq/internal/model"
)

// jsonParser ingests structured JSON log objects (docs/ingestion.md §6):
// standard keys map to LogEntry fields, everything else becomes Fields.
type jsonParser struct{}

func init() { Register(&jsonParser{}) }

func (p *jsonParser) Name() string { return FormatJSON }

// Parse accepts a single JSON object.
func (p *jsonParser) Parse(raw []byte, meta Meta) (Parsed, error) {
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return Parsed{}, err
	}
	return MapToParsed(m, raw, meta)
}

// MapToParsed converts one decoded JSON object to Parsed; shared with the
// HTTP ingest endpoint (which handles arrays/NDJSON itself).
//
// Key mapping: timestamp/time/@timestamp → Timestamp; host/hostname →
// Hostname; level/severity(_name)/priority → Severity (name or number);
// facility → Facility; service/app(_name) → AppName; message/msg → Message.
// Remaining keys become Fields (stringified).
func MapToParsed(m map[string]any, raw []byte, meta Meta) (Parsed, error) {
	out := Parsed{Format: FormatJSON, Fields: map[string]string{}, Raw: raw}

	// Pass 1: consume recognized standard keys.
	recognized := make(map[string]bool, 16)
	for k, v := range m {
		lk := strings.ToLower(k)
		switch lk {
		case "timestamp", "time", "@timestamp":
			recognized[k] = true
			if s, ok := toString(v); ok {
				if ts, err := parseJSONTime(s); err == nil {
					out.Timestamp = ts
				}
			}
		case "host", "hostname":
			recognized[k] = true
			if s, ok := toString(v); ok {
				out.Hostname = s
			}
		case "level", "severity", "severity_name", "level_name":
			recognized[k] = true
			if s, ok := toString(v); ok {
				out.Severity = severityFromNameOrNumber(s)
			}
		case "priority", "pri":
			recognized[k] = true
			if n, ok := toInt(v); ok && n >= 0 && n <= 191 {
				pri := n
				out.Priority = &pri
				f := pri / 8
				s := pri % 8
				out.Facility = &f
				out.Severity = &s
			}
		case "facility":
			recognized[k] = true
			if n, ok := toInt(v); ok && n >= 0 && n <= 23 {
				out.Facility = &n
			}
		case "message", "msg", "short_message":
			recognized[k] = true
			if s, ok := toString(v); ok {
				out.Message = s
			}
		case "service", "app", "app_name":
			recognized[k] = true
			if s, ok := toString(v); ok {
				out.AppName = s
			}
		case "process_id", "procid":
			recognized[k] = true
			if s, ok := toString(v); ok {
				out.ProcessID = s
			}
		}
	}

	// Pass 2: everything else → Fields.
	for k, v := range m {
		if recognized[k] {
			continue
		}
		if s, ok := toString(v); ok {
			out.Fields[strings.ToLower(k)] = s
		}
	}

	if out.Message == "" {
		// No message key: keep the raw JSON so nothing is lost.
		out.Message = string(raw)
	}
	return out, nil
}

// JSONDecode unmarshals one JSON object; test/ingest helper.
func JSONDecode(raw []byte) (map[string]any, error) {
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, err
	}
	return m, nil
}

func parseJSONTime(s string) (time.Time, error) {
	if ts, err := time.Parse(time.RFC3339Nano, s); err == nil {
		return ts.UTC(), nil
	}
	if ts, err := time.Parse(time.RFC3339, s); err == nil {
		return ts.UTC(), nil
	}
	// Unix seconds / milliseconds as bare numbers arrive as strings rarely;
	// support the common integer-seconds form.
	if n, err := strconv.ParseInt(s, 10, 64); err == nil && n > 1_000_000_000 {
		return time.Unix(n, 0).UTC(), nil
	}
	return time.Time{}, fmt.Errorf("unparsable timestamp %q", s)
}

func severityFromNameOrNumber(s string) *int {
	if n, err := strconv.Atoi(s); err == nil && n >= 0 && n <= 7 {
		return &n
	}
	for i := 0; i <= 7; i++ {
		if strings.EqualFold(model.SeverityNameRFC5424(i), s) {
			return &i
		}
	}
	for i := 0; i <= 7; i++ {
		if strings.EqualFold(model.SeverityNameRFC3164(i), s) {
			return &i
		}
	}
	switch strings.ToLower(s) {
	case "fatal", "critical", "crit":
		v := 2
		return &v
	case "warn", "warning":
		v := 4
		return &v
	case "err", "error":
		v := 3
		return &v
	}
	return nil
}

func toString(v any) (string, bool) {
	switch x := v.(type) {
	case string:
		return x, true
	case float64: // JSON numbers
		if x == float64(int64(x)) {
			return strconv.FormatInt(int64(x), 10), true
		}
		return strconv.FormatFloat(x, 'g', -1, 64), true
	case bool:
		return strconv.FormatBool(x), true
	case nil:
		return "", true
	default:
		b, err := json.Marshal(x)
		if err != nil {
			return "", false
		}
		return string(b), true
	}
}

func toInt(v any) (int, bool) {
	switch x := v.(type) {
	case float64:
		return int(x), true
	case int:
		return x, true
	case string:
		if n, err := strconv.Atoi(x); err == nil {
			return n, true
		}
	}
	return 0, false
}
