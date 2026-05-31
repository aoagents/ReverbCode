package decide

import (
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

var t0 = time.Date(2026, 5, 31, 12, 0, 0, 0, time.UTC)

func TestResolveProbeDecision(t *testing.T) {
	tests := []struct {
		name       string
		in         ProbeInput
		wantState  domain.SessionState
		wantReason domain.TerminationReason
		wantAlive  bool
		wantDetect bool // expect a detecting verdict (first attempt -> SessionDetecting)
	}{
		{
			name:      "kill requested -> terminated with reason",
			in:        ProbeInput{KillRequested: true, KillReason: domain.TermManuallyKilled, Now: t0},
			wantState: domain.SessionTerminated, wantReason: domain.TermManuallyKilled, wantAlive: false,
		},
		{
			name:      "kill requested without reason defaults to manually_killed",
			in:        ProbeInput{KillRequested: true, Now: t0},
			wantState: domain.SessionTerminated, wantReason: domain.TermManuallyKilled, wantAlive: false,
		},
		{
			name:      "runtime probe failed -> detecting (not death)",
			in:        ProbeInput{RuntimeFailed: true, Now: t0},
			wantState: domain.SessionDetecting, wantAlive: true, wantDetect: true,
		},
		{
			name:      "process probe failed -> detecting",
			in:        ProbeInput{RuntimeAlive: true, ProcessFailed: true, Now: t0},
			wantState: domain.SessionDetecting, wantAlive: true, wantDetect: true,
		},
		{
			name:      "runtime alive + process alive -> working",
			in:        ProbeInput{RuntimeAlive: true, Process: ProcessAlive, Now: t0},
			wantState: domain.SessionWorking, wantAlive: true,
		},
		{
			name:      "runtime alive + process indeterminate -> working",
			in:        ProbeInput{RuntimeAlive: true, Process: ProcessIndeterminate, Now: t0},
			wantState: domain.SessionWorking, wantAlive: true,
		},
		{
			name:      "runtime alive + process dead -> detecting (disagree)",
			in:        ProbeInput{RuntimeAlive: true, Process: ProcessDead, Now: t0},
			wantState: domain.SessionDetecting, wantAlive: true, wantDetect: true,
		},
		{
			name:      "runtime down + process dead + no activity -> terminated runtime_lost",
			in:        ProbeInput{RuntimeAlive: false, Process: ProcessDead, RecentActivity: false, Now: t0},
			wantState: domain.SessionTerminated, wantReason: domain.TermRuntimeLost, wantAlive: false,
		},
		{
			name:      "runtime down + process alive -> detecting (disagree)",
			in:        ProbeInput{RuntimeAlive: false, Process: ProcessAlive, Now: t0},
			wantState: domain.SessionDetecting, wantAlive: true, wantDetect: true,
		},
		{
			name:      "runtime down + process dead + recent activity -> detecting",
			in:        ProbeInput{RuntimeAlive: false, Process: ProcessDead, RecentActivity: true, Now: t0},
			wantState: domain.SessionDetecting, wantAlive: true, wantDetect: true,
		},
		{
			name:      "runtime down + process indeterminate -> detecting",
			in:        ProbeInput{RuntimeAlive: false, Process: ProcessIndeterminate, Now: t0},
			wantState: domain.SessionDetecting, wantAlive: true, wantDetect: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := ResolveProbeDecision(tt.in)
			if d.SessionState != tt.wantState {
				t.Errorf("state = %q, want %q", d.SessionState, tt.wantState)
			}
			if d.TerminationReason != tt.wantReason {
				t.Errorf("reason = %q, want %q", d.TerminationReason, tt.wantReason)
			}
			if d.IsAlive != tt.wantAlive {
				t.Errorf("isAlive = %v, want %v", d.IsAlive, tt.wantAlive)
			}
			if tt.wantDetect && d.Detecting == nil {
				t.Errorf("expected detecting memory, got nil")
			}
		})
	}
}

