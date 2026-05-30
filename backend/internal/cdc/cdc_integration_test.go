package cdc_test

import (
	"context"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/cdc"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
)

// outboxAdapter bridges sqlite.Store's outbox methods to cdc.OutboxStore. This
// is the same glue the composition root (main.go) installs.
type outboxAdapter struct{ s *sqlite.Store }

func (a outboxAdapter) ListUnsent(ctx context.Context, limit int) ([]cdc.PendingEvent, error) {
	evs, err := a.s.ListUnsent(ctx, limit)
	if err != nil {
		return nil, err
	}
	out := make([]cdc.PendingEvent, len(evs))
	for i, e := range evs {
		out[i] = cdc.PendingEvent{
			OutboxID: e.OutboxID,
			Event: cdc.Event{
				Seq:       e.Seq,
				SessionID: e.SessionID,
				EventType: e.EventType,
				Revision:  e.Revision,
				Payload:   e.Payload,
				CreatedAt: e.CreatedAt,
			},
		}
	}
	return out, nil
}

func (a outboxAdapter) MarkSent(ctx context.Context, id int64, at time.Time) error {
	return a.s.MarkSent(ctx, id, at)
}
func (a outboxAdapter) MarkFailed(ctx context.Context, id int64, msg string) error {
	return a.s.MarkFailed(ctx, id, msg)
}

func newStore(t *testing.T) *sqlite.Store {
	t.Helper()
	db, err := sqlite.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return sqlite.NewStore(db)
}

func rec(id string) domain.SessionRecord {
	now := time.Now().UTC()
	return domain.SessionRecord{
		ID: domain.SessionID(id), ProjectID: "p", Kind: domain.KindWorker, CreatedAt: now, UpdatedAt: now,
		Lifecycle: domain.CanonicalSessionLifecycle{
			Session:  domain.SessionSubstate{State: domain.SessionWorking, Reason: domain.ReasonTaskInProgress},
			PR:       domain.PRSubstate{State: domain.PRNone, Reason: domain.PRReasonNotCreated},
			Runtime:  domain.RuntimeSubstate{State: domain.RuntimeAlive, Reason: domain.RuntimeReasonProcessRunning},
			Activity: domain.ActivitySubstate{State: domain.ActivityActive, LastActivityAt: now, Source: domain.SourceNative},
		},
	}
}

