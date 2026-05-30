package reaper_test

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/lifecycle"
	"github.com/aoagents/agent-orchestrator/backend/internal/observe/reaper"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// ---- fakes ----

type aliveResult struct {
	alive bool
	err   error
}

// fakeRuntime is a programmable ports.Runtime. The reaper only calls IsAlive,
// but the interface requires the other methods so we stub them.
type fakeRuntime struct {
	mu      sync.Mutex
	results map[string]aliveResult
	probed  []string
}

var _ ports.Runtime = (*fakeRuntime)(nil)

func (f *fakeRuntime) IsAlive(_ context.Context, h ports.RuntimeHandle) (bool, error) {
	f.mu.Lock()
	f.probed = append(f.probed, h.ID)
	f.mu.Unlock()
	r, ok := f.results[h.ID]
	if !ok {
		return false, errors.New("fakeRuntime: no programmed response for " + h.ID)
	}
	return r.alive, r.err
}

func (f *fakeRuntime) Create(context.Context, ports.RuntimeConfig) (ports.RuntimeHandle, error) {
	return ports.RuntimeHandle{}, nil
}
func (f *fakeRuntime) Destroy(context.Context, ports.RuntimeHandle) error { return nil }
func (f *fakeRuntime) SendMessage(context.Context, ports.RuntimeHandle, string) error {
	return nil
}
func (f *fakeRuntime) GetOutput(context.Context, ports.RuntimeHandle, int) (string, error) {
	return "", nil
}

// fakeLCM records every reaper-facing call in order so tests can assert the
// exact sequence (TickEscalations -> RunningSessions -> ApplyRuntimeObservation).
type fakeLCM struct {
	mu       sync.Mutex
	sessions []domain.SessionRecord
	calls    []call

	runErr  error
	tickErr error
	obsErr  error
}

type call struct {
	Kind    string
	Now     time.Time
	Session domain.SessionID
	Facts   ports.RuntimeFacts
}

var _ ports.LifecycleManager = (*fakeLCM)(nil)

func (l *fakeLCM) RunningSessions(_ context.Context) ([]domain.SessionRecord, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.calls = append(l.calls, call{Kind: "RunningSessions"})
	if l.runErr != nil {
		return nil, l.runErr
	}
	out := make([]domain.SessionRecord, len(l.sessions))
	copy(out, l.sessions)
	return out, nil
}

func (l *fakeLCM) TickEscalations(_ context.Context, now time.Time) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.calls = append(l.calls, call{Kind: "TickEscalations", Now: now})
	return l.tickErr
}

func (l *fakeLCM) ApplyRuntimeObservation(_ context.Context, id domain.SessionID, f ports.RuntimeFacts) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.calls = append(l.calls, call{Kind: "ApplyRuntimeObservation", Session: id, Facts: f})
	return l.obsErr
}

// unused methods on the LCM port — the reaper never invokes them.
func (l *fakeLCM) ApplySCMObservation(context.Context, domain.SessionID, ports.SCMFacts) error {
	return nil
}
func (l *fakeLCM) ApplyActivitySignal(context.Context, domain.SessionID, ports.ActivitySignal) error {
	return nil
}
func (l *fakeLCM) OnSpawnInitiated(context.Context, domain.SessionRecord) error { return nil }
func (l *fakeLCM) OnSpawnCompleted(context.Context, domain.SessionID, ports.SpawnOutcome) error {
	return nil
}
func (l *fakeLCM) OnKillRequested(context.Context, domain.SessionID, ports.KillReason) error {
	return nil
}

// ---- helpers ----

func aliveSessionWith(id domain.SessionID, runtimeName, handleID string) domain.SessionRecord {
	return domain.SessionRecord{
		ID: id,
		Lifecycle: domain.CanonicalSessionLifecycle{
			Session: domain.SessionSubstate{State: domain.SessionWorking, Reason: domain.ReasonTaskInProgress},
			Runtime: domain.RuntimeSubstate{State: domain.RuntimeAlive, Reason: domain.RuntimeReasonProcessRunning},
		},
		Metadata: map[string]string{
			lifecycle.MetaRuntimeHandleID: handleID,
			lifecycle.MetaRuntimeName:     runtimeName,
		},
	}
}

// detectingSessionWith returns a session in the Detecting quarantine, the
// shape `Manager.RunningSessions` MUST include so a probe-alive can recover it
// (otherwise the reaper traps every session that hiccups once in detecting).
func detectingSessionWith(id domain.SessionID, runtimeName, handleID string) domain.SessionRecord {
	return domain.SessionRecord{
		ID: id,
		Lifecycle: domain.CanonicalSessionLifecycle{
			Session: domain.SessionSubstate{State: domain.SessionDetecting, Reason: domain.ReasonProbeFailure},
			Runtime: domain.RuntimeSubstate{State: domain.RuntimeProbeFailed, Reason: domain.RuntimeReasonProbeError},
		},
		Metadata: map[string]string{
			lifecycle.MetaRuntimeHandleID: handleID,
			lifecycle.MetaRuntimeName:     runtimeName,
		},
	}
}