func TestCreateDetectingDecision(t *testing.T) {
	t.Run("first entry sets attempts 1", func(t *testing.T) {
		d := CreateDetectingDecision(DetectingInput{Evidence: "runtime down", Now: t0})
		if d.SessionState != domain.SessionDetecting || d.Detecting == nil || d.Detecting.Attempts != 1 {
			t.Fatalf("got %+v", d)
		}
	})
	t.Run("same evidence climbs the counter", func(t *testing.T) {
		prior := &domain.DetectingState{Attempts: 1, StartedAt: t0, EvidenceHash: HashEvidence("runtime down")}
		d := CreateDetectingDecision(DetectingInput{Evidence: "runtime down", Prior: prior, Now: t0.Add(time.Second)})
		if d.Detecting == nil || d.Detecting.Attempts != 2 {
			t.Fatalf("attempts = %+v, want 2", d.Detecting)
		}
	})
	t.Run("changed evidence resets the counter", func(t *testing.T) {
		prior := &domain.DetectingState{Attempts: 2, StartedAt: t0, EvidenceHash: HashEvidence("runtime down")}
		d := CreateDetectingDecision(DetectingInput{Evidence: "process dead", Prior: prior, Now: t0.Add(time.Second)})
		if d.Detecting == nil || d.Detecting.Attempts != 1 {
			t.Fatalf("attempts = %+v, want 1 (evidence changed)", d.Detecting)
		}
	})
	t.Run("escalates to stuck at the attempt cap", func(t *testing.T) {
		prior := &domain.DetectingState{Attempts: DetectingMaxAttempts - 1, StartedAt: t0, EvidenceHash: HashEvidence("runtime down")}
		d := CreateDetectingDecision(DetectingInput{Evidence: "runtime down", Prior: prior, Now: t0.Add(time.Second)})
		if d.SessionState != domain.SessionStuck {
			t.Fatalf("state = %q, want stuck", d.SessionState)
		}
	})
	t.Run("escalates to stuck past the duration cap", func(t *testing.T) {
		prior := &domain.DetectingState{Attempts: 1, StartedAt: t0, EvidenceHash: HashEvidence("runtime down")}
		d := CreateDetectingDecision(DetectingInput{Evidence: "runtime down", Prior: prior, Now: t0.Add(DetectingMaxDuration + time.Second)})
		if d.SessionState != domain.SessionStuck {
			t.Fatalf("state = %q, want stuck (duration cap)", d.SessionState)
		}
	})
}

func TestProbeDetectingEscalationFlow(t *testing.T) {
	in := ProbeInput{RuntimeAlive: false, Process: ProcessIndeterminate, Now: t0}
	var prior *domain.DetectingState
	for i := 1; i < DetectingMaxAttempts; i++ {
		in.Prior = prior
		in.Now = t0.Add(time.Duration(i) * time.Second)
		d := ResolveProbeDecision(in)
		if d.SessionState != domain.SessionDetecting {
			t.Fatalf("attempt %d: state = %q, want detecting", i, d.SessionState)
		}
		prior = d.Detecting
	}
	in.Prior = prior
	in.Now = t0.Add(time.Hour)
	if d := ResolveProbeDecision(in); d.SessionState != domain.SessionStuck {
		t.Fatalf("final attempt: state = %q, want stuck", d.SessionState)
	}
}

func TestHashEvidence(t *testing.T) {
	// timestamp-only differences hash equal; a real change differs.
	a := HashEvidence("runtime down at 2026-05-31T12:00:00Z")
	b := HashEvidence("runtime down at 2026-05-31T13:30:45Z")
	if a != b {
		t.Errorf("restamped evidence should hash equal")
	}
	c := HashEvidence("process dead at 2026-05-31T12:00:00Z")
	if a == c {
		t.Errorf("different evidence should hash differently")
	}
}
