package daemon

import (
	"context"
	"log/slog"

	"github.com/aoagents/agent-orchestrator/backend/internal/lifecycle"
	"github.com/aoagents/agent-orchestrator/backend/internal/observe/reaper"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
)

// lifecycleStack owns the Lifecycle Manager (which the Session Manager and the
// reaper both depend on) and the reaper goroutine.
type lifecycleStack struct {
	lcm        *lifecycle.Manager
	reaperDone <-chan struct{}
}

// startLifecycle constructs the Lifecycle Manager over the store and starts the
// reaper. The messenger is passed into the LCM so PR-driven reactions (CI fail,
// review feedback, merge conflict) can nudge the agent. The goroutine stops
// when ctx is cancelled; Stop waits for it to drain.
func startLifecycle(ctx context.Context, store *sqlite.Store, runtime ports.Runtime, messenger ports.AgentMessenger, logger *slog.Logger) *lifecycleStack {
	lcm := lifecycle.New(store, messenger)
	rp := reaper.New(lcm, store, runtime, reaper.Config{Logger: logger})
	return &lifecycleStack{lcm: lcm, reaperDone: rp.Start(ctx)}
}

// Stop waits for the reaper goroutine to exit. The caller must cancel the ctx
// passed to startLifecycle before calling Stop.
func (l *lifecycleStack) Stop() { <-l.reaperDone }
