package ingestion

import (
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/freezxp/syslogq/internal/config"
	"github.com/freezxp/syslogq/internal/metrics"
	"github.com/freezxp/syslogq/internal/model"
	"github.com/freezxp/syslogq/internal/storage"
	"github.com/freezxp/syslogq/internal/storage/memory"

	"github.com/prometheus/client_golang/prometheus"
)

func testConfig() config.IngestionConfig {
	return config.IngestionConfig{
		QueueCapacity:        100,
		QueuePolicy:          config.PolicyDropNewest,
		Workers:              2,
		MaxMessageBytes:      64 * 1024,
		ActiveConnections:    10,
		StoreUnknown:         true,
		ShutdownDrainTimeout: 2 * time.Second,
		IdleTimeout:          5 * time.Second,
		Batch: config.BatchConfig{
			MaxEntries:    50,
			MaxBytes:      1024 * 1024,
			FlushInterval: 10 * time.Millisecond,
		},
		Sources: []config.SourceConfig{{
			ID:      "src-test",
			Type:    config.TypeSyslogUDP,
			Enabled: true,
			Address: "127.0.0.1:0",
			Parse:   []string{config.ParseRFC5424, config.ParseRFC3164},
		}},
	}
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// newPipeline builds a pipeline without starting it (for enqueue-level tests).
func newPipeline(t *testing.T, cfg config.IngestionConfig, store storage.Writer) (*Pipeline, *metrics.Ingestion, *prometheus.Registry) {
	t.Helper()
	reg := prometheus.NewRegistry()
	m := metrics.NewIngestion(reg)
	pipe, err := NewPipeline(cfg, store, m, discardLogger())
	if err != nil {
		t.Fatalf("NewPipeline: %v", err)
	}
	return pipe, m, reg
}

// newRunningPipeline builds and starts a pipeline; Stop runs on cleanup.
func newRunningPipeline(t *testing.T, cfg config.IngestionConfig, store storage.Writer) (*Pipeline, *metrics.Ingestion, *prometheus.Registry) {
	t.Helper()
	pipe, m, reg := newPipeline(t, cfg, store)
	pipe.Start()
	t.Cleanup(pipe.Stop)
	return pipe, m, reg
}

// waitStored polls until the store holds n entries or 2s pass.
func waitStored(t *testing.T, s *memory.Store, n int) bool {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if s.Len() >= n {
			return true
		}
		time.Sleep(2 * time.Millisecond)
	}
	return s.Len() >= n
}

func droppedCount(t *testing.T, reg *prometheus.Registry, reason string) int {
	t.Helper()
	mfs, err := reg.Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}
	for _, mf := range mfs {
		if mf.GetName() != "syslogq_messages_dropped_total" {
			continue
		}
		var n int
		for _, m := range mf.GetMetric() {
			for _, l := range m.GetLabel() {
				if l.GetName() == "reason" && l.GetValue() == reason {
					n += int(m.GetCounter().GetValue())
				}
			}
		}
		return n
	}
	return 0
}

func entry(msg string) model.LogEntry { return model.LogEntry{Message: msg, SourceID: "src-test"} }

func TestPipelineStoresRFC5424(t *testing.T) {
	store := &memory.Store{}
	pipe, _, _ := newRunningPipeline(t, testConfig(), store)

	raw := "<165>1 2026-09-14T14:30:00Z fw01 vpn 1234 ID47 [example vendor=\"fortinet\" policy_id=\"1234\"] VPN tunnel disconnected"
	pipe.Ingest("src-test", []byte(raw), "10.10.1.1", 41022)

	if !waitStored(t, store, 1) {
		t.Fatalf("entry never stored; store has %d", store.Len())
	}
	got := store.All()[0]
	if got.Hostname != "fw01" || got.AppName != "vpn" || got.ProcessID != "1234" || got.MessageID != "ID47" {
		t.Errorf("header parse wrong: %+v", got)
	}
	if got.Message != "VPN tunnel disconnected" {
		t.Errorf("message: %q", got.Message)
	}
	if got.Severity == nil || *got.Severity != 5 || got.Facility == nil || *got.Facility != 20 {
		t.Errorf("severity/facility: %v %v", got.Severity, got.Facility)
	}
	if got.Fields["vendor"] != "fortinet" || got.Fields["policy_id"] != "1234" {
		t.Errorf("structured data: %v", got.Fields)
	}
	if got.SourceIP != "10.10.1.1" || got.SourcePort != 41022 {
		t.Errorf("origin: %s:%d", got.SourceIP, got.SourcePort)
	}
	if got.Format != "rfc5424" || got.Protocol != "udp" || got.SourceID != "src-test" {
		t.Errorf("classification: %s %s %s", got.Format, got.Protocol, got.SourceID)
	}
	if got.RawMessage == "" {
		t.Error("raw message not preserved")
	}
}

