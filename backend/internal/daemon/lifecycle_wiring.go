package daemon

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/claudecode"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/codex"
	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/lifecycle"
	"github.com/aoagents/agent-orchestrator/backend/internal/observe/reaper"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
)

// lifecycleStack owns the runtime reaper goroutine started with the lifecycle
// reducer. The reducer itself is only used for wiring observations into storage.
type lifecycleStack struct {
	reaperDone <-chan struct{}
}

// startLifecycle constructs the Lifecycle Manager over the store and starts the
// reaper. The goroutine stops when ctx is cancelled; Stop waits for it to drain.
func startLifecycle(ctx context.Context, store *sqlite.Store, runtime ports.Runtime, logger *slog.Logger) *lifecycleStack {
	lcm := lifecycle.New(store, nil)
	rp := reaper.New(lcm, store, runtime, reaper.Config{Logger: logger})
	return &lifecycleStack{reaperDone: rp.Start(ctx)}
}

// Stop waits for the reaper goroutine to exit. The caller must cancel the ctx
// passed to startLifecycle before calling Stop.
func (l *lifecycleStack) Stop() { <-l.reaperDone }

// buildAgentRegistry returns a registry populated with the agent adapters the
// daemon ships, keyed by manifest id. Registration only fails on an
// empty/duplicate id — a programmer error, not a runtime condition.
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

// buildAgentResolver constructs the per-session agent resolver the Session
// Manager consumes (sessionmanager.Deps.Agents): a registry of the shipped
// adapters plus the configured default harness. It fails fast if the default
// does not resolve, so a typo'd AO_AGENT surfaces at startup. The session lane
// plugs this in when it mounts the controller-facing session service at the
// httpd APIDeps.Sessions slot.
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
	log.Info("built per-session agent resolver", "default", defaultAgent, "registered", ids)
	return resolver, nil
}