// ---- tests ----

func TestReaper_Tick(t *testing.T) {
	now := time.Date(2026, 5, 28, 12, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }

	type runtimeProbes struct {
		name    string
		results map[string]aliveResult
	}

	tests := []struct {
		name      string
		sessions  []domain.SessionRecord
		runtimes  []runtimeProbes
		wantCalls []call
		wantProbe map[string][]string // runtime name -> handle IDs probed, in order
	}{
		{
			// "No death applied" per the spec: the LCM does not receive a
			// death-causing fact. It still receives the alive fact, because
			// the reaper reports what it probed and the LCM is the one that
			// diffs against canonical (a no-op when runtime is already alive,
			// a recovery when the session was in Detecting).
			name:     "alive session: alive fact reported, no death applied, tick still fires",
			sessions: []domain.SessionRecord{aliveSessionWith("s1", "tmux", "h1")},
			runtimes: []runtimeProbes{{name: "tmux", results: map[string]aliveResult{"h1": {alive: true}}}},
			wantCalls: []call{
				{Kind: "TickEscalations", Now: now},
				{Kind: "RunningSessions"},
				{
					Kind:    "ApplyRuntimeObservation",
					Session: "s1",
					Facts:   ports.RuntimeFacts{ObservedAt: now, RuntimeState: ports.RuntimeProbeAlive, ProcessState: ports.ProcessProbeAlive},
				},
			},
			wantProbe: map[string][]string{"tmux": {"h1"}},
		},
		{
			// Recovery path: a session in Detecting+probe_failed must be in
			// the poll set so an alive probe can flow through and recover it.
			// If the reaper filtered to runtime-axis-alive only, this session
			// would be trapped in Detecting forever.
			name:     "detecting session: alive probe reported so LCM can recover from quarantine",
			sessions: []domain.SessionRecord{detectingSessionWith("s1", "tmux", "h1")},
			runtimes: []runtimeProbes{{name: "tmux", results: map[string]aliveResult{"h1": {alive: true}}}},
			wantCalls: []call{
				{Kind: "TickEscalations", Now: now},
				{Kind: "RunningSessions"},
				{
					Kind:    "ApplyRuntimeObservation",
					Session: "s1",
					Facts:   ports.RuntimeFacts{ObservedAt: now, RuntimeState: ports.RuntimeProbeAlive, ProcessState: ports.ProcessProbeAlive},
				},
			},
			wantProbe: map[string][]string{"tmux": {"h1"}},
		},
		{
			name:     "dead session: exactly one ApplyRuntimeObservation with Dead facts",
			sessions: []domain.SessionRecord{aliveSessionWith("s1", "tmux", "h1")},
			runtimes: []runtimeProbes{{name: "tmux", results: map[string]aliveResult{"h1": {alive: false}}}},
			wantCalls: []call{
				{Kind: "TickEscalations", Now: now},
				{Kind: "RunningSessions"},
				{
					Kind:    "ApplyRuntimeObservation",
					Session: "s1",
					Facts:   ports.RuntimeFacts{ObservedAt: now, RuntimeState: ports.RuntimeProbeDead, ProcessState: ports.ProcessProbeDead},
				},
			},
			wantProbe: map[string][]string{"tmux": {"h1"}},
		},
		{
			name:     "probe error: reported as failed fact, NOT collapsed to alive",
			sessions: []domain.SessionRecord{aliveSessionWith("s1", "tmux", "h1")},
			runtimes: []runtimeProbes{{name: "tmux", results: map[string]aliveResult{"h1": {err: errors.New("boom")}}}},
			wantCalls: []call{
				{Kind: "TickEscalations", Now: now},
				{Kind: "RunningSessions"},
				{
					Kind:    "ApplyRuntimeObservation",
					Session: "s1",
					Facts:   ports.RuntimeFacts{ObservedAt: now, RuntimeState: ports.RuntimeProbeFailed, ProcessState: ports.ProcessProbeFailed},
				},
			},
			wantProbe: map[string][]string{"tmux": {"h1"}},
		},
		{
			name: "multi-runtime dispatch: tmux + zellij in same tick",
			sessions: []domain.SessionRecord{
				aliveSessionWith("s1", "tmux", "ht"),
				aliveSessionWith("s2", "zellij", "hz"),
			},
			runtimes: []runtimeProbes{
				{name: "tmux", results: map[string]aliveResult{"ht": {alive: false}}},
				{name: "zellij", results: map[string]aliveResult{"hz": {alive: true}}},
			},
			wantCalls: []call{
				{Kind: "TickEscalations", Now: now},
				{Kind: "RunningSessions"},
				{
					Kind:    "ApplyRuntimeObservation",
					Session: "s1",
					Facts:   ports.RuntimeFacts{ObservedAt: now, RuntimeState: ports.RuntimeProbeDead, ProcessState: ports.ProcessProbeDead},
				},
				{
					Kind:    "ApplyRuntimeObservation",
					Session: "s2",
					Facts:   ports.RuntimeFacts{ObservedAt: now, RuntimeState: ports.RuntimeProbeAlive, ProcessState: ports.ProcessProbeAlive},
				},
			},
			wantProbe: map[string][]string{"tmux": {"ht"}, "zellij": {"hz"}},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			lcm := &fakeLCM{sessions: tc.sessions}
			registry := reaper.MapRegistry{}
			byName := map[string]*fakeRuntime{}
			for _, r := range tc.runtimes {
				rt := &fakeRuntime{results: r.results}
				registry[r.name] = rt
				byName[r.name] = rt
			}
			rp := reaper.New(lcm, registry, reaper.Config{Clock: clock, Tick: time.Hour})

			if err := rp.Tick(context.Background()); err != nil {
				t.Fatalf("Tick error: %v", err)
			}

			if !reflect.DeepEqual(lcm.calls, tc.wantCalls) {
				t.Errorf("LCM call log mismatch:\n got  %#v\n want %#v", lcm.calls, tc.wantCalls)
			}

			for name, want := range tc.wantProbe {
				got := byName[name].probed
				if !reflect.DeepEqual(got, want) {
					t.Errorf("runtime %q probed handles mismatch: got %v want %v", name, got, want)
				}
			}
		})
	}
}