func TestPipelineStoresRFC3164(t *testing.T) {
	store := &memory.Store{}
	pipe, _, _ := newRunningPipeline(t, testConfig(), store)

	pipe.Ingest("src-test", []byte("<34>Oct 11 22:14:15 mymachine su: 'su root' failed"), "10.0.0.9", 0)

	if !waitStored(t, store, 1) {
		t.Fatalf("entry never stored")
	}
	got := store.All()[0]
	if got.Hostname != "mymachine" || got.Message != "'su root' failed" {
		t.Errorf("parsed: hostname=%q message=%q", got.Hostname, got.Message)
	}
	if got.Severity == nil || *got.Severity != 2 || got.Facility == nil || *got.Facility != 4 {
		t.Errorf("severity/facility: %v %v", got.Severity, got.Facility)
	}
	if got.Format != "rfc3164" {
		t.Errorf("format: %s", got.Format)
	}
}

func TestPipelineUnknownFormatStored(t *testing.T) {
	store := &memory.Store{}
	pipe, _, _ := newRunningPipeline(t, testConfig(), store)

	pipe.Ingest("src-test", []byte("this is not syslog"), "10.0.0.9", 0)

	if !waitStored(t, store, 1) {
		t.Fatalf("unknown-format entry never stored")
	}
	got := store.All()[0]
	if got.Format != "unknown" || got.Message != "this is not syslog" {
		t.Errorf("got format=%q message=%q", got.Format, got.Message)
	}
}

func TestPipelineUnknownFormatDroppedWhenDisabled(t *testing.T) {
	store := &memory.Store{}
	cfg := testConfig()
	cfg.StoreUnknown = false
	pipe, _, reg := newRunningPipeline(t, cfg, store)

	pipe.Ingest("src-test", []byte("this is not syslog"), "10.0.0.9", 0)

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && droppedCount(t, reg, "parse_error") == 0 {
		time.Sleep(2 * time.Millisecond)
	}
	if store.Len() != 0 {
		t.Fatalf("unknown entry stored despite store_unknown=false: %d", store.Len())
	}
	if dropped := droppedCount(t, reg, "parse_error"); dropped != 1 {
		t.Fatalf("parse_error drops: %d", dropped)
	}
}

func TestPipelineFormatNotInParseList(t *testing.T) {
	store := &memory.Store{}
	cfg := testConfig()
	cfg.Sources[0].Parse = []string{config.ParseRFC5424}
	pipe, _, _ := newRunningPipeline(t, cfg, store)

	pipe.Ingest("src-test", []byte("<34>Oct 11 22:14:15 host su: failed"), "10.0.0.9", 0)

	// falls back to unknown-format storage
	if !waitStored(t, store, 1) {
		t.Fatalf("entry never stored")
	}
	if got := store.All()[0]; got.Format != "unknown" {
		t.Fatalf("format: %q", got.Format)
	}
}

func TestPipelineUnknownSourceIgnored(t *testing.T) {
	store := &memory.Store{}
	pipe, _, _ := newRunningPipeline(t, testConfig(), store)

	pipe.Ingest("no-such-source", []byte("<34>Oct 11 22:14:15 host su: failed"), "10.0.0.9", 0)

	time.Sleep(30 * time.Millisecond)
	if store.Len() != 0 {
		t.Fatalf("unknown source wrote %d entries", store.Len())
	}
}

