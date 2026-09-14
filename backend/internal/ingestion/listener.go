package ingestion

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/freezxp/syslogq/internal/config"
	"github.com/freezxp/syslogq/internal/metrics"
)

// Listener owns one source's socket lifecycle (docs/ingestion.md §1).
type Listener interface {
	Run() error
	Close() error
	SourceID() string
	// Addr is the bound address (usable after construction).
	Addr() net.Addr
}

// NewListener builds the listener for a source config, opening the socket
// eagerly so bind errors surface at startup, before Run.
func NewListener(cfg config.SourceConfig, pipe *Pipeline, m *metrics.Ingestion, log *slog.Logger) (Listener, error) {
	switch cfg.Type {
	case config.TypeSyslogUDP:
		return newUDPListener(cfg, pipe, m, log)
	case config.TypeSyslogTCP:
		return newTCPListener(cfg, pipe, m, log, nil)
	case config.TypeSyslogTLS:
		tlsCfg, err := buildTLSConfig(cfg.TLS)
		if err != nil {
			return nil, fmt.Errorf("source %s: %w", cfg.ID, err)
		}
		return newTCPListener(cfg, pipe, m, log, tlsCfg)
	default:
		return nil, fmt.Errorf("source %s: unknown type %q", cfg.ID, cfg.Type)
	}
}

// buildTLSConfig loads the source's certificate material. Paths come from
// validated operator config, not user input.
func buildTLSConfig(t config.TLSConfig) (*tls.Config, error) {
	if t.CertFile == "" || t.KeyFile == "" {
		return nil, errors.New("tls: cert_file and key_file required")
	}
	cert, err := tls.LoadX509KeyPair(t.CertFile, t.KeyFile) //nolint:gosec // G304: operator-configured paths
	if err != nil {
		return nil, fmt.Errorf("tls: load keypair: %w", err)
	}
	cfg := &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS12,
	}
	if t.ClientCAFile != "" {
		pem, err := os.ReadFile(t.ClientCAFile) //nolint:gosec // G304: operator-configured path
		if err != nil {
			return nil, fmt.Errorf("tls: read client CA: %w", err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pem) {
			return nil, errors.New("tls: client_ca_file contains no certificates")
		}
		cfg.ClientCAs = pool
		cfg.ClientAuth = tls.RequireAndVerifyClientCert
	}
	return cfg, nil
}

// Manager starts and tracks listeners for readiness and shutdown.
type Manager struct {
	mu    sync.Mutex
	items []*managedListener
	wg    sync.WaitGroup
	log   *slog.Logger
}

type managedListener struct {
	id      string
	l       Listener
	running atomic.Bool
}

func NewManager(log *slog.Logger) *Manager {
	return &Manager{log: log}
}

func (m *Manager) Add(id string, l Listener) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.items = append(m.items, &managedListener{id: id, l: l})
}

func (m *Manager) Start() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, it := range m.items {
		m.wg.Add(1)
		go func(it *managedListener) {
			defer m.wg.Done()
			it.running.Store(true)
			err := it.l.Run()
			it.running.Store(false)
			if err != nil && !errors.Is(err, net.ErrClosed) {
				m.log.Error("listener stopped", "source_id", it.id, "error", err)
			}
		}(it)
	}
}

// Close stops all listeners and waits for their Run loops (and, for TCP,
// their connection handlers) to finish. Safe to call once.
func (m *Manager) Close() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, it := range m.items {
		if err := it.l.Close(); err != nil {
			m.log.Warn("listener close error", "source_id", it.id, "error", err)
		}
	}
	m.wg.Wait()
}

// DownSources lists sources whose listener is not running.
func (m *Manager) DownSources() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	var down []string
	for _, it := range m.items {
		if !it.running.Load() {
			down = append(down, it.id)
		}
	}
	return down
}

// DownError reports listener state for readiness: nil while every source is
// running, an error naming the down sources otherwise.
func (m *Manager) DownError() error {
	down := m.DownSources()
	if len(down) == 0 {
		return nil
	}
	return fmt.Errorf("sources down: %s", strings.Join(down, ","))
}
