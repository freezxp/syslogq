package ingestion

import (
	"context"
	"crypto/rand"
	"errors"
	"log/slog"
	"math/big"
	"sync"
	"sync/atomic"
	"time"

	"github.com/freezxp/syslogq/internal/config"
	"github.com/freezxp/syslogq/internal/metrics"
	"github.com/freezxp/syslogq/internal/model"
	"github.com/freezxp/syslogq/internal/normalization"
	"github.com/freezxp/syslogq/internal/parser"
	"github.com/freezxp/syslogq/internal/storage"
)

// Pipeline wires listeners → per-source bounded queues → batcher → writer
// pool → storage (docs/ingestion.md). All channels are bounded; the only
// exit path for entries is storage (success) or a counted drop.
type Pipeline struct {
	cfg    config.IngestionConfig
	writer storage.Writer
	m      *metrics.Ingestion
	log    *slog.Logger
	norm   *normalization.Normalizer

	sources map[string]*source
	drops   *dropLogger

	in      chan model.LogEntry
	batches chan []model.LogEntry

	softCtx    context.Context // canceled on Stop: no more intake, drain begins
	softCancel context.CancelFunc
	hardCtx    context.Context // canceled at the drain deadline: drop remainder
	hardCancel context.CancelFunc
	dispatchWG sync.WaitGroup
	workerWG   sync.WaitGroup
}

// source is one configured listener's slice of the pipeline.
type source struct {
	cfg      config.SourceConfig
	protocol string // "udp" | "tcp" | "tls"
	policy   string
	parse    map[string]bool
	queue    chan model.LogEntry
}

func NewPipeline(cfg config.IngestionConfig, w storage.Writer, m *metrics.Ingestion, log *slog.Logger) (*Pipeline, error) {
	if log == nil {
		log = slog.Default()
	}
	p := &Pipeline{
		cfg:     cfg,
		writer:  w,
		m:       m,
		log:     log,
		norm:    normalization.New(),
		sources: make(map[string]*source, len(cfg.Sources)),
		drops:   newDropLogger(log),
	}
	p.softCtx, p.softCancel = context.WithCancel(context.Background())
	p.hardCtx, p.hardCancel = context.WithCancel(context.Background())
	for _, sc := range cfg.Sources {
		if !sc.Enabled {
			continue
		}
		parse := make(map[string]bool, len(sc.Parse))
		for _, f := range sc.Parse {
			parse[f] = true
		}
		policy := sc.QueuePolicy
		if policy == "" {
			policy = cfg.QueuePolicy
		}
		p.sources[sc.ID] = &source{
			cfg:      sc,
			protocol: protocolFor(sc.Type),
			policy:   policy,
			parse:    parse,
			// Bounded queue per source: one chatty sender cannot starve
			// the others; the ceiling is ingestion.queue_capacity.
			queue: make(chan model.LogEntry, cfg.QueueCapacity),
		}
		m.QueueCapacity.WithLabelValues(sc.ID).Set(float64(cfg.QueueCapacity))
	}
	if len(p.sources) == 0 {
		return nil, errors.New("ingestion: no enabled sources")
	}
	p.in = make(chan model.LogEntry, 1024)
	p.batches = make(chan []model.LogEntry, cfg.Workers*2)
	return p, nil
}

func protocolFor(srcType string) string {
	switch srcType {
	case config.TypeSyslogUDP:
		return "udp"
	case config.TypeSyslogTLS:
		return "tls"
	default:
		return "tcp"
	}
}

// Start launches dispatchers, the batcher, and the writer pool. Contexts are
// created in NewPipeline; Stop cancels them.
func (p *Pipeline) Start() {
	go p.batcherLoop()
	for i := 0; i < p.cfg.Workers; i++ {
		p.workerWG.Add(1)
		go p.writerLoop()
	}
	for _, s := range p.sources {
		p.dispatchWG.Add(1)
		go p.dispatchLoop(s)
	}
	go p.gaugeLoop()
	go func() {
		p.dispatchWG.Wait()
		close(p.in)
	}()
}

