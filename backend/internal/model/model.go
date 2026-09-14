package model

import "time"

// LogEntry is the single normalized internal representation of a log record
// across the whole pipeline. See docs/log-data-model.md.
type LogEntry struct {
	// Identity / time
	ID         string    `json:"id,omitempty"`
	Timestamp  time.Time `json:"timestamp"`
	ReceivedAt time.Time `json:"received_at"`

	// Core content
	Message string `json:"message"`

	// Network origin
	Hostname   string `json:"hostname,omitempty"`
	SourceIP   string `json:"source_ip,omitempty"`
	SourcePort int    `json:"source_port,omitempty"`

	// Syslog classification
	Facility     *int   `json:"facility,omitempty"`
	FacilityName string `json:"facility_name,omitempty"`
	Severity     *int   `json:"severity,omitempty"`
	SeverityName string `json:"severity_name,omitempty"`
	Priority     *int   `json:"priority,omitempty"`

	// Origin classification
	Protocol  string `json:"protocol,omitempty"`
	Format    string `json:"format,omitempty"`
	AppName   string `json:"app_name,omitempty"`
	ProcessID string `json:"process_id,omitempty"`
	MessageID string `json:"message_id,omitempty"`

	// Platform extension points
	SourceID   string `json:"source_id,omitempty"`
	SourceType string `json:"source_type,omitempty"`
	TenantID   string `json:"tenant_id,omitempty"`

	// Arbitrary data (schema-on-read)
	Fields map[string]string `json:"fields,omitempty"`
	Labels map[string]string `json:"labels,omitempty"`

	// Raw preservation (always stored, UI-togglable)
	RawMessage string `json:"raw_message,omitempty"`
}

// TenantDefault is the single tenant id until multi-tenancy lands (Phase 7).
const TenantDefault = "default"

// SourceTypeSyslog marks entries arriving via the syslog listeners.
const SourceTypeSyslog = "syslog"
