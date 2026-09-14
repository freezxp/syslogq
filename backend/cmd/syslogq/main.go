// Command syslogq is the syslogq server: syslog listeners, ingestion
// pipeline, HTTP API, and embedded web UI.
//
// Phase 0 skeleton: process bootstrap, structured logging, and signal
// handling only. Listeners, pipeline, and API land in Phase 1 per
// docs/roadmap.md.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
)

// version is set at build time via -ldflags "-X main.version=…".
var version = "dev"

func main() {
	showVersion := flag.Bool("version", false, "print version and exit")
	configPath := flag.String("config", "", "path to syslogq.yaml (also SYSLOGQ_CONFIG)")
	flag.Parse()

	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	})))

	if *showVersion {
		fmt.Println("syslogq", version)
		return
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	slog.InfoContext(ctx, "starting", "version", version, "config", *configPath)

	// Phase 1: config load + validation, listener/pipeline/API start here,
	// then graceful drain on ctx cancellation.

	<-ctx.Done()
	slog.Info("shutdown signal received, stopping")
	slog.Info("shutdown complete")
}
