package daemon

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/claudecode"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/codex"
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
// gitworktree Workspace, the LCM and store from startLifecycle, and the agent
// adapter selected by cfg.Agent from the registry (see buildAgentRegistry). The
// Messenger remains a stub until the runtime/agent nudge path lands. It does NOT
// mount any HTTP routes — those come with the daemon lane (#10). Returning the
// SM here lets main hold the wired instance so future route wiring is a
// one-line plumb-through.
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

	agents, err := buildAgentResolver(cfg.Agent, log)
	if err != nil {
		return nil, err
	}

	sm := session.New(session.Deps{
		Runtime:   runtime,
		Agents:    agents,
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

// buildAgentRegistry returns a registry populated with the agent adapters the
// daemon ships. Each implements ports.Agent and is keyed by its manifest id, so
// cfg.Agent can select one without the daemon hard-coding a concrete type.
// Registration only fails on an empty/duplicate id — a programmer error, not a
// runtime condition — so it surfaces as a startup error.
func buildAgentRegistry() (*adapters.Registry, error) {
	reg := adapters.NewRegistry()
	for _, a := range []adapters.Adapter{claudecode.New(), codex.New()} {
		if err := reg.Register(a); err != nil {
			return nil, fmt.Errorf("register agent adapter %q: %w", a.Manifest().ID, err)
		}
	}
	return reg, nil
}

// agentRegistry adapts the generic adapter Registry to ports.AgentResolver: it
// maps a session's harness onto the registered adapter of the same id and
// asserts that adapter drives an agent. An empty harness falls back to the
// daemon's configured default (AO_AGENT), so a spawn that names no harness still
// gets a real agent.
type agentRegistry struct {
	reg            *adapters.Registry
	defaultHarness domain.AgentHarness
}

var _ ports.AgentResolver = agentRegistry{}

func (a agentRegistry) Agent(harness domain.AgentHarness) (ports.Agent, bool) {
	if harness == "" {
		harness = a.defaultHarness
	}
	adapter, ok := a.reg.Get(string(harness))
	if !ok {
		return nil, false
	}
	agent, ok := adapter.(ports.Agent)
	return agent, ok
}

// buildAgentResolver constructs the per-session agent resolver: a registry of the
// shipped adapters plus the configured default harness. It fails fast if the
// default does not resolve, so a typo'd AO_AGENT surfaces at startup rather than
// at the first spawn that omits a harness.
func buildAgentResolver(defaultAgent string, log *slog.Logger) (ports.AgentResolver, error) {
	if defaultAgent == "" {
		defaultAgent = config.DefaultAgent
	}
	reg, err := buildAgentRegistry()
	if err != nil {
		return nil, err
	}
	resolver := agentRegistry{reg: reg, defaultHarness: domain.AgentHarness(defaultAgent)}
	if _, ok := resolver.Agent(""); !ok {
		return nil, fmt.Errorf("configured default agent %q is not a registered adapter", defaultAgent)
	}
	ids := make([]string, 0)
	for _, mf := range reg.Manifests() {
		ids = append(ids, mf.ID)
	}
	log.Info("wired per-session agent resolver", "default", defaultAgent, "registered", ids)
	return resolver, nil
}
