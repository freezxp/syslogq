package parser

import (
	"bytes"
	"fmt"
	"time"
)

// RFC5424 parses "<PRI>VERSION TIMESTAMP HOSTNAME APP-NAME PROCID MSGID
// STRUCTURED-DATA MSG" messages. STRUCTURED-DATA params are flattened into
// Fields (docs/log-data-model.md §5.1). Malformed input returns an error;
// the pipeline counts it and stores the raw line as format "unknown".
type RFC5424 struct{}

func (p *RFC5424) Name() string { return FormatRFC5424 }

func init() { Register(&RFC5424{}) }

var bom = []byte{0xEF, 0xBB, 0xBF}

func (p *RFC5424) Parse(raw []byte, _ Meta) (Parsed, error) {
	out := Parsed{Format: FormatRFC5424, Raw: raw}

	pri, rest, ok := parsePRI(raw)
	if !ok {
		return Parsed{}, fmt.Errorf("rfc5424: missing valid PRI")
	}
	fac, sev := pri/8, pri%8
	out.Facility, out.Severity = &fac, &sev

	// VERSION: 1–3 non-leading-zero digits, then SP.
	i := 0
	for i < len(rest) && isDigit(rest[i]) {
		i++
	}
	if i == 0 || i > 3 || i >= len(rest) || rest[i] != ' ' {
		return Parsed{}, fmt.Errorf("rfc5424: invalid VERSION")
	}
	rest = rest[i+1:]

	var tok []byte
	var err error
	if tok, rest, err = nextToken(rest); err != nil {
		return Parsed{}, err
	}
	if string(tok) != "-" {
		ts, err := time.Parse(time.RFC3339, string(tok))
		if err != nil {
			return Parsed{}, fmt.Errorf("rfc5424: invalid TIMESTAMP %q: %w", string(tok), err)
		}
		out.Timestamp = ts
	}
	if tok, rest, err = nextToken(rest); err != nil {
		return Parsed{}, err
	}
	out.Hostname = nilValue(tok)
	if tok, rest, err = nextToken(rest); err != nil {
		return Parsed{}, err
	}
	out.AppName = nilValue(tok)
	if tok, rest, err = nextToken(rest); err != nil {
		return Parsed{}, err
	}
	out.ProcessID = nilValue(tok)
	if tok, rest, err = nextToken(rest); err != nil {
		return Parsed{}, err
	}
	out.MessageID = nilValue(tok)

	fields, rest, err := parseStructuredData(rest)
	if err != nil {
		return Parsed{}, err
	}
	out.Fields = fields

	if len(rest) > 0 {
		if rest[0] != ' ' {
			return Parsed{}, fmt.Errorf("rfc5424: expected SP before MSG")
		}
		msg := rest[1:]
		msg = bytes.TrimPrefix(msg, bom)
		out.Message = string(msg)
	}
	return out, nil
}

// nextToken splits the leading space-delimited token. It errors when the
// remaining input is exhausted — all five header fields are mandatory.
func nextToken(b []byte) ([]byte, []byte, error) {
	i := bytes.IndexByte(b, ' ')
	if i < 0 {
		if len(b) == 0 {
			return nil, nil, fmt.Errorf("rfc5424: truncated header")
		}
		return b, nil, fmt.Errorf("rfc5424: truncated header")
	}
	if i == 0 {
		return nil, nil, fmt.Errorf("rfc5424: empty header field")
	}
	return b[:i], b[i+1:], nil
}

func nilValue(b []byte) string {
	if string(b) == "-" {
		return ""
	}
	return string(b)
}

// parseStructuredData parses NILVALUE ("-", meaning no SD) or one or more
// "[id name=\"value\" …]" elements. Param values honor \", \\, \] escapes.
// It returns the bytes after SD (empty, or " MSG").
func parseStructuredData(b []byte) (map[string]string, []byte, error) {
	if len(b) == 0 {
		return nil, nil, fmt.Errorf("rfc5424: missing STRUCTURED-DATA")
	}
	if b[0] == '-' {
		rest := b[1:]
		if len(rest) > 0 && rest[0] != ' ' {
			return nil, nil, fmt.Errorf("rfc5424: junk after SD nilvalue")
		}
		return nil, rest, nil
	}
	if b[0] != '[' {
		return nil, nil, fmt.Errorf("rfc5424: STRUCTURED-DATA must start with '['")
	}

	var fields map[string]string
	i := 0
	for i < len(b) && b[i] == '[' {
		i++
		for i < len(b) && b[i] != ' ' && b[i] != ']' {
			i++
		}
		if i >= len(b) {
			return nil, nil, fmt.Errorf("rfc5424: unterminated SD-ID")
		}
		for i < len(b) && b[i] == ' ' {
			i++ // skip SP before PARAM-NAME
			ns := i
			for i < len(b) && b[i] != '=' && b[i] != ']' && b[i] != ' ' {
				i++
			}
			if i >= len(b) || b[i] != '=' {
				return nil, nil, fmt.Errorf("rfc5424: invalid PARAM-NAME")
			}
			name := string(b[ns:i])
			i++ // '='
			if i >= len(b) || b[i] != '"' {
				return nil, nil, fmt.Errorf("rfc5424: PARAM-VALUE must be quoted")
			}
			i++
			val := make([]byte, 0, 16)
			closed := false
			for i < len(b) {
				c := b[i]
				if c == '\\' && i+1 < len(b) {
					switch b[i+1] {
					case '\\', '"', ']':
						val = append(val, b[i+1])
						i += 2
						continue
					}
					val = append(val, c)
					i++
					continue
				}
				if c == '"' {
					i++
					closed = true
					break
				}
				val = append(val, c)
				i++
			}
			if !closed {
				return nil, nil, fmt.Errorf("rfc5424: unterminated PARAM-VALUE")
			}
			if fields == nil {
				fields = make(map[string]string, 4)
			}
			fields[name] = string(val)
		}
		if i >= len(b) || b[i] != ']' {
			return nil, nil, fmt.Errorf("rfc5424: unterminated SD element")
		}
		i++ // ']'
	}
	if i == 0 {
		return nil, nil, fmt.Errorf("rfc5424: empty STRUCTURED-DATA")
	}
	rest := b[i:]
	if len(rest) > 0 && rest[0] != ' ' {
		return nil, nil, fmt.Errorf("rfc5424: junk after STRUCTURED-DATA")
	}
	return fields, rest, nil
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }
