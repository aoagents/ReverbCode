package daemon

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/cdc"
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
		Activity: domain.ActivitySubstate{State: domain.ActivityIdle, LastActivityAt: time.Now(), Source: domain.SourceNone},
	})
	if err != nil {
		t.Fatal(err)
	}
	// A real transition through the engine, which writes the row and fires the
	// activity_state/is_terminated CDC trigger.
	if err := lcm.ApplyActivitySignal(ctx, rec.ID, ports.ActivitySignal{Valid: true, State: domain.ActivityActive, Timestamp: time.Now(), Source: domain.SourceNative}); err != nil {
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