func TestPipelineRetryThenSuccess(t *testing.T) {
	// First write fails with a retryable error; the second attempt succeeds.
	store := &memory.Store{FailN: 1, FailErr: storage.ErrBackendUnavailable}
	pipe, _, _ := newRunningPipeline(t, testConfig(), store)

	pipe.Ingest("src-test", []byte("<34>Oct 11 22:14:15 host su: failed"), "10.0.0.9", 0)

	if !waitStored(t, store, 1) {
		t.Fatalf("retry did not succeed; store has %d", store.Len())
	}
	if store.Batches() != 2 {
		t.Fatalf("expected 2 write attempts (fail + success), got %d", store.Batches())
	}
}

func TestPipelineBadRequestNoRetry(t *testing.T) {
	store := &memory.Store{FailN: 10, FailErr: storage.ErrBadRequest}
	cfg := testConfig()
	pipe, _, reg := newRunningPipeline(t, cfg, store)

	pipe.Ingest("src-test", []byte("<34>Oct 11 22:14:15 host su: failed"), "10.0.0.9", 0)

	// ErrBadRequest must abort retries: exactly one write attempt, then a
	// storage_rejected drop.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && droppedCount(t, reg, "storage_rejected") == 0 {
		time.Sleep(2 * time.Millisecond)
	}
	if dropped := droppedCount(t, reg, "storage_rejected"); dropped != 1 {
		t.Fatalf("storage_rejected drops: %d", dropped)
	}
	if n := store.Batches(); n != 1 {
		t.Fatalf("expected exactly 1 write attempt, got %d", n)
	}
	if store.Len() != 0 {
		t.Fatalf("bad-request batch must be dropped, not stored")
	}
}

func TestPipelineShutdownDrains(t *testing.T) {
	store := &memory.Store{Delay: 20 * time.Millisecond}
	pipe, _, _ := newPipeline(t, testConfig(), store)
	pipe.Start()

	for i := 0; i < 20; i++ {
		pipe.Ingest("src-test", []byte("<34>Oct 11 22:14:15 host su: failed"), "10.0.0.9", 0)
	}
	pipe.Stop()

	if store.Len() != 20 {
		t.Fatalf("after drain: stored=%d, want 20 (batches=%d)", store.Len(), store.Batches())
	}
}

func TestPipelineBatchFlushOnInterval(t *testing.T) {
	store := &memory.Store{}
	cfg := testConfig()
	cfg.Batch.MaxEntries = 1000 // only the interval can flush
	cfg.Batch.FlushInterval = 30 * time.Millisecond
	pipe, _, _ := newRunningPipeline(t, cfg, store)

	pipe.Ingest("src-test", []byte("<34>Oct 11 22:14:15 host su: failed"), "10.0.0.9", 0)
	if !waitStored(t, store, 1) {
		t.Fatalf("interval flush never fired")
	}
}

func TestPipelineBatchFlushOnSize(t *testing.T) {
	store := &memory.Store{}
	cfg := testConfig()
	cfg.Batch.MaxEntries = 3
	cfg.Batch.FlushInterval = time.Hour
	pipe, _, _ := newRunningPipeline(t, cfg, store)

	for i := 0; i < 3; i++ {
		pipe.Ingest("src-test", []byte("<34>Oct 11 22:14:15 host su: failed "+string(rune('a'+i))), "10.0.0.9", 0)
	}
	if !waitStored(t, store, 3) {
		t.Fatalf("size flush never fired; stored=%d", store.Len())
	}
}

func TestPipelineOversizedDrop(t *testing.T) {
	store := &memory.Store{}
	pipe, _, reg := newRunningPipeline(t, testConfig(), store)

	pipe.DropOversized("src-test")

	if dropped := droppedCount(t, reg, "oversized"); dropped != 1 {
		t.Fatalf("oversized drops: %d", dropped)
	}
}

// Enqueue policies are tested without Start so the queue state is
// deterministic (no dispatcher draining it).

