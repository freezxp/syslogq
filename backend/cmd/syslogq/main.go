// Command syslogq is the syslogq server: syslog listeners, ingestion
// pipeline, and the HTTP surface (health, readiness, metrics). The REST API
// and web UI land in later phases per docs/roadmap.md.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/freezxp/syslogq/internal/api"
	"github.com/freezxp/syslogq/internal/config"
	"github.com/freezxp/syslogq/internal/health"
	"github.com/freezxp/syslogq/internal/ingestion"
	"github.com/freezxp/syslogq/internal/metrics"
	"github.com/freezxp/syslogq/internal/storage/victorialogs"

	"github.com/prometheus/client_golang/prometheus"
)

// version is set at build time via -ldflags "-X main.version=…".
var version = "dev"

func main() {
	if code := run(); code != 0 {
		os.Exit(code)
	}
}

func run() int {
	showVersion := flag.Bool("version", false, "print version and exit")
	configPath := flag.String("config", "", "path to syslogq.yaml (also SYSLOGQ_CONFIG)")
	flag.Parse()

	if *showVersion {
		fmt.Println("syslogq", version)
		return 0
	}

	cfg, err := config.Load(config.LoadPath(*configPath))
	if err != nil {
		slog.Error("configuration invalid", "error", err)
		return 1
	}

	logger := newLogger(cfg.Logging.Level)
	slog.SetDefault(logger)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	logger.Info("starting", "version", version)

	store, err := victorialogs.New(victorialogs.Config{
		BaseURL:   cfg.Storage.URL,
		Timeout:   cfg.Storage.Timeout,
		AccountID: cfg.Storage.AccountID,
	})
	if err != nil {
		logger.Error("storage client", "error", err)
		return 1
	}

	reg := prometheus.NewRegistry()
	ing := metrics.NewIngestion(reg)

	pipe, err := ingestion.NewPipeline(cfg.Ingestion, store, ing, logger)
	if err != nil {
		logger.Error("ingestion pipeline", "error", err)
		return 1
	}
	pipe.Start()

	mgr := ingestion.NewManager(logger)
	for _, src := range cfg.EnabledSources() {
		l, lerr := ingestion.NewListener(src, pipe, ing, logger)
		if lerr != nil {
			logger.Error("listener setup failed", "source_id", src.ID, "error", lerr)
			mgr.Close()
			pipe.Stop()
			return 1
		}
		mgr.Add(src.ID, l)
	}
	mgr.Start()

	ready := health.NewRegistry(
		health.NewChecker("storage", store.Health),
		health.NewChecker("listeners", func(_ context.Context) error {
			return mgr.DownError()
		}),
		health.NewChecker("queue", func(_ context.Context) error {
			if pipe.Saturation() >= 0.9 {
				return fmt.Errorf("queue saturation %.0f%%", pipe.Saturation()*100)
			}
			return nil
		}),
	)

	srv := api.NewServer(api.Config{Address: cfg.API.Address}, reg, ready, logger)
	httpErr := make(chan error, 1)
	go func() { httpErr <- srv.Start() }()

	logger.Info("started",
		"api", cfg.API.Address,
		"storage", cfg.Storage.URL,
		"sources", len(cfg.EnabledSources()))

	select {
	case <-ctx.Done():
		logger.Info("shutdown signal received")
	case err := <-httpErr:
		logger.Error("http server failed", "error", err)
	}

	// Shutdown order (docs/ingestion.md §3): stop listeners, drain the
	// pipeline, then stop HTTP.
	mgr.Close()
	pipe.Stop()
	srv.Stop()
	logger.Info("shutdown complete")
	return 0
}

func newLogger(level string) *slog.Logger {
	var lvl slog.Level
	switch level {
	case "debug":
		lvl = slog.LevelDebug
	case "warn":
		lvl = slog.LevelWarn
	case "error":
		lvl = slog.LevelError
	default:
		lvl = slog.LevelInfo
	}
	return slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: lvl}))
}