// Stop cancels intake and drains: listeners must already be closed so no
// new entries arrive. Bounded by shutdown_drain_timeout; whatever remains
// past it is dropped and counted.
func (p *Pipeline) Stop() {
	p.softCancel()
	done := make(chan struct{})
	go func() {
		p.workerWG.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(p.cfg.ShutdownDrainTimeout):
		p.log.Warn("shutdown drain deadline exceeded, dropping remainder",
			"timeout", p.cfg.ShutdownDrainTimeout.String())
		p.hardCancel()
		<-done
	}
	p.hardCancel()
}

// IngestRaw is the HTTP-ingest entry point: it validates that raw is a JSON
// object (the only HTTP body shape), then feeds it through the standard
// path. Returns false for non-JSON payloads (counted as rejected).
func (p *Pipeline) IngestRaw(srcID string, raw []byte, remoteIP string) bool {
	if _, ok := p.sources[srcID]; !ok {
		return false
	}
	if len(raw) == 0 || raw[0] != '{' {
		p.m.ParseErrors.WithLabelValues(srcID, parser.FormatJSON).Inc()
		return false
	}
	p.Ingest(srcID, raw, remoteIP, 0)
	return true
}

// Ingest is the listeners' entry point: detect, parse, normalize, enqueue.
// Safe for concurrent use.
func (p *Pipeline) Ingest(srcID string, raw []byte, remoteIP string, remotePort int) {
	s, ok := p.sources[srcID]
	if !ok {
		return
	}
	p.m.MessagesReceived.WithLabelValues(s.cfg.ID, s.protocol).Inc()
	p.m.BytesReceived.WithLabelValues(s.cfg.ID, s.protocol).Add(float64(len(raw)))

	meta := parser.Meta{
		SourceID:   s.cfg.ID,
		SourceType: model.SourceTypeSyslog,
		Protocol:   s.protocol,
		RemoteIP:   remoteIP,
		RemotePort: remotePort,
		ReceivedAt: time.Now().UTC(),
	}

	format := parser.Detect(raw)
	if format == parser.FormatUnknown {
		p.m.UnknownFormat.WithLabelValues(s.cfg.ID).Inc()
		p.storeUnknown(s, raw, meta)
		return
	}
	if !s.parse[format] {
		// Detected a format this source is not configured to accept.
		p.m.ParseErrors.WithLabelValues(s.cfg.ID, format).Inc()
		p.storeUnknown(s, raw, meta)
		return
	}
	pr, ok := parser.Lookup(format)
	if !ok {
		p.m.ParseErrors.WithLabelValues(s.cfg.ID, format).Inc()
		p.storeUnknown(s, raw, meta)
		return
	}
	parsed, err := pr.Parse(raw, meta)
	if err != nil {
		p.m.ParseErrors.WithLabelValues(s.cfg.ID, format).Inc()
		p.log.Debug("parse failure", "source_id", s.cfg.ID, "format", format, "error", err)
		p.storeUnknown(s, raw, meta)
		return
	}
	p.m.MessagesParsed.WithLabelValues(s.cfg.ID, format).Inc()
	p.enqueue(s, p.norm.Normalize(parsed, meta))
}

// storeUnknown keeps the raw line under format "unknown" per FR-I3, or
// drops it (counted) when ingestion.store_unknown is false.
func (p *Pipeline) storeUnknown(s *source, raw []byte, meta parser.Meta) {
	if !p.cfg.StoreUnknown {
		p.drop(s, metrics.ReasonParseError)
		return
	}
	p.enqueue(s, p.norm.Normalize(parser.Parsed{
		Format:  parser.FormatUnknown,
		Message: string(raw),
		Raw:     raw,
	}, meta))
}

func (p *Pipeline) enqueue(s *source, e model.LogEntry) {
	switch s.policy {
	case config.PolicyBlock:
		select {
		case s.queue <- e:
		case <-p.softCtx.Done():
			p.drop(s, metrics.ReasonShutdown)
		}
	case config.PolicyDropOldest:
		select {
		case s.queue <- e:
			return
		default:
		}
		select {
		case <-s.queue:
			p.drop(s, metrics.ReasonQueueFull)
		default:
		}
		select {
		case s.queue <- e:
		default:
			p.drop(s, metrics.ReasonQueueFull)
		}
	default: // drop_newest
		select {
		case s.queue <- e:
		default:
			p.drop(s, metrics.ReasonQueueFull)
		}
	}
}

