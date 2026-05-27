// Command backend is the Agent Orchestrator HTTP daemon: a loopback-only
// sidecar spawned and supervised by the Electron main process. Phase 1a brings
// up the server skeleton — config, 127.0.0.1 bind, middleware stack, health
// probes, the running.json handshake, and graceful shutdown.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd"
	"github.com/aoagents/agent-orchestrator/backend/internal/runfile"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "ao backend daemon: "+err.Error())
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	log := newLogger(cfg)

	// Fail fast if a live daemon already owns the handshake file. A run-file
	// left by a crashed predecessor (dead PID) is treated as stale and
	// overwritten when the new server starts.
	if live, err := runfile.CheckStale(cfg.RunFilePath); err != nil {
		return fmt.Errorf("inspect run-file: %w", err)
	} else if live != nil {
		return fmt.Errorf("daemon already running (pid %d, port %d); refusing to start", live.PID, live.Port)
	}

	srv, err := httpd.New(cfg, log)
	if err != nil {
		return err
	}

	// signal.NotifyContext cancels ctx on SIGINT/SIGTERM, which drives the
	// graceful shutdown inside Server.Run.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	return srv.Run(ctx)
}

// newLogger returns a text logger in development and JSON in production. The
// daemon logs to stderr so the Electron supervisor can capture it separately
// from any structured stdout protocol added later.
func newLogger(cfg config.Config) *slog.Logger {
	if cfg.IsProduction() {
		return slog.New(slog.NewJSONHandler(os.Stderr, nil))
	}
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
}
