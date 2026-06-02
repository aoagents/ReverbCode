package daemon

import (
	"context"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters"
	"github.com/aoagents/agent-orchestrator/backend/internal/cdc"
	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/lifecycle"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
)

// TestWiring_WriteFlowsToBroadcaster exercises the real boot path end to end:
// a lifecycle write -> sqlite -> DB trigger -> change_log -> CDC poller ->
// broadcaster, through the same cdc.Source implementation the daemon uses.
func TestWiring_WriteFlowsToBroadcaster(t *testing.T) {
	ctx := context.Background()
	store, err := sqlite.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	lcm := lifecycle.New(store, nil)

	bcast := cdc.NewBroadcaster()
	poller := cdc.NewPoller(store, bcast, cdc.PollerConfig{})
	if err := poller.SeekToHead(ctx); err != nil {
		t.Fatal(err)
	}

	var mu sync.Mutex
	var got []cdc.Event
	bcast.Subscribe(func(e cdc.Event) { mu.Lock(); got = append(got, e); mu.Unlock() })

	if err := store.UpsertProject(ctx, domain.ProjectRecord{ID: "mer", Path: "/repo/mer"}); err != nil {
		t.Fatal(err)
	}
	rec, err := store.CreateSession(ctx, domain.SessionRecord{
		ProjectID: "mer", Kind: domain.KindWorker,
		Activity: domain.Activity{State: domain.ActivityIdle, LastActivityAt: time.Now()},
	})
	if err != nil {
		t.Fatal(err)
	}
	// A real transition through the engine, which writes the row and fires the
	// activity_state/is_terminated CDC trigger.
	if err := lcm.ApplyActivitySignal(ctx, rec.ID, ports.ActivitySignal{Valid: true, State: domain.ActivityActive, Timestamp: time.Now()}); err != nil {
		t.Fatal(err)
	}

	if err := poller.Poll(ctx); err != nil {
		t.Fatal(err)
	}

	mu.Lock()
	defer mu.Unlock()
	var sawSession bool
	for _, e := range got {
		if e.SessionID == string(rec.ID) {
			sawSession = true
		}
	}
	if !sawSession {
		t.Fatalf("expected a change_log event for %s to reach the broadcaster, got %d events", rec.ID, len(got))
	}
}

// TestWiring_AgentResolverResolvesRealAdapters asserts buildAgentResolver wires a
// real registry-backed per-session resolver: each harness resolves to the
// matching registered adapter, an empty harness falls back to the AO_AGENT
// default, and an unknown harness misses.
func TestWiring_AgentResolverResolvesRealAdapters(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	resolver, err := buildAgentResolver("", log) // empty default → claude-code
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		harness domain.AgentHarness
		wantID  string
	}{
		{domain.HarnessClaudeCode, "claude-code"},
		{domain.HarnessCodex, "codex"},
		{"", config.DefaultAgent}, // empty harness falls back to the AO_AGENT default
	} {
		agent, ok := resolver.Agent(tc.harness)
		if !ok {
			t.Fatalf("resolver has no agent for harness %q", tc.harness)
		}
		described, ok := agent.(adapters.Adapter)
		if !ok {
			t.Fatalf("agent for harness %q is %T, not a registered adapters.Adapter", tc.harness, agent)
		}
		if got := described.Manifest().ID; got != tc.wantID {
			t.Fatalf("harness %q resolved to adapter %q, want %q", tc.harness, got, tc.wantID)
		}
	}
	if _, ok := resolver.Agent("definitely-not-an-agent"); ok {
		t.Fatal("unknown harness resolved to an agent; want a miss")
	}
}