func (p *Pipeline) dispatchLoop(s *source) {
	defer p.dispatchWG.Done()
	for {
		select {
		case e := <-s.queue:
			p.forward(s, e)
		case <-p.softCtx.Done():
			p.drainSource(s)
			return
		case <-p.hardCtx.Done():
			p.discardSource(s)
			return
		}
	}
}

// forward hands one entry to the batcher; under the hard deadline the
// entry and the rest of the queue are dropped as shutdown losses.
func (p *Pipeline) forward(s *source, e model.LogEntry) {
	select {
	case p.in <- e:
	case <-p.hardCtx.Done():
		p.drop(s, metrics.ReasonShutdown)
		p.discardSource(s)
	}
}

func (p *Pipeline) drainSource(s *source) {
	for {
		select {
		case e := <-s.queue:
			p.forward(s, e)
		default:
			return
		}
	}
}

func (p *Pipeline) discardSource(s *source) {
	for {
		select {
		case <-s.queue:
			p.drop(s, metrics.ReasonShutdown)
		default:
			return
		}
	}
}

func (p *Pipeline) batcherLoop() {
	defer close(p.batches)
	ticker := time.NewTicker(p.cfg.Batch.FlushInterval)
	defer ticker.Stop()

	batch := make([]model.LogEntry, 0, p.cfg.Batch.MaxEntries)
	var batchBytes int
	var started time.Time

	flush := func() {
		if len(batch) == 0 {
			return
		}
		p.m.BatchFlushSeconds.Observe(time.Since(started).Seconds())
		p.m.BatchSizeEntries.Observe(float64(len(batch)))
		select {
		case p.batches <- batch:
		case <-p.hardCtx.Done():
			p.dropEntries(batch, metrics.ReasonShutdown)
		}
		batch = make([]model.LogEntry, 0, p.cfg.Batch.MaxEntries)
		batchBytes = 0
	}

	for {
		select {
		case e, ok := <-p.in:
			if !ok {
				flush()
				return
			}
			if len(batch) == 0 {
				started = time.Now()
			}
			batch = append(batch, e)
			batchBytes += approxEntrySize(&e)
			if len(batch) >= p.cfg.Batch.MaxEntries || batchBytes >= p.cfg.Batch.MaxBytes {
				flush()
			}
		case <-ticker.C:
			flush()
		case <-p.hardCtx.Done():
			p.dropEntries(batch, metrics.ReasonShutdown)
			for range p.in { // unblock dispatchers still forwarding
			}
			return
		}
	}
}

func (p *Pipeline) writerLoop() {
	defer p.workerWG.Done()
	for batch := range p.batches {
		p.writeBatch(batch)
	}
}

const writeAttempts = 3

func (p *Pipeline) writeBatch(batch []model.LogEntry) {
	var lastErr error
	for attempt := 0; attempt < writeAttempts; attempt++ {
		if attempt > 0 {
			select {
			case <-time.After(backoff(attempt)):
			case <-p.hardCtx.Done():
				p.dropEntries(batch, metrics.ReasonShutdown)
				return
			}
		}
		start := time.Now()
		err := p.writer.WriteLogs(p.hardCtx, batch)
		p.m.StorageWriteSecs.Observe(time.Since(start).Seconds())
		if err == nil {
			p.countStored(batch)
			return
		}
		lastErr = err
		p.m.StorageWriteErrors.Inc()
		if errors.Is(err, storage.ErrBadRequest) {
			break // the same payload cannot succeed on retry
		}
	}
	reason := metrics.ReasonWriteFailed
	if errors.Is(lastErr, storage.ErrBadRequest) {
		reason = metrics.ReasonStorageRejected
	}
	p.log.Warn("storage write failed, dropping batch", "reason", reason,
		"entries", len(batch), "error", lastErr)
	p.dropEntries(batch, reason)
}

