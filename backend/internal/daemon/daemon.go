// Package daemon owns the Agent Orchestrator backend process: config loading,
// loopback HTTP serving, durable storage, CDC fan-out, lifecycle wiring, and
// graceful shutdown.
package daemon

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/runtime/tmux"
	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd"
	"github.com/aoagents/agent-orchestrator/backend/internal/runfile"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
	"github.com/aoagents/agent-orchestrator/backend/internal/terminal"
)

// Run starts the daemon and blocks until it exits. SIGINT/SIGTERM drive
// graceful shutdown through the HTTP server and background workers.
func Run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	log := newLogger()

	// Fail fast if a live daemon already owns the handshake file. A run-file
	// left by a crashed predecessor (dead PID) is treated as stale and
	// overwritten when the new server starts.
	if live, err := runfile.CheckStale(cfg.RunFilePath); err != nil {
		return fmt.Errorf("inspect run-file: %w", err)
	} else if live != nil {
		return fmt.Errorf("daemon already running (pid %d, port %d); refusing to start", live.PID, live.Port)
	}

	// Open the durable store and bring up the CDC substrate: the DB triggers
	// capture changes into change_log, the poller tails it, and the broadcaster
	// fans events out to the SSE transport. The LCM/Session Manager and the HTTP
	// API routes that drive and read this store are owned by the daemon lane and
	// are wired there once their collaborators (Notifier, AgentMessenger, and the
	// runtime/agent/workspace plugins) have production implementations; here we
	// stand up the persistence + change-delivery foundation they build on.
	store, err := sqlite.Open(cfg.DataDir)
	if err != nil {
		return fmt.Errorf("open store: %w", err)
	}
	defer func() { _ = store.Close() }()

	// signal.NotifyContext cancels ctx on SIGINT/SIGTERM, which drives the
	// graceful shutdown inside Server.Run and stops the background goroutines.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	cdcPipe, err := startCDC(ctx, store, log)
	if err != nil {
		return err
	}

	// Terminal streaming: the tmux runtime supplies the PTY-attach command and
	// liveness; the CDC broadcaster feeds the session-state channel. The manager
	// is handed to httpd, which mounts it at /mux. Raw PTY bytes never flow
	// through the CDC change_log — only session-state events do.
	runtimeAdapter := tmux.New(tmux.Options{})
	termMgr := terminal.NewManager(runtimeAdapter, cdcPipe.Broadcaster, log)
	defer termMgr.Close()

	srv, err := httpd.New(cfg, log, termMgr)
	if err != nil {
		return err
	}

	// Bring up the Lifecycle Manager (sole store writer) and the reaper (OBSERVE
	// timer). This makes the write path live end-to-end: LCM write -> store -> DB
	// trigger -> change_log -> poller -> broadcaster.
	lcStack := startLifecycle(ctx, store, log)

	// Bring up the Session Manager. Runtime (tmux) and Workspace (gitworktree)
	// are real on main; ports.Agent has no production adapter yet, so a loud
	// stub returns a sentinel command that makes any Spawn fail at the runtime
	// layer rather than start a broken session quietly. Notifier and
	// AgentMessenger remain stubbed alongside the LCM until their multiplexers
	// land. No HTTP routes wire to this yet — the daemon lane (#10) owns API
	// surfacing — so we hold the SM in a local until it does.
	sStack, err := startSession(ctx, cfg, lcStack, log)
	if err != nil {
		// startSession is the first start* call after this point that can
		// realistically fail while the cdc poller and the reaper are already
		// running. Mirror the bottom-of-run shutdown sequence so both have
		// drained before the deferred store.Close() fires. Defers would hit
		// the LIFO trap (see comment after srv.Run), hence explicit.
		stop()
		lcStack.Stop()
		if cdcErr := cdcPipe.Stop(); cdcErr != nil {
			log.Error("cdc pipeline shutdown", "err", cdcErr)
		}
		return err
	}
	_ = sStack

	runErr := srv.Run(ctx)

	// Shut the background goroutines down in order: cancel the context FIRST so
	// their loops exit, then wait for them to drain. Doing this explicitly (not
	// via defer) avoids the LIFO trap where a Stop() that blocks on ctx-cancel
	// runs before the cancel — which would hang any non-signal exit path.
	stop()
	lcStack.Stop()
	if err := cdcPipe.Stop(); err != nil {
		log.Error("cdc pipeline shutdown", "err", err)
	}
	return runErr
}

// newLogger returns the daemon's slog logger. It writes to stderr so supervisors
// can capture it separately from any structured stdout protocol added later.
func newLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
}
