package daemon

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/messenger/composite"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/messenger/inbox"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/messenger/panep"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/runtime/zellij"
	"github.com/aoagents/agent-orchestrator/backend/internal/cdc"
	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/lifecycle"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/project"
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

	if err := store.Upsert(ctx, project.Row{ID: "mer", Path: "/repo/mer"}); err != nil {
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

// TestWiring_SessionStackSharesSingletons asserts the daemon's wiring shape:
// startLifecycle and buildSessionStack share the same messenger and LCM, and
// the messenger reaches the same store the SM reads. Two LCMs would split
// agent-nudge state; two messengers would route inbox writes inconsistently.
//
// The pointer-identity check on ss.messenger proves buildSessionStack does not
// silently construct a second messenger; the end-to-end Send through a row the
// store owns proves the storeWorkspaceLookup is the same store SM uses.
func TestWiring_SessionStackSharesSingletons(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())

	store, err := sqlite.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	cfg := config.Config{DataDir: t.TempDir()}

	projects := project.NewManager(store)
	runtime := zellij.New(zellij.Options{})
	messenger := newSessionMessenger(store, runtime, nil)
	lcStack := startLifecycle(ctx, store, runtime, messenger, nil)
	// Cancel-then-Stop in order: Stop drains the reaper goroutine, which only
	// exits when ctx is cancelled. A naive `defer cancel(); defer lcStack.Stop()`
	// reverses this (defer is LIFO) and deadlocks.
	t.Cleanup(func() {
		cancel()
		lcStack.Stop()
	})

	if lcStack.lcm == nil {
		t.Fatal("lifecycleStack must expose its LCM so the SM can share it")
	}
	ss, err := buildSessionStack(cfg, store, runtime, projects, lcStack.lcm, messenger)
	if err != nil {
		t.Fatalf("buildSessionStack: %v", err)
	}
	if ss.svc == nil || ss.workspace == nil || ss.messenger == nil {
		t.Fatal("session stack must be fully populated")
	}
	if ss.messenger != messenger {
		t.Error("buildSessionStack must reuse the messenger it is given, not construct a second one")
	}
	// The daemon's session messenger is a composite: inbox first (durable
	// file write — must succeed), then panep (best-effort live pane ping).
	// Reversing this would tell the agent about a file the inbox messenger
	// failed to write.
	comp, ok := messenger.(*composite.Messenger)
	if !ok {
		t.Fatalf("session messenger should be *composite.Messenger, got %T", messenger)
	}
	if len(comp.Inner) != 2 {
		t.Fatalf("composite should wrap exactly 2 inner messengers (inbox + panep), got %d", len(comp.Inner))
	}
	if _, ok := comp.Inner[0].(*inbox.Messenger); !ok {
		t.Errorf("composite Inner[0] should be *inbox.Messenger, got %T", comp.Inner[0])
	}
	if _, ok := comp.Inner[1].(*panep.Messenger); !ok {
		t.Errorf("composite Inner[1] should be *panep.Messenger, got %T", comp.Inner[1])
	}

	// End-to-end: a session row in the shared store should be reachable through
	// the messenger that buildSessionStack wired up. A second store would
	// surface as "session not found" here.
	if err := store.Upsert(ctx, project.Row{ID: "p", Path: "/repo/p", RegisteredAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	workspaceDir := t.TempDir()
	rec, err := store.CreateSession(ctx, domain.SessionRecord{
		ProjectID: "p", Kind: domain.KindWorker,
		Activity: domain.Activity{State: domain.ActivityIdle, LastActivityAt: time.Now()},
		Metadata: domain.SessionMetadata{WorkspacePath: workspaceDir},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := ss.messenger.Send(ctx, rec.ID, "hello"); err != nil {
		t.Fatalf("messenger.Send through shared store lookup: %v", err)
	}
	entries, err := os.ReadDir(filepath.Join(workspaceDir, ".ao", "inbox"))
	if err != nil {
		t.Fatalf("inbox dir: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("want 1 inbox file, got %d", len(entries))
	}
}
