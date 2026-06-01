package reaper

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

var ctx = context.Background()

type fakeLCM struct {
	observed map[domain.SessionID]ports.RuntimeFacts
}

func (l *fakeLCM) ApplyRuntimeObservation(_ context.Context, id domain.SessionID, f ports.RuntimeFacts) error {
	if l.observed == nil {
		l.observed = map[domain.SessionID]ports.RuntimeFacts{}
	}
	l.observed[id] = f
	return nil
}
func (l *fakeLCM) ApplyActivitySignal(context.Context, domain.SessionID, ports.ActivitySignal) error {
	return nil
}
func (l *fakeLCM) ApplyPRObservation(context.Context, domain.SessionID, ports.PRObservation) error {
	return nil
}
func (l *fakeLCM) MarkSpawned(context.Context, domain.SessionID, ports.SpawnOutcome) error {
	return nil
}
func (l *fakeLCM) MarkTerminated(context.Context, domain.SessionID) error { return nil }

type fakeSessions struct{ rows []domain.SessionRecord }

func (s fakeSessions) ListAllSessions(context.Context) ([]domain.SessionRecord, error) {
	return s.rows, nil
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
		Activity: domain.ActivitySubstate{State: domain.ActivityActive},
		Metadata: domain.SessionMetadata{RuntimeHandleID: "h1"},
	}
}

func quietLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func newReaper(lcm *fakeLCM, sessions fakeSessions, rt fakeRuntime) *Reaper {
	return New(lcm, sessions, rt, Config{Logger: quietLogger()})
}

func TestTick_ReportsAliveProbe(t *testing.T) {
	lcm := &fakeLCM{}
	sessions := fakeSessions{rows: []domain.SessionRecord{probableSession("mer-1")}}
	if err := newReaper(lcm, sessions, fakeRuntime{alive: true}).Tick(ctx); err != nil {
		t.Fatal(err)
	}
	if lcm.observed["mer-1"].Runtime != ports.ProbeAlive {
		t.Fatalf("want alive probe, got %q", lcm.observed["mer-1"].Runtime)
	}
}

func TestTick_ReportsProbeErrorAsFailed(t *testing.T) {
	lcm := &fakeLCM{}
	sessions := fakeSessions{rows: []domain.SessionRecord{probableSession("mer-1")}}
	if err := newReaper(lcm, sessions, fakeRuntime{err: errors.New("Zellij gone")}).Tick(ctx); err != nil {
		t.Fatal(err)
	}
	if lcm.observed["mer-1"].Runtime != ports.ProbeFailed {
		t.Fatalf("probe error must be reported as failed, got %q", lcm.observed["mer-1"].Runtime)
	}
}

func TestTick_SkipsTerminatedSession(t *testing.T) {
	lcm := &fakeLCM{}
	dead := probableSession("mer-1")
	dead.IsTerminated = true
	sessions := fakeSessions{rows: []domain.SessionRecord{dead}}
	if err := newReaper(lcm, sessions, fakeRuntime{alive: true}).Tick(ctx); err != nil {
		t.Fatal(err)
	}
	if _, probed := lcm.observed["mer-1"]; probed {
		t.Fatal("terminated sessions must not be probed")
	}
}

func TestTick_SkipsSessionWithoutHandle(t *testing.T) {
	lcm := &fakeLCM{}
	noHandle := domain.SessionRecord{ID: "mer-1"} // no runtime metadata
	sessions := fakeSessions{rows: []domain.SessionRecord{noHandle}}
	if err := newReaper(lcm, sessions, fakeRuntime{alive: true}).Tick(ctx); err != nil {
		t.Fatal(err)
	}
	if _, probed := lcm.observed["mer-1"]; probed {
		t.Fatal("a session without a runtime handle must be skipped")
	}
}