func (p *Pipeline) countStored(batch []model.LogEntry) {
	counts := make(map[string]int, 4)
	var stored int
	for i := range batch {
		counts[batch[i].SourceID]++
		stored += approxEntrySize(&batch[i])
	}
	for srcID, n := range counts {
		p.m.MessagesStored.WithLabelValues(srcID).Add(float64(n))
	}
	p.m.BytesStored.Add(float64(stored))
}

func (p *Pipeline) drop(s *source, reason string) {
	p.m.MessagesDropped.WithLabelValues(s.cfg.ID, reason).Inc()
	p.drops.record(s.cfg.ID, reason)
}

func (p *Pipeline) dropEntries(batch []model.LogEntry, reason string) {
	counts := make(map[string]int, 4)
	for i := range batch {
		counts[batch[i].SourceID]++
	}
	for srcID, n := range counts {
		p.m.MessagesDropped.WithLabelValues(srcID, reason).Add(float64(n))
		p.drops.recordN(srcID, reason, n)
	}
}

func (p *Pipeline) gaugeLoop() {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			for id, s := range p.sources {
				p.m.QueueDepth.WithLabelValues(id).Set(float64(len(s.queue)))
			}
		case <-p.softCtx.Done():
			return
		}
	}
}

// Saturation is the fullest source queue as a fraction of capacity.
func (p *Pipeline) Saturation() float64 {
	maxSat := 0.0
	for _, s := range p.sources {
		if cap(s.queue) == 0 {
			continue
		}
		sat := float64(len(s.queue)) / float64(cap(s.queue))
		if sat > maxSat {
			maxSat = sat
		}
	}
	return maxSat
}

// MaxMessageBytes is the per-message size guard shared with listeners.
func (p *Pipeline) MaxMessageBytes() int { return p.cfg.MaxMessageBytes }

// ActiveConnections is the concurrent TCP/TLS connection ceiling.
func (p *Pipeline) ActiveConnections() int { return p.cfg.ActiveConnections }

// IdleTimeout is the TCP/TLS idle connection timeout.
func (p *Pipeline) IdleTimeout() time.Duration { return p.cfg.IdleTimeout }

// DropOversized counts a message rejected at the socket for size; listeners
// call it before the frame ever reaches Ingest.
func (p *Pipeline) DropOversized(srcID string) {
	if s, ok := p.sources[srcID]; ok {
		p.drop(s, metrics.ReasonOversized)
	}
}

func approxEntrySize(e *model.LogEntry) int {
	return len(e.Message) + len(e.RawMessage) + 128
}

// backoff returns attempt's pre-jitter delay (200ms, 400ms) plus jitter.
func backoff(attempt int) time.Duration {
	base := time.Duration(1<<uint(attempt)) * 100 * time.Millisecond
	return base + jitter(base/2)
}

func jitter(max time.Duration) time.Duration {
	if max <= 0 {
		return 0
	}
	n, err := rand.Int(rand.Reader, big.NewInt(int64(max)))
	if err != nil {
		return max / 2
	}
	return time.Duration(n.Int64())
}

// dropLogger keeps drop accounting noisy enough to see, quiet enough to
// survive: first drop and every 1000th per source hit the warn log.
type dropLogger struct {
	mu     sync.Mutex
	log    *slog.Logger
	counts map[string]*atomic.Int64
}

func newDropLogger(log *slog.Logger) *dropLogger {
	return &dropLogger{log: log, counts: make(map[string]*atomic.Int64)}
}

func (d *dropLogger) counter(source string) *atomic.Int64 {
	d.mu.Lock()
	defer d.mu.Unlock()
	c, ok := d.counts[source]
	if !ok {
		c = &atomic.Int64{}
		d.counts[source] = c
	}
	return c
}

func (d *dropLogger) record(source, reason string) {
	d.recordN(source, reason, 1)
}

func (d *dropLogger) recordN(source, reason string, n int) {
	c := d.counter(source)
	total := c.Add(int64(n))
	if total == int64(n) || total%1000 < int64(n) {
		d.log.Warn("messages dropped", "source_id", source, "reason", reason, "total_dropped", total)
	}
}