func TestEndToEndPublishConsume(t *testing.T) {
	ctx := context.Background()
	store := newStore(t)
	dir := t.TempDir()
	log, err := cdc.OpenLog(dir, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close()

	// Three canonical writes => three outbox rows, seq 1..3.
	r := rec("s1")
	if err := store.Upsert(ctx, r, ports.EventSessionCreated); err != nil {
		t.Fatal(err)
	}
	r.Lifecycle.Revision = 1
	if err := store.Upsert(ctx, r, ports.EventSessionStateChanged); err != nil {
		t.Fatal(err)
	}
	r.Lifecycle.Revision = 2
	if err := store.Upsert(ctx, r, ports.EventSessionStateChanged); err != nil {
		t.Fatal(err)
	}

	pub := cdc.NewPublisher(outboxAdapter{store}, log, cdc.PublisherConfig{})
	if err := pub.Drain(ctx); err != nil {
		t.Fatalf("drain: %v", err)
	}

	var got []cdc.Event
	bc := cdc.NewBroadcaster()
	bc.Subscribe(func(e cdc.Event) { got = append(got, e) })

	con := cdc.NewConsumer("fe", dir+"/"+cdc.LogFileName, store, bc, cdc.ConsumerConfig{})
	if _, err := con.Start(ctx); err != nil {
		t.Fatal(err)
	}
	// Drive one poll synchronously instead of waiting on the goroutine.
	if err := con.Poll(ctx); err != nil {
		t.Fatalf("poll: %v", err)
	}

	if len(got) != 3 {
		t.Fatalf("delivered %d events, want 3", len(got))
	}
	for i, e := range got {
		if e.Seq != int64(i+1) {
			t.Fatalf("event %d has seq %d, want %d", i, e.Seq, i+1)
		}
	}
	if got[0].EventType != string(ports.EventSessionCreated) {
		t.Fatalf("first event type = %q", got[0].EventType)
	}

	// Idempotency: a second poll with no new bytes delivers nothing more.
	if err := con.Poll(ctx); err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("re-poll delivered extra events: %d", len(got))
	}

	// Offset persisted at seq 3.
	off, _ := store.GetOffset(ctx, "fe")
	if off != 3 {
		t.Fatalf("offset = %d, want 3", off)
	}

	// Janitor: consumer ACKed 3, so all sent rows with seq <= 3 are reclaimed.
	// The watermark itself is fully delivered and safe to delete.
	jan := cdc.NewJanitor(store, cdc.JanitorConfig{})
	deleted, err := jan.Sweep(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if deleted != 3 {
		t.Fatalf("janitor deleted %d, want 3 (seq 1,2,3 <= watermark 3)", deleted)
	}
}

// TestJanitorSweepIncludesWatermarkRow exercises the fix for the off-by-one in
// DeleteSentOutboxBelow. With a strict `<` the row at the watermark seq lingers
// forever in a quiet system; with `<=` a single-ACK system vacuums fully.
func TestJanitorSweepIncludesWatermarkRow(t *testing.T) {
	ctx := context.Background()
	store := newStore(t)
	dir := t.TempDir()
	log, err := cdc.OpenLog(dir, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close()

	// Exactly one event -> one outbox row.
	if err := store.Upsert(ctx, rec("s1"), ports.EventSessionCreated); err != nil {
		t.Fatal(err)
	}
	pub := cdc.NewPublisher(outboxAdapter{store}, log, cdc.PublisherConfig{})
	if err := pub.Drain(ctx); err != nil {
		t.Fatal(err)
	}
	// Consumer ACKs seq 1.
	if err := store.SetOffset(ctx, "fe", 1, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}

	jan := cdc.NewJanitor(store, cdc.JanitorConfig{})
	deleted, err := jan.Sweep(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if deleted != 1 {
		t.Fatalf("janitor deleted %d, want 1 (seq 1 <= watermark 1)", deleted)
	}
	// Outbox should now be empty; nothing further to vacuum.
	deleted, err = jan.Sweep(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if deleted != 0 {
		t.Fatalf("second sweep deleted %d, want 0", deleted)
	}
}

func TestConsumerRestartSkipsDelivered(t *testing.T) {
	ctx := context.Background()
	store := newStore(t)
	dir := t.TempDir()
	log, _ := cdc.OpenLog(dir, 0)
	defer log.Close()

	if err := store.Upsert(ctx, rec("s1"), ports.EventSessionCreated); err != nil {
		t.Fatal(err)
	}
	pub := cdc.NewPublisher(outboxAdapter{store}, log, cdc.PublisherConfig{})
	if err := pub.Drain(ctx); err != nil {
		t.Fatal(err)
	}

	// Pre-seed the durable offset as if a prior consumer already delivered seq 1.
	if err := store.SetOffset(ctx, "fe", 1, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}

	var got []cdc.Event
	bc := cdc.NewBroadcaster()
	bc.Subscribe(func(e cdc.Event) { got = append(got, e) })
	con := cdc.NewConsumer("fe", dir+"/"+cdc.LogFileName, store, bc, cdc.ConsumerConfig{})
	if _, err := con.Start(ctx); err != nil {
		t.Fatal(err)
	}
	if err := con.Poll(ctx); err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("restart re-delivered already-acked events: %d", len(got))
	}
}

// fakeSnapshot stands in for the sessions-table snapshot source on resync.
type fakeSnapshot struct {
	events []cdc.Event
	maxSeq int64
}

func (f fakeSnapshot) Snapshot(context.Context) ([]cdc.Event, int64, error) {
	return f.events, f.maxSeq, nil
}

func TestRotationTriggersResync(t *testing.T) {
	ctx := context.Background()
	store := newStore(t)
	dir := t.TempDir()
	// Tiny cap so a couple of writes force a rotation.
	log, err := cdc.OpenLog(dir, 80)
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close()

	var got []cdc.Event
	bc := cdc.NewBroadcaster()
	bc.Subscribe(func(e cdc.Event) { got = append(got, e) })

	snap := fakeSnapshot{events: []cdc.Event{{Seq: 5, SessionID: "s1", EventType: "session_updated"}}, maxSeq: 5}
	con := cdc.NewConsumer("fe", dir+"/"+cdc.LogFileName, store, bc, cdc.ConsumerConfig{Snapshot: snap})
	if _, err := con.Start(ctx); err != nil {
		t.Fatal(err)
	}

	pub := cdc.NewPublisher(outboxAdapter{store}, log, cdc.PublisherConfig{})

	// First write + drain + poll: consumer reads it and advances its cursor.
	if err := store.Upsert(ctx, rec("s1"), ports.EventSessionCreated); err != nil {
		t.Fatal(err)
	}
	if err := pub.Drain(ctx); err != nil {
		t.Fatal(err)
	}
	if err := con.Poll(ctx); err != nil {
		t.Fatal(err)
	}
	cursorBefore := len(got)

	// Force rotation by writing past the cap, then poll: the file shrank, so the
	// consumer must resync from the snapshot source.
	r := rec("s1")
	r.Lifecycle.Revision = 1
	if err := store.Upsert(ctx, r, ports.EventSessionStateChanged); err != nil {
		t.Fatal(err)
	}
	if err := pub.Drain(ctx); err != nil {
		t.Fatal(err)
	}
	if err := con.Poll(ctx); err != nil {
		t.Fatal(err)
	}

	if len(got) <= cursorBefore {
		t.Fatal("expected resync to deliver the snapshot event")
	}
	// The snapshot event (seq 5) must be among the delivered events.
	var sawSnapshot bool
	for _, e := range got {
		if e.Seq == 5 {
			sawSnapshot = true
		}
	}
	if !sawSnapshot {
		t.Fatalf("resync did not deliver snapshot event; got %+v", got)
	}
}
