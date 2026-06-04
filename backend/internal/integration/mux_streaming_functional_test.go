package integration

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"

	"github.com/aoagents/agent-orchestrator/backend/internal/cdc"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
	"github.com/aoagents/agent-orchestrator/backend/internal/terminal"
)

// testSessionSource implements terminal.SessionSource over the sqlite store,
// replicating the production daemonSessionSource without importing the daemon package.
type testSessionSource struct{ store *sqlite.Store }

func (s *testSessionSource) AllSessions(ctx context.Context) ([]domain.Session, error) {
	recs, err := s.store.ListAllSessions(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]domain.Session, 0, len(recs))
	for _, rec := range recs {
		out = append(out, s.toSession(ctx, rec))
	}
	return out, nil
}

func (s *testSessionSource) Session(ctx context.Context, id domain.SessionID) (domain.Session, bool, error) {
	rec, ok, err := s.store.GetSession(ctx, id)
	if err != nil || !ok {
		return domain.Session{}, ok, err
	}
	return s.toSession(ctx, rec), true, nil
}

func (s *testSessionSource) toSession(ctx context.Context, rec domain.SessionRecord) domain.Session {
	pr, ok, _ := s.store.GetDisplayPRFactsForSession(ctx, rec.ID)
	if ok {
		return domain.Session{SessionRecord: rec, Status: domain.DeriveStatus(rec, &pr)}
	}
	return domain.Session{SessionRecord: rec, Status: domain.DeriveStatus(rec, nil)}
}

// muxMsg is the on-wire frame shape for both directions. Using raw maps keeps
// the test decoupled from the internal protocol types.
type muxMsg map[string]any

func sendMux(ctx context.Context, t *testing.T, c *websocket.Conn, msg muxMsg) {
	t.Helper()
	if err := wsjson.Write(ctx, c, msg); err != nil {
		t.Fatalf("mux write: %v", err)
	}
}

// recvMux reads frames until one matching ch+typ is found (draining others).
func recvMux(ctx context.Context, t *testing.T, c *websocket.Conn, ch, typ string) muxMsg {
	t.Helper()
	deadline, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	for {
		var raw json.RawMessage
		if err := wsjson.Read(deadline, c, &raw); err != nil {
			t.Fatalf("mux read waiting for %s/%s: %v", ch, typ, err)
		}
		var m muxMsg
		if err := json.Unmarshal(raw, &m); err != nil {
			t.Fatalf("mux unmarshal: %v", err)
		}
		if m["ch"] == ch && m["type"] == typ {
			return m
		}
	}
}

func discardLog() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// noopPTYSource satisfies terminal.PTYSource without spawning real PTYs.
type noopPTYSource struct{}

func (noopPTYSource) AttachCommand(ports.RuntimeHandle) ([]string, error) {
	return []string{"true"}, nil
}
func (noopPTYSource) IsAlive(context.Context, ports.RuntimeHandle) (bool, error) {
	return false, nil
}

// toSlice casts the "sessions" field from a muxMsg to []any.
func toSlice(t *testing.T, v any) []any {
	t.Helper()
	if v == nil {
		return nil
	}
	s, ok := v.([]any)
	if !ok {
		t.Fatalf("sessions field is %T, want []any", v)
	}
	return s
}

// TestMuxStreamingSessionPatchDelivery is a functional end-to-end test of the
// CDC → terminal.Manager → WebSocket → sessionPatch delivery path:
//
//  1. Subscribe on /mux — receives empty initial snapshot.
//  2. Spawn a session (triggers a DB INSERT → change_log DB trigger).
//  3. Poll CDC — broadcaster fires → terminal.Manager enqueues sessionPatch.
//  4. Client receives a sessions/snapshot frame containing the spawned session.
func TestMuxStreamingSessionPatchDelivery(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	st := newStack(t)

	bc := cdc.NewBroadcaster()
	poller := cdc.NewPoller(st.store, bc, cdc.PollerConfig{StartSeq: 0})

	src := &testSessionSource{store: st.store}
	mgr := terminal.NewManager(
		&noopPTYSource{},
		bc,
		discardLog(),
		terminal.WithHeartbeat(0),
		terminal.WithSessionSource(src),
	)
	t.Cleanup(mgr.Close)

	srv := httptest.NewServer(httpd.TerminalMuxHandler(mgr, discardLog()))
	t.Cleanup(srv.Close)

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/"
	conn, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatalf("websocket dial: %v", err)
	}
	t.Cleanup(func() { _ = conn.CloseNow() })

	// Subscribe and receive the initial empty snapshot.
	sendMux(ctx, t, conn, muxMsg{"ch": "subscribe", "type": "subscribe"})
	m := recvMux(ctx, t, conn, "sessions", "snapshot")
	sessions := toSlice(t, m["sessions"])
	if len(sessions) != 0 {
		t.Fatalf("initial snapshot: want 0 sessions, got %d", len(sessions))
	}

	// Spawn a session — this inserts into sessions, which triggers the DB
	// trigger writing to change_log.
	sess, err := st.sm.Spawn(ctx, ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindWorker, Branch: "test-branch", Prompt: "hello"})
	if err != nil {
		t.Fatalf("spawn: %v", err)
	}

	// Poll CDC: tails change_log and publishes to the broadcaster, which the
	// terminal.Manager subscription picks up and enqueues as sessionPatch.
	if err := poller.Poll(ctx); err != nil {
		t.Fatalf("poll: %v", err)
	}

	// The client must receive a sessions/snapshot frame containing the spawned session.
	m = recvMux(ctx, t, conn, "sessions", "snapshot")
	sessions = toSlice(t, m["sessions"])
	if len(sessions) != 1 {
		t.Fatalf("post-spawn snapshot: want 1 session, got %d: %v", len(sessions), sessions)
	}

	patch := sessions[0].(map[string]any)
	if patch["id"] != string(sess.ID) {
		t.Fatalf("session id: got %q, want %q", patch["id"], sess.ID)
	}
	// A freshly spawned worker with no PR derives to idle/idle, which maps to the
	// "working" attention level. Assert the exact mapping, not just presence.
	if patch["status"] != "idle" {
		t.Fatalf("status: got %q, want %q", patch["status"], "idle")
	}
	if patch["activity"] != "idle" {
		t.Fatalf("activity: got %q, want %q", patch["activity"], "idle")
	}
	if patch["attentionLevel"] != "working" {
		t.Fatalf("attentionLevel: got %q, want %q", patch["attentionLevel"], "working")
	}
}

