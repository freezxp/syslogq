package parser

import "time"

// Parser turns a raw wire payload into a Parsed value. Implementations are
// pure functions over bytes, safe for concurrent use, and never panic on
// hostile input (docs/ingestion.md §4).
type Parser interface {
	Name() string
	Parse(raw []byte, meta Meta) (Parsed, error)
}

// Meta carries what the listener knows about a message before parsing.
type Meta struct {
	SourceID   string
	SourceType string
	Protocol   string // "udp" | "tcp" | "tls" | "http"
	RemoteIP   string
	RemotePort int
	ReceivedAt time.Time
}

// Parsed is the parser-agnostic result consumed by normalization.
type Parsed struct {
	Format    string
	Timestamp time.Time // zero if the message carried no parsable time
	Hostname  string
	AppName   string
	ProcessID string
	MessageID string
	Facility  *int
	Severity  *int
	Message   string
	Fields    map[string]string
	Raw       []byte
}

// Format identifiers (also used as metric label values).
const (
	FormatRFC3164 = "rfc3164"
	FormatRFC5424 = "rfc5424"
	FormatJSON    = "json"
	FormatUnknown = "unknown"
)

// registered maps format name → parser. The set grows per phase; Phase 1
// ships the two syslog formats.
var registered = map[string]Parser{}

func Register(p Parser) { registered[p.Name()] = p }

func Lookup(format string) (Parser, bool) {
	p, ok := registered[format]
	return p, ok
}