func TestEnqueueDropNewest(t *testing.T) {
	store := &memory.Store{}
	cfg := testConfig()
	cfg.QueueCapacity = 1
	pipe, _, reg := newPipeline(t, cfg, store)
	s := pipe.sources["src-test"]

	pipe.enqueue(s, entry("A"))
	pipe.enqueue(s, entry("B")) // queue full → B dropped

	if dropped := droppedCount(t, reg, "queue_full"); dropped != 1 {
		t.Fatalf("queue_full drops: %d", dropped)
	}
	if got := <-s.queue; got.Message != "A" {
		t.Fatalf("queue holds %q, want A", got.Message)
	}
	if sat := pipe.Saturation(); sat != 0 {
		t.Fatalf("saturation after drain: %v", sat)
	}
}

func TestEnqueueDropOldest(t *testing.T) {
	store := &memory.Store{}
	cfg := testConfig()
	cfg.QueueCapacity = 1
	cfg.QueuePolicy = config.PolicyDropOldest
	pipe, _, reg := newPipeline(t, cfg, store)
	s := pipe.sources["src-test"]

	pipe.enqueue(s, entry("A"))
	pipe.enqueue(s, entry("B")) // queue full → A evicted, B kept

	if dropped := droppedCount(t, reg, "queue_full"); dropped != 1 {
		t.Fatalf("queue_full drops: %d", dropped)
	}
	if got := <-s.queue; got.Message != "B" {
		t.Fatalf("queue holds %q, want B", got.Message)
	}
}

func TestEnqueueBlock(t *testing.T) {
	store := &memory.Store{}
	cfg := testConfig()
	cfg.QueueCapacity = 1
	cfg.QueuePolicy = config.PolicyBlock
	pipe, _, reg := newPipeline(t, cfg, store)
	s := pipe.sources["src-test"]

	pipe.enqueue(s, entry("A"))

	done := make(chan struct{})
	go func() {
		pipe.enqueue(s, entry("B"))
		close(done)
	}()

	select {
	case <-done:
		t.Fatal("block policy must block while the queue is full")
	case <-time.After(50 * time.Millisecond):
	}

	if dropped := droppedCount(t, reg, "queue_full"); dropped != 0 {
		t.Fatalf("block policy dropped: %d", dropped)
	}

	<-s.queue // free a slot
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("blocked enqueue never completed after freeing a slot")
	}
	if got := <-s.queue; got.Message != "B" {
		t.Fatalf("queue holds %q, want B", got.Message)
	}
}

func TestEnqueueBlockShutdownDrop(t *testing.T) {
	store := &memory.Store{}
	cfg := testConfig()
	cfg.QueueCapacity = 1
	cfg.QueuePolicy = config.PolicyBlock
	pipe, _, reg := newPipeline(t, cfg, store)
	s := pipe.sources["src-test"]

	pipe.enqueue(s, entry("A"))
	done := make(chan struct{})
	go func() {
		pipe.enqueue(s, entry("B"))
		close(done)
	}()
	time.Sleep(20 * time.Millisecond)

	pipe.softCancel() // shutdown begins

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("shutdown must unblock a blocked enqueue")
	}
	if dropped := droppedCount(t, reg, "shutdown"); dropped != 1 {
		t.Fatalf("shutdown drops: %d", dropped)
	}
}

func TestPipelineSaturationFull(t *testing.T) {
	store := &memory.Store{}
	cfg := testConfig()
	cfg.QueueCapacity = 4
	pipe, _, _ := newPipeline(t, cfg, store)
	s := pipe.sources["src-test"]

	for i := 0; i < 4; i++ {
		pipe.enqueue(s, entry("A"))
	}
	if sat := pipe.Saturation(); sat != 1 {
		t.Fatalf("saturation: %v", sat)
	}
}

func TestNewPipelineRejectsNoSources(t *testing.T) {
	cfg := testConfig()
	cfg.Sources = nil
	if _, err := NewPipeline(cfg, &memory.Store{}, metrics.NewIngestion(prometheus.NewRegistry()), discardLogger()); err == nil {
		t.Fatal("expected error for no enabled sources")
	}
}