// TestReaper_Loop verifies the background goroutine actually drives ticks and
// exits on context cancel without leaking.
func TestReaper_Loop(t *testing.T) {
	now := time.Date(2026, 5, 28, 12, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }
	lcm := &fakeLCM{}
	rp := reaper.New(lcm, reaper.MapRegistry{}, reaper.Config{Clock: clock, Tick: 5 * time.Millisecond})

	ctx, cancel := context.WithCancel(context.Background())
	done := rp.Start(ctx)

	// Wait for at least two ticks so we know the loop is actually firing.
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		lcm.mu.Lock()
		n := countKind(lcm.calls, "TickEscalations")
		lcm.mu.Unlock()
		if n >= 2 {
			break
		}
		time.Sleep(2 * time.Millisecond)
	}
	cancel()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("reaper goroutine did not exit within 1s of ctx cancel")
	}

	lcm.mu.Lock()
	defer lcm.mu.Unlock()
	if got := countKind(lcm.calls, "TickEscalations"); got < 2 {
		t.Errorf("expected at least 2 TickEscalations calls during loop, got %d", got)
	}
}

func countKind(calls []call, kind string) int {
	n := 0
	for _, c := range calls {
		if c.Kind == kind {
			n++
		}
	}
	return n
}

// TestReaper_SkipsUnknownRuntime verifies the reaper does not panic and does not
// report a fact when a session references an unregistered runtime — the reaper
// only reports what it actually probed.
func TestReaper_SkipsUnknownRuntime(t *testing.T) {
	now := time.Date(2026, 5, 28, 12, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }
	lcm := &fakeLCM{sessions: []domain.SessionRecord{aliveSessionWith("s1", "ghost", "h1")}}
	rp := reaper.New(lcm, reaper.MapRegistry{}, reaper.Config{Clock: clock, Tick: time.Hour})

	if err := rp.Tick(context.Background()); err != nil {
		t.Fatalf("Tick error: %v", err)
	}

	for _, c := range lcm.calls {
		if c.Kind == "ApplyRuntimeObservation" {
			t.Fatalf("unexpected ApplyRuntimeObservation for unknown-runtime session: %+v", c)
		}
	}
}

// TestReaper_SkipsMissingHandle verifies the reaper does not probe (and does not
// report) for sessions whose runtime handle metadata is missing — probing
// nothing returns no fact.
func TestReaper_SkipsMissingHandle(t *testing.T) {
	now := time.Date(2026, 5, 28, 12, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }
	sess := aliveSessionWith("s1", "tmux", "h1")
	delete(sess.Metadata, lifecycle.MetaRuntimeHandleID)
	lcm := &fakeLCM{sessions: []domain.SessionRecord{sess}}
	rt := &fakeRuntime{results: map[string]aliveResult{}}
	rp := reaper.New(lcm, reaper.MapRegistry{"tmux": rt}, reaper.Config{Clock: clock, Tick: time.Hour})

	if err := rp.Tick(context.Background()); err != nil {
		t.Fatalf("Tick error: %v", err)
	}
	if len(rt.probed) != 0 {
		t.Errorf("expected no probes for session without handle id, got %v", rt.probed)
	}
	for _, c := range lcm.calls {
		if c.Kind == "ApplyRuntimeObservation" {
			t.Fatalf("unexpected ApplyRuntimeObservation: %+v", c)
		}
	}
}
