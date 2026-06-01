package daemon

import (
	"context"
	"log/slog"
	"path/filepath"
	"sync"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/runtime/tmux"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/workspace/gitworktree"
	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/lifecycle"
	"github.com/aoagents/agent-orchestrator/backend/internal/notification"
	"github.com/aoagents/agent-orchestrator/backend/internal/observe/reaper"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/session"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
)

// lifecycleStack owns the running LCM + reaper. The LCM is the sole writer of
// canonical transitions; the reaper is the OBSERVE-layer timer that probes live
// runtimes and reports facts back through it. Store is exposed so the Session
// Manager construction in startSession can plug the same SessionStore + PRWriter
// instance the LCM already holds (*sqlite.Store satisfies both ports directly).
type lifecycleStack struct {
	LCM        *lifecycle.Manager
	Store      *sqlite.Store
	reaperDone <-chan struct{}
}

// startLifecycle constructs the LCM over the store adapter and starts the reaper.
// The goroutine stops when ctx is cancelled; Stop waits for it to drain.
//
// TEMPORARY STUBS (replace as the daemon lane lands the collaborators):
//   - noopMessenger — swap for the runtime/agent-plugin-backed AgentMessenger.
//   - reaper.MapRegistry{} — empty runtime registry, so the reaper ticks
//     escalations but probes nothing until the runtime plugins exist.
func startLifecycle(ctx context.Context, store *sqlite.Store, logger *slog.Logger) *lifecycleStack {
	renderer := notification.NewRenderer(store)
	notifier := notification.NewEnqueuer(store, renderer, logger)
	lcm := lifecycle.New(store, store, notifier, noopMessenger{})
	rp := reaper.New(lcm, reaper.MapRegistry{}, reaper.Config{Logger: logger})
	return &lifecycleStack{LCM: lcm, Store: store, reaperDone: rp.Start(ctx)}
}

// Stop waits for the reaper goroutine to exit (the caller must have cancelled the
// ctx passed to startLifecycle).
func (l *lifecycleStack) Stop() { <-l.reaperDone }

// sessionStack holds the daemon's live Session Manager. It mirrors
// lifecycleStack's shape so a future teardown hook (worktree drain, runtime
// shutdown) has a place to attach.
type sessionStack struct {
	SM *session.Manager
}

// startSession constructs the Session Manager over the real tmux Runtime and
// gitworktree Workspace, the LCM and adapter created by startLifecycle, and the
// loud-stub Agent / Messenger / Notifier ports that have no production
// implementations yet. It does NOT mount any HTTP routes — those come with the
// daemon lane (#10). Returning the SM here lets main hold the wired-but-quiet
// instance so future route wiring is a one-line plumb-through.
func startSession(ctx context.Context, cfg config.Config, ls *lifecycleStack, log *slog.Logger) (*sessionStack, error) {
	_ = ctx // reserved for future ctx-aware plugin construction; today's tmux/gitworktree constructors are synchronous.
	runtime := tmux.New(tmux.Options{})

	ws, err := gitworktree.New(gitworktree.Options{
		// ManagedRoot is the directory under which per-session worktrees are
		// materialised. Co-located with the SQLite DB so a single AO_DATA_DIR
		// override moves all durable per-user state together.
		ManagedRoot: filepath.Join(cfg.DataDir, "worktrees"),
		// An empty resolver fails every project lookup with a clear
		// `no repo configured for project %q` error. That's the right loud
		// failure until the projects table feeds repo paths into the resolver
		// — hard-coding a single repo here would silently misroute spawns.
		RepoResolver: gitworktree.StaticRepoResolver{},
	})
	if err != nil {
		return nil, err
	}

	agent := newNoopAgent(log)

	sm := session.New(session.Deps{
		Runtime:   runtime,
		Agent:     agent,
		Workspace: ws,
		Store:     ls.Store,
		Messenger: noopMessenger{},
		Lifecycle: ls.LCM,
	})

	return &sessionStack{SM: sm}, nil
}

// noopMessenger is a TEMPORARY stub (see startLifecycle): the canonical write
// path and durable notifications work without it; only live agent nudges are
// absent until the real runtime/agent plugin is wired.
type noopMessenger struct{}

func (noopMessenger) Send(context.Context, domain.SessionID, string) error { return nil }

// agentNotWiredSentinel is the launch / restore command (and env-var key)
// noopAgent returns. tmux will try to exec a binary named exactly this and fail
// fast, so a Spawn against the loud stub surfaces a clear runtime error rather
// than starting a quiet, broken session.
const agentNotWiredSentinel = "AO_AGENT_HARNESS_NOT_WIRED"

// noopAgent is a loud stub for ports.Agent. There is no production Agent
// adapter on main yet; rather than panic at construction, this struct lets the
// daemon stand up the Session Manager, then logs a single warning the first
// time any SM call route through it and returns sentinel commands that make
// the runtime layer fail loudly.
type noopAgent struct {
	log  *slog.Logger
	once *sync.Once
}

var _ ports.Agent = (*noopAgent)(nil)

func newNoopAgent(log *slog.Logger) *noopAgent {
	return &noopAgent{log: log, once: &sync.Once{}}
}

func (n *noopAgent) warn() {
	n.once.Do(func() {
		n.log.Warn(
			"agent harness not wired: Spawn/Restore will fail at the runtime layer until a ports.Agent adapter is built",
			"sentinel", agentNotWiredSentinel,
			"next_step", "implement a per-harness ports.Agent adapter and plug it into startSession",
		)
	})
}

func (n *noopAgent) GetLaunchCommand(ports.AgentConfig) string {
	n.warn()
	return agentNotWiredSentinel
}

func (n *noopAgent) GetEnvironment(ports.AgentConfig) map[string]string {
	n.warn()
	return map[string]string{agentNotWiredSentinel: "1"}
}

func (n *noopAgent) GetRestoreCommand(string) string {
	n.warn()
	return agentNotWiredSentinel
}
