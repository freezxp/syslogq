package ingestion

import (
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/freezxp/syslogq/internal/config"
	"github.com/freezxp/syslogq/internal/metrics"
)

// tcpListener serves RFC6587-framed syslog over TCP (or TLS when tlsCfg is
// set). The accept backlog is bounded by ingestion.active_connections.
type tcpListener struct {
	cfg    config.SourceConfig
	pipe   *Pipeline
	m      *metrics.Ingestion
	log    *slog.Logger
	tlsCfg *tls.Config

	ln       net.Listener
	close    sync.Once
	closeErr error

	active  atomic.Int64
	mu      sync.Mutex
	open    map[net.Conn]struct{}
	connsWG sync.WaitGroup
}

func newTCPListener(cfg config.SourceConfig, pipe *Pipeline, m *metrics.Ingestion, log *slog.Logger, tlsCfg *tls.Config) (*tcpListener, error) {
	ln, err := net.Listen("tcp", cfg.Address)
	if err != nil {
		return nil, fmt.Errorf("source %s: bind tcp: %w", cfg.ID, err)
	}
	return &tcpListener{
		cfg:    cfg,
		pipe:   pipe,
		m:      m,
		log:    log,
		tlsCfg: tlsCfg,
		ln:     ln,
		open:   make(map[net.Conn]struct{}),
	}, nil
}

func (t *tcpListener) SourceID() string { return t.cfg.ID }

func (t *tcpListener) Addr() net.Addr { return t.ln.Addr() }

func (t *tcpListener) Run() error {
	t.log.Info("tcp listener started", "source_id", t.cfg.ID, "addr", t.ln.Addr().String(),
		"tls", t.tlsCfg != nil)

	for {
		conn, err := t.ln.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				t.connsWG.Wait()
				return nil
			}
			// Transient accept failures (EMFILE &c.) must not kill the
			// listener; retry with a small delay.
			t.log.Warn("accept error", "source_id", t.cfg.ID, "error", err)
			time.Sleep(100 * time.Millisecond)
			continue
		}
		t.handleAccept(conn)
	}
}

func (t *tcpListener) handleAccept(conn net.Conn) {
	if limit := t.pipe.ActiveConnections(); limit > 0 && int(t.active.Load()) >= limit {
		t.m.ConnsRejected.WithLabelValues(t.cfg.ID).Inc()
		conn.Close()
		return
	}
	if t.tlsCfg != nil {
		conn = tls.Server(conn, t.tlsCfg)
	}

	t.mu.Lock()
	t.open[conn] = struct{}{}
	t.mu.Unlock()
	t.connsWG.Add(1)
	t.active.Add(1)
	t.m.ActiveConns.WithLabelValues(t.cfg.ID).Set(float64(t.active.Load()))

	go func() {
		defer t.connsWG.Done()
		t.serveConn(conn)
	}()
}

func (t *tcpListener) serveConn(conn net.Conn) {
	remote := "unknown"
	if addr := conn.RemoteAddr(); addr != nil {
		remote = addr.String()
	}
	defer func() {
		conn.Close()
		t.mu.Lock()
		delete(t.open, conn)
		t.mu.Unlock()
		t.active.Add(-1)
		t.m.ActiveConns.WithLabelValues(t.cfg.ID).Set(float64(t.active.Load()))
	}()

	ip, port := splitAddr(conn.RemoteAddr())
	fr := newFrameReader(conn, t.pipe.MaxMessageBytes(), t.pipe.IdleTimeout())
	for {
		msg, err := fr.next()
		if err != nil {
			switch {
			case errors.Is(err, io.EOF):
				return // peer closed
			case errors.Is(err, errOversizedFrame):
				t.pipe.DropOversized(t.cfg.ID)
				continue // frame skipped; connection stays usable
			case errors.Is(err, ErrIdleTimeout):
				t.log.Debug("connection idle timeout", "source_id", t.cfg.ID, "remote", remote)
				return
			case errors.Is(err, net.ErrClosed):
				return
			default:
				t.log.Debug("connection read error", "source_id", t.cfg.ID, "remote", remote, "error", err)
				return
			}
		}
		if len(msg) == 0 {
			continue
		}
		t.pipe.Ingest(t.cfg.ID, msg, ip, port)
	}
}

// Close stops accepting and tears down live connections; handlers finish
// via read errors. Safe to call once.
func (t *tcpListener) Close() error {
	t.close.Do(func() {
		t.closeErr = t.ln.Close()
		t.mu.Lock()
		conns := make([]net.Conn, 0, len(t.open))
		for c := range t.open {
			conns = append(conns, c)
		}
		t.mu.Unlock()
		for _, c := range conns {
			c.Close()
		}
	})
	return t.closeErr
}
