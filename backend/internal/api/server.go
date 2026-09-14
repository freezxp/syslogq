package api

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/freezxp/syslogq/internal/health"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Config for the Phase 1 API server: /health, /ready, /metrics (docs/api.md
// §2 "Meta"). Versioned REST endpoints land in Phase 3.
type Config struct {
	// Address to bind (":8080").
	Address string
	// ShutdownTimeout bounds graceful HTTP shutdown (default 5s).
	ShutdownTimeout time.Duration
}

// Server is the HTTP server for metrics and health endpoints.
type Server struct {
	http    *http.Server
	cfg     Config
	log     *slog.Logger
	metrics *prometheus.Registry
	ready   *health.Registry
}

func NewServer(cfg Config, metricsReg *prometheus.Registry, ready *health.Registry, log *slog.Logger) *Server {
	if cfg.ShutdownTimeout <= 0 {
		cfg.ShutdownTimeout = 5 * time.Second
	}
	if log == nil {
		log = slog.Default()
	}
	s := &Server{cfg: cfg, log: log, metrics: metricsReg, ready: ready}
	mux := http.NewServeMux()
	mux.HandleFunc("/health", s.handleLiveness)
	mux.Handle("/ready", ready.Handler())
	mux.Handle("/metrics", promhttp.HandlerFor(metricsReg, promhttp.HandlerOpts{}))
	s.http = &http.Server{
		Addr:              cfg.Address,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	return s
}

// Start serves until Stop is called; it returns the listener error.
func (s *Server) Start() error {
	ln, err := net.Listen("tcp", s.cfg.Address)
	if err != nil {
		return err
	}
	s.log.Info("http server started", "addr", ln.Addr().String())
	if err := s.http.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// Stop gracefully drains in-flight HTTP requests.
func (s *Server) Stop() {
	ctx, cancel := context.WithTimeout(context.Background(), s.cfg.ShutdownTimeout)
	defer cancel()
	if err := s.http.Shutdown(ctx); err != nil {
		s.log.Warn("http shutdown", "error", err)
	}
}

func (s *Server) handleLiveness(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}
