package reaper

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

var ctx = context.Background()

type fakeLCM struct {
	running   []domain.SessionRecord
	observed  map[domain.SessionID]ports.RuntimeFacts
	escalated int
}

func (l *fakeLCM) RunningSessions(context.Context) ([]domain.SessionRecord, error) {
	return l.running, nil
}
func (l *fakeLCM) ApplyRuntimeObservation(_ context.Context, id domain.SessionID, f ports.RuntimeFacts) error {
	if l.observed == nil {
		l.observed = map[domain.SessionID]ports.RuntimeFacts{}
	}
	l.observed[id] = f
	return nil
}
func (l *fakeLCM) TickEscalations(context.Context, time.Time) error { l.escalated++; return nil }
func (l *fakeLCM) ApplyActivitySignal(context.Context, domain.SessionID, ports.ActivitySignal) error {
	return nil
}
func (l *fakeLCM) ApplySCMObservation(context.Context, domain.SessionID, ports.SCMFacts) error {
	return nil
}
func (l *fakeLCM) ApplyPRObservation(context.Context, domain.SessionID, ports.PRObservation) error {
	return nil
}
func (l *fakeLCM) OnSpawnCompleted(context.Context, domain.SessionID, ports.SpawnOutcome) error {
	return nil
}
func (l *fakeLCM) OnKillRequested(context.Context, domain.SessionID, domain.TerminationReason) error {
	return nil
}

type fakeRuntime struct {
	alive bool
	err   error
}

func (r fakeRuntime) Create(context.Context, ports.RuntimeConfig) (ports.RuntimeHandle, error) {
	return ports.RuntimeHandle{}, nil
}
func (r fakeRuntime) Destroy(context.Context, ports.RuntimeHandle) error { return nil }
func (r fakeRuntime) IsAlive(context.Context, ports.RuntimeHandle) (bool, error) {
	return r.alive, r.err
}

func probableSession(id domain.SessionID) domain.SessionRecord {
	return domain.SessionRecord{
		ID:       id,
		Metadata: domain.SessionMetadata{RuntimeHandleID: "h1", RuntimeName: "tmux"},
		Lifecycle: domain.CanonicalSessionLifecycle{
			Session: domain.SessionSubstate{State: domain.SessionWorking},
		},
	}
}

func quietLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func newReaper(lcm *fakeLCM, rt fakeRuntime) *Reaper {
	return New(lcm, MapRegistry{"tmux": rt}, Config{Logger: quietLogger()})
}

func TestTick_ReportsAliveProbe(t *testing.T) {
	lcm := &fakeLCM{running: []domain.SessionRecord{probableSession("mer-1")}}
	if err := newReaper(lcm, fakeRuntime{alive: true}).Tick(ctx); err != nil {
		t.Fatal(err)
	}
	if lcm.observed["mer-1"].Runtime != ports.ProbeAlive {
		t.Fatalf("want alive probe, got %q", lcm.observed["mer-1"].Runtime)
	}
}

func TestTick_ReportsProbeErrorAsFailed(t *testing.T) {
	lcm := &fakeLCM{running: []domain.SessionRecord{probableSession("mer-1")}}
	if err := newReaper(lcm, fakeRuntime{err: errors.New("tmux gone")}).Tick(ctx); err != nil {
		t.Fatal(err)
	}
	if lcm.observed["mer-1"].Runtime != ports.ProbeFailed {
		t.Fatalf("probe error must be reported as failed, got %q", lcm.observed["mer-1"].Runtime)
	}
}

func TestTick_FiresEscalationHeartbeat(t *testing.T) {
	lcm := &fakeLCM{}
	if err := newReaper(lcm, fakeRuntime{}).Tick(ctx); err != nil {
		t.Fatal(err)
	}
	if lcm.escalated != 1 {
		t.Fatalf("tick must drive TickEscalations once, got %d", lcm.escalated)
	}
}

func TestTick_SkipsSessionWithoutHandle(t *testing.T) {
	noHandle := domain.SessionRecord{ID: "mer-1"} // no runtime metadata
	lcm := &fakeLCM{running: []domain.SessionRecord{noHandle}}
	if err := newReaper(lcm, fakeRuntime{alive: true}).Tick(ctx); err != nil {
		t.Fatal(err)
	}
	if _, probed := lcm.observed["mer-1"]; probed {
		t.Fatal("a session without a runtime handle must be skipped")
	}
}
