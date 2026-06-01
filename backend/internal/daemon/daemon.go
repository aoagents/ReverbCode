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

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/messenger/inbox"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/runtime/zellij"
	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd"
	"github.com/aoagents/agent-orchestrator/backend/internal/project"
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

	// Open the durable store and bring up the CDC substrate: DB triggers capture
	// changes into change_log, the poller tails it, and the broadcaster fans
	// events out to live transports.
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

	// Singletons shared across the daemon. Constructing each exactly once and
	// passing the same instance everywhere prevents the multi-zellij-socket /
	// dual-LCM / dual-project-store hazards that fragmented adapters create.
	runtimeAdapter := zellij.New(zellij.Options{})
	projects := project.NewManager(store)
	messenger := inbox.New(newStoreWorkspaceLookup(store))

	// Terminal streaming: the Zellij runtime supplies the PTY-attach command and
	// liveness; the CDC broadcaster feeds the session-state channel. The manager
	// is handed to httpd, which mounts it at /mux. Raw PTY bytes never flow
	// through the CDC change_log — only session-state events do.
	termMgr := terminal.NewManager(runtimeAdapter, cdcPipe.Broadcaster, log)
	defer termMgr.Close()

	// Bring up the Lifecycle Manager + reaper, then the Session Manager stack
	// over the same lcm/runtime/projects/messenger singletons. SM is constructed
	// before the HTTP server so its Spawner can be plumbed into APIDeps and the
	// /api/v1/sessions controller can drive it.
	lcStack := startLifecycle(ctx, store, runtimeAdapter, messenger, log)
	ss, err := buildSessionStack(cfg, store, runtimeAdapter, projects, lcStack.lcm, messenger)
	if err != nil {
		stop()
		lcStack.Stop()
		if cdcErr := cdcPipe.Stop(); cdcErr != nil {
			log.Error("cdc pipeline shutdown", "err", cdcErr)
		}
		return err
	}

	srv, err := httpd.NewWithDeps(cfg, log, termMgr, httpd.APIDeps{Projects: projects, Sessions: ss.sm})
	if err != nil {
		stop()
		lcStack.Stop()
		if cdcErr := cdcPipe.Stop(); cdcErr != nil {
			log.Error("cdc pipeline shutdown", "err", cdcErr)
		}
		return err
	}

	// SCM observation: polling Provider -> pr.Manager -> lifecycle nudges.
	// Constructed after lifecycle so the PR Manager can forward observations
	// to ApplyPRObservation; runs alongside the reaper as a sibling background
	// loop. Missing GITHUB_TOKEN degrades gracefully (loop is not started).
	scmStk := startSCM(ctx, store, projects, lcStack.lcm, log)

	runErr := srv.Run(ctx)

	// Shut the background goroutines down in order: cancel the context FIRST so
	// their loops exit, then wait for them to drain. Doing this explicitly (not
	// via defer) avoids the LIFO trap where a Stop() that blocks on ctx-cancel
	// runs before the cancel — which would hang any non-signal exit path.
	stop()
	scmStk.Stop()
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