// TestMuxStreamingPatchReflectsPRDerivedStatus drives a PR observation that makes
// the session derive to ci_failed and asserts the live patch carries the derived
// status and its mapped attention level — exercising the per-event enrichment path
// with a non-trivial status mapping.
func TestMuxStreamingPatchReflectsPRDerivedStatus(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	st := newStack(t)

	bc := cdc.NewBroadcaster()
	poller := cdc.NewPoller(st.store, bc, cdc.PollerConfig{StartSeq: 0})

	src := &testSessionSource{store: st.store}
	mgr := terminal.NewManager(
		&noopPTYSource{},
		bc,
		discardLog(),
		terminal.WithHeartbeat(0),
		terminal.WithSessionSource(src),
	)
	t.Cleanup(mgr.Close)

	srv := httptest.NewServer(httpd.TerminalMuxHandler(mgr, discardLog()))
	t.Cleanup(srv.Close)

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/"
	conn, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatalf("websocket dial: %v", err)
	}
	t.Cleanup(func() { _ = conn.CloseNow() })

	sess, err := st.sm.Spawn(ctx, ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindWorker, Branch: "b", Prompt: "go"})
	if err != nil {
		t.Fatalf("spawn: %v", err)
	}

	sendMux(ctx, t, conn, muxMsg{"ch": "subscribe", "type": "subscribe"})
	recvMux(ctx, t, conn, "sessions", "snapshot") // drain initial snapshot

	// A failing CI check on the PR makes the session derive to ci_failed.
	if err := st.prm.ApplyObservation(ctx, sess.ID, ports.PRObservation{
		Fetched: true, URL: "pr1", Number: 1, CI: domain.CIFailing,
		Checks: []ports.PRCheckObservation{{Name: "build", CommitHash: "c1", Status: domain.PRCheckFailed, LogTail: "boom"}},
	}); err != nil {
		t.Fatalf("apply observation: %v", err)
	}
	if err := poller.Poll(ctx); err != nil {
		t.Fatalf("poll: %v", err)
	}

	// Drain patches until one reports the spawned session as ci_failed.
	deadline := time.Now().Add(3 * time.Second)
	for {
		if time.Now().After(deadline) {
			t.Fatal("did not receive a ci_failed patch within 3s")
		}
		m := recvMux(ctx, t, conn, "sessions", "snapshot")
		sessions := toSlice(t, m["sessions"])
		if len(sessions) == 0 {
			continue
		}
		patch := sessions[0].(map[string]any)
		if patch["id"] != string(sess.ID) || patch["status"] != "ci_failed" {
			continue
		}
		if patch["attentionLevel"] != "review" {
			t.Fatalf("attentionLevel: got %q, want %q", patch["attentionLevel"], "review")
		}
		break
	}
}

// TestMuxStreamingInitialSnapshotContainsExistingSessions verifies that sessions
// already in the store at subscribe-time appear in the first snapshot — the
// subscriber does not miss sessions that predate the connection.
func TestMuxStreamingInitialSnapshotContainsExistingSessions(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	st := newStack(t)

	// Spawn a session BEFORE the client connects.
	sess, err := st.sm.Spawn(ctx, ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindWorker})
	if err != nil {
		t.Fatalf("spawn: %v", err)
	}

	bc := cdc.NewBroadcaster()
	src := &testSessionSource{store: st.store}
	mgr := terminal.NewManager(
		&noopPTYSource{},
		bc,
		discardLog(),
		terminal.WithHeartbeat(0),
		terminal.WithSessionSource(src),
	)
	t.Cleanup(mgr.Close)

	srv := httptest.NewServer(httpd.TerminalMuxHandler(mgr, discardLog()))
	t.Cleanup(srv.Close)

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/"
	conn, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatalf("websocket dial: %v", err)
	}
	t.Cleanup(func() { _ = conn.CloseNow() })

	sendMux(ctx, t, conn, muxMsg{"ch": "subscribe", "type": "subscribe"})
	m := recvMux(ctx, t, conn, "sessions", "snapshot")
	sessions := toSlice(t, m["sessions"])
	if len(sessions) != 1 {
		t.Fatalf("initial snapshot: want 1 session, got %d", len(sessions))
	}
	patch := sessions[0].(map[string]any)
	if patch["id"] != string(sess.ID) {
		t.Fatalf("session id: got %q, want %q", patch["id"], sess.ID)
	}
}
