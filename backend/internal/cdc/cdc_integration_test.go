package cdc_test

import (
	"context"
	"os"
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
			Attempts: e.Attempts,
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

	// Seed N=2 snapshot events with distinct seqs ending at maxSeq, mirroring
	// the production snapshotSource (cdc_wiring.go) after the S3 fix. The
	// older "all events share Seq: maxSeq" shape would cause per-seq dedup in
	// the broadcaster's subscribers to drop N-1 of N — assert distinct seqs
	// below.
	snap := fakeSnapshot{
		events: []cdc.Event{
			{Seq: 4, SessionID: "s1", EventType: "session_snapshot"},
			{Seq: 5, SessionID: "s2", EventType: "session_snapshot"},
		},
		maxSeq: 5,
	}
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
	// Both snapshot events (seq 4 and 5) must be among the delivered events
	// with distinct seqs — the S3 fix means a snapshot of N sessions emits N
	// uniquely-keyed events instead of collapsing them all to maxSeq.
	seen := map[int64]bool{}
	for _, e := range got {
		seen[e.Seq] = true
	}
	if !seen[4] || !seen[5] {
		t.Fatalf("resync did not deliver every snapshot event (need seq 4 and 5); got %+v", got)
	}
}

// TestConsumerStartResyncsAcrossRestartWithRotation pins the B1 fix: across a
// process restart that straddles a JSONL rotation, the consumer's first Poll
// cannot detect the rotation (prevInfo nil, cursor 0). Without the fix every
// event in the rotated-out archive between lastSeq+1 and the rotation point
// is silently dropped from the live stream — only the final-state version
// reappears via a future snapshot (if rotation ever happens again).
//
// Scenario:
//  1. Consumer delivered seq 1..5 and durably stored offset = 5.
//  2. Publisher drained seq 6..10 to the same JSONL file (still active),
//     consumer was killed before processing them.
//  3. JSONL rotated: jsonl -> jsonl.1 (carries seq 1..10). Fresh jsonl gets
//     seq 11..15.
//  4. New consumer starts with offset = 5, prevInfo = nil, cursor = 0.
//     Without B1 it reads the fresh jsonl, sees seq 11..15 (all > lastSeq),
//     delivers them, and seq 6..10 are lost forever from the live stream.
//     With B1 it resyncs from the snapshot source first, so the gap is
//     closed (subscribers see at least the final state for each session
//     that had events in 6..10), lastSeq advances past the gap, and the
//     live stream resumes correctly.
func TestConsumerStartResyncsAcrossRestartWithRotation(t *testing.T) {
	ctx := context.Background()
	store := newStore(t)
	dir := t.TempDir()
	logPath := dir + "/" + cdc.LogFileName

	log, err := cdc.OpenLog(dir, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close()
	pub := cdc.NewPublisher(outboxAdapter{store}, log, cdc.PublisherConfig{})

	// 1) Seed 5 events (seq 1..5), drain to JSONL.
	for i := 0; i < 5; i++ {
		id := "s" + string(rune('1'+i))
		if err := store.Upsert(ctx, rec(id), ports.EventSessionCreated); err != nil {
			t.Fatalf("seed upsert: %v", err)
		}
	}
	if err := pub.Drain(ctx); err != nil {
		t.Fatalf("first drain: %v", err)
	}
	// Pre-seed the durable offset as if a prior consumer delivered seq 1..5.
	if err := store.SetOffset(ctx, "fe", 5, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}

	// 2) Publisher writes seq 6..10 to the *same* jsonl file (still active),
	//    but the consumer was killed before processing — they will end up
	//    stranded in the archive.
	for i := 0; i < 5; i++ {
		id := "u" + string(rune('1'+i))
		if err := store.Upsert(ctx, rec(id), ports.EventSessionCreated); err != nil {
			t.Fatalf("upsert pre-rotate: %v", err)
		}
	}
	if err := pub.Drain(ctx); err != nil {
		t.Fatalf("drain pre-rotate: %v", err)
	}

	// 3) Rotate jsonl -> jsonl.1 (carries seq 1..10) and create a fresh
	//    active jsonl. Then write seq 11..15 to the new active file.
	if err := log.Close(); err != nil {
		t.Fatalf("close log for rotate: %v", err)
	}
	archive := logPath + ".1"
	_ = os.Remove(archive)
	if err := os.Rename(logPath, archive); err != nil {
		t.Fatalf("rotate: %v", err)
	}
	freshLog, err := cdc.OpenLog(dir, 0)
	if err != nil {
		t.Fatalf("reopen fresh log: %v", err)
	}
	defer freshLog.Close()
	freshPub := cdc.NewPublisher(outboxAdapter{store}, freshLog, cdc.PublisherConfig{})
	for i := 0; i < 5; i++ {
		id := "v" + string(rune('1'+i))
		if err := store.Upsert(ctx, rec(id), ports.EventSessionCreated); err != nil {
			t.Fatalf("upsert post-rotate: %v", err)
		}
	}
	if err := freshPub.Drain(ctx); err != nil {
		t.Fatalf("drain post-rotate: %v", err)
	}

	// 4) Bring up a fresh consumer with offset = 5 and prevInfo = nil. The
	//    snapshot source reports the full live-state seq frontier (15).
	//    Without B1 the resync never fires on startup; with B1 it does and
	//    advances lastSeq past the rotated-out gap.
	snap := fakeSnapshot{
		events: []cdc.Event{
			// One snapshot event per live session at the seq frontier
			// (covers the seq 6..10 archive gap by terminal state).
			{Seq: 11, SessionID: "u1", EventType: "session_snapshot"},
			{Seq: 12, SessionID: "u2", EventType: "session_snapshot"},
			{Seq: 13, SessionID: "u3", EventType: "session_snapshot"},
			{Seq: 14, SessionID: "u4", EventType: "session_snapshot"},
			{Seq: 15, SessionID: "u5", EventType: "session_snapshot"},
		},
		maxSeq: 15,
	}
	bc := cdc.NewBroadcaster()
	var got []cdc.Event
	bc.Subscribe(func(e cdc.Event) { got = append(got, e) })
	con := cdc.NewConsumer("fe", logPath, store, bc, cdc.ConsumerConfig{Snapshot: snap})
	if _, err := con.Start(ctx); err != nil {
		t.Fatalf("start consumer: %v", err)
	}
	if err := con.Poll(ctx); err != nil {
		t.Fatalf("poll consumer: %v", err)
	}

	// B1 invariant: the consumer MUST have resynced from the snapshot on
	// startup, otherwise seq 6..10 are silently dropped (they were in the
	// archive; the fresh jsonl only holds seq 11..15). With the fix, the
	// snapshot events (which carry the live-state frontier up to seq 15)
	// are delivered before the live stream resumes. Without the fix, only
	// seq 11..15 from the fresh file are delivered, with no signal that
	// seq 6..10 ever existed.
	sawSnapshot := false
	for _, e := range got {
		if e.EventType == "session_snapshot" {
			sawSnapshot = true
			break
		}
	}
	if !sawSnapshot {
		t.Fatal("B1: consumer did not resync from snapshot on startup; events in the rotated-out archive (seq 6..10) are silently dropped")
	}

	// Offset must have advanced to at least 15 (the snapshot maxSeq); the
	// rotated-out archive's seq 6..10 are covered transitively by the
	// snapshot's final-state events.
	off, _ := store.GetOffset(ctx, "fe")
	if off < 15 {
		t.Fatalf("B1: offset after resync = %d, want >= 15", off)
	}
}
