// Package metrics defines the Prometheus collectors for the ingestion path
// and HTTP/API surface (docs/ingestion.md §8, docs/api.md §9). Collectors
// are created against an injected Registerer so tests get isolated
// registries.
package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
)

const namespace = "syslogq"

// Ingestion holds the ingest-path collectors. All *Vec metrics take their
// documented label sets; source is the configured source id.
type Ingestion struct {
	MessagesReceived *prometheus.CounterVec // {source, protocol}
	BytesReceived    *prometheus.CounterVec // {source, protocol}
	MessagesParsed   *prometheus.CounterVec // {source, format}
	ParseErrors      *prometheus.CounterVec // {source, format}
	UnknownFormat    *prometheus.CounterVec // {source}
	MessagesStored   *prometheus.CounterVec // {source}
	MessagesDropped  *prometheus.CounterVec // {source, reason}
	BytesStored      prometheus.Counter
	QueueDepth       *prometheus.GaugeVec // {source}
	QueueCapacity    *prometheus.GaugeVec // {source}
	ActiveConns      *prometheus.GaugeVec // {source}
	ConnsRejected    *prometheus.CounterVec

	BatchFlushSeconds  prometheus.Histogram
	BatchSizeEntries   prometheus.Histogram
	StorageWriteSecs   prometheus.Histogram
	StorageWriteErrors prometheus.Counter
}

func NewIngestion(reg prometheus.Registerer) *Ingestion {
	factory := newFactory(reg)
	m := &Ingestion{
		MessagesReceived: factory.counterVec("messages_received_total", "Messages accepted by listeners.", "source", "protocol"),
		BytesReceived:    factory.counterVec("bytes_received_total", "Bytes accepted by listeners.", "source", "protocol"),
		MessagesParsed:   factory.counterVec("messages_parsed_total", "Messages successfully parsed, by format.", "source", "format"),
		ParseErrors:      factory.counterVec("messages_parse_errors_total", "Parse failures, by format.", "source", "format"),
		UnknownFormat:    factory.counterVec("messages_unknown_format_total", "Messages whose format could not be detected.", "source"),
		MessagesStored:   factory.counterVec("messages_stored_total", "Entries accepted by storage, by source.", "source"),
		MessagesDropped:  factory.counterVec("messages_dropped_total", "Dropped entries by reason (queue_full, oversized, parse_error, storage_write_failed, storage_rejected, shutdown).", "source", "reason"),
		BytesStored:      factory.counter("bytes_stored_total", "Bytes accepted by storage."),
		QueueDepth:       factory.gaugeVec("queue_depth", "Entries currently queued per source.", "source"),
		QueueCapacity:    factory.gaugeVec("queue_capacity", "Configured queue capacity per source.", "source"),
		ActiveConns:      factory.gaugeVec("active_connections", "Active TCP/TLS connections per source.", "source"),
		ConnsRejected:    factory.counterVec("connections_rejected_total", "Connections rejected over the active-connection limit.", "source"),

		BatchFlushSeconds:  factory.histogram("batch_flush_seconds", "Time from batch start to flush.", prometheus.ExponentialBuckets(0.001, 2, 12)),
		BatchSizeEntries:   factory.histogram("batch_size_entries", "Entries per flushed batch.", prometheus.ExponentialBuckets(1, 2, 14)),
		StorageWriteSecs:   factory.histogram("storage_write_seconds", "Storage WriteLogs call duration.", prometheus.ExponentialBuckets(0.001, 2, 12)),
		StorageWriteErrors: factory.counter("storage_write_errors_total", "Failed storage write attempts."),
	}
	return m
}

// Drop reason label values shared across the pipeline.
const (
	ReasonQueueFull       = "queue_full"
	ReasonOversized       = "oversized"
	ReasonParseError      = "parse_error"
	ReasonWriteFailed     = "storage_write_failed"
	ReasonStorageRejected = "storage_rejected"
	ReasonShutdown        = "shutdown"
	ReasonConnectionLimit = "connection_limit"
)

type regFactory struct{ reg prometheus.Registerer }

func newFactory(reg prometheus.Registerer) *regFactory { return &regFactory{reg: reg} }

func (f *regFactory) counter(name, help string) prometheus.Counter {
	c := prometheus.NewCounter(prometheus.CounterOpts{Namespace: namespace, Name: name, Help: help})
	f.reg.MustRegister(c)
	return c
}

func (f *regFactory) counterVec(name, help string, labels ...string) *prometheus.CounterVec {
	c := prometheus.NewCounterVec(prometheus.CounterOpts{Namespace: namespace, Name: name, Help: help}, labels)
	f.reg.MustRegister(c)
	return c
}

func (f *regFactory) gaugeVec(name, help string, labels ...string) *prometheus.GaugeVec {
	g := prometheus.NewGaugeVec(prometheus.GaugeOpts{Namespace: namespace, Name: name, Help: help}, labels)
	f.reg.MustRegister(g)
	return g
}

func (f *regFactory) histogram(name, help string, buckets []float64) prometheus.Histogram {
	h := prometheus.NewHistogram(prometheus.HistogramOpts{Namespace: namespace, Name: name, Help: help, Buckets: buckets})
	f.reg.MustRegister(h)
	return h
}
