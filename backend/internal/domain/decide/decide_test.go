package decide

import (
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

var t0 = time.Date(2026, 5, 26, 12, 0, 0, 0, time.UTC)

func TestResolveProbeDecision(t *testing.T) {
	tests := []struct {
		name        string
		in          ProbeInput
		wantStatus  domain.SessionStatus
		wantState   domain.SessionState
		wantReason  domain.SessionReason
		wantDetect  bool // expect non-nil Detecting memory
		wantTermNil bool // expect terminal (Detecting must be nil)
	}{
		{
			name:        "kill requested short-circuits to terminal killed",
			in:          ProbeInput{KillRequested: true, Runtime: domain.RuntimeAlive, Process: ProcessAlive, Now: t0},
			wantStatus:  domain.StatusKilled,
			wantState:   domain.SessionTerminated,
			wantReason:  domain.ReasonManuallyKilled,
			wantTermNil: true,
		},
		{
			name:        "kill requested wins even over a dead+dead probe",
			in:          ProbeInput{KillRequested: true, Runtime: domain.RuntimeMissing, Process: ProcessDead, Now: t0},
			wantStatus:  domain.StatusKilled,
			wantState:   domain.SessionTerminated,
			wantReason:  domain.ReasonManuallyKilled,
			wantTermNil: true,
		},
		{
			name:       "runtime probe failed routes to detecting, never death",
			in:         ProbeInput{Runtime: domain.RuntimeMissing, RuntimeFailed: true, Process: ProcessDead, Now: t0},
			wantStatus: domain.StatusDetecting,
			wantState:  domain.SessionDetecting,
			wantReason: domain.ReasonProbeFailure,
			wantDetect: true,
		},
		{
			name:       "process probe failed routes to detecting",
			in:         ProbeInput{Runtime: domain.RuntimeAlive, Process: ProcessDead, ProcessFailed: true, Now: t0},
			wantStatus: domain.StatusDetecting,
			wantState:  domain.SessionDetecting,
			wantReason: domain.ReasonProbeFailure,
			wantDetect: true,
		},
		{
			name:       "runtime state probe_failed routes to detecting",
			in:         ProbeInput{Runtime: domain.RuntimeProbeFailed, Process: ProcessIndeterminate, Now: t0},
			wantStatus: domain.StatusDetecting,
			wantState:  domain.SessionDetecting,
			wantReason: domain.ReasonProbeFailure,
			wantDetect: true,
		},
		{
			name:       "runtime alive + process alive is working",
			in:         ProbeInput{Runtime: domain.RuntimeAlive, Process: ProcessAlive, Now: t0},
			wantStatus: domain.StatusWorking,
			wantState:  domain.SessionWorking,
			wantReason: domain.ReasonTaskInProgress,
		},
		{
			name:       "runtime alive + process indeterminate leans alive",
			in:         ProbeInput{Runtime: domain.RuntimeAlive, Process: ProcessIndeterminate, Now: t0},
			wantStatus: domain.StatusWorking,
			wantState:  domain.SessionWorking,
			wantReason: domain.ReasonTaskInProgress,
		},
		{
			name:       "runtime alive + process dead disagree -> detecting (agent_process_exited)",
			in:         ProbeInput{Runtime: domain.RuntimeAlive, Process: ProcessDead, Now: t0},
			wantStatus: domain.StatusDetecting,
			wantState:  domain.SessionDetecting,
			wantReason: domain.ReasonAgentProcessExited,
			wantDetect: true,
		},
		{
			name:       "runtime dead + process alive disagree -> detecting (runtime_lost)",
			in:         ProbeInput{Runtime: domain.RuntimeExited, Process: ProcessAlive, Now: t0},
			wantStatus: domain.StatusDetecting,
			wantState:  domain.SessionDetecting,
			wantReason: domain.ReasonRuntimeLost,
			wantDetect: true,
		},
		{
			name:       "runtime dead + recent activity disagree -> detecting (runtime_lost)",
			in:         ProbeInput{Runtime: domain.RuntimeMissing, Process: ProcessDead, RecentActivity: true, Now: t0},
			wantStatus: domain.StatusDetecting,
			wantState:  domain.SessionDetecting,
			wantReason: domain.ReasonRuntimeLost,
			wantDetect: true,
		},
		{
			name:       "runtime dead + process indeterminate cannot confirm -> detecting",
			in:         ProbeInput{Runtime: domain.RuntimeMissing, Process: ProcessIndeterminate, Now: t0},
			wantStatus: domain.StatusDetecting,
			wantState:  domain.SessionDetecting,
			wantReason: domain.ReasonRuntimeLost,
			wantDetect: true,
		},
		{
			name:        "runtime exited + process dead + no activity -> killed terminal",
			in:          ProbeInput{Runtime: domain.RuntimeExited, Process: ProcessDead, Now: t0},
			wantStatus:  domain.StatusKilled,
			wantState:   domain.SessionTerminated,
			wantReason:  domain.ReasonRuntimeLost,
			wantTermNil: true,
		},
		{
			name:        "runtime missing + process dead + no activity -> killed terminal",
			in:          ProbeInput{Runtime: domain.RuntimeMissing, Process: ProcessDead, Now: t0},
			wantStatus:  domain.StatusKilled,
			wantState:   domain.SessionTerminated,
			wantReason:  domain.ReasonRuntimeLost,
			wantTermNil: true,
		},
		{
			name:       "runtime unknown is ambiguous -> detecting (runtime_lost)",
			in:         ProbeInput{Runtime: domain.RuntimeUnknown, Process: ProcessDead, Now: t0},
			wantStatus: domain.StatusDetecting,
			wantState:  domain.SessionDetecting,
			wantReason: domain.ReasonRuntimeLost,
			wantDetect: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ResolveProbeDecision(tt.in)
			if got.Status != tt.wantStatus {
				t.Errorf("Status = %q, want %q", got.Status, tt.wantStatus)
			}
			if got.SessionState != tt.wantState {
				t.Errorf("SessionState = %q, want %q", got.SessionState, tt.wantState)
			}
			if got.SessionReason != tt.wantReason {
				t.Errorf("SessionReason = %q, want %q", got.SessionReason, tt.wantReason)
			}
			if tt.wantDetect && got.Detecting == nil {
				t.Errorf("expected non-nil Detecting memory, got nil")
			}
			if tt.wantTermNil && got.Detecting != nil {
				t.Errorf("terminal decision must carry nil Detecting, got %+v", got.Detecting)
			}
		})
	}
}

func TestResolveOpenPRDecision(t *testing.T) {
	tests := []struct {
		name        string
		in          OpenPRInput
		wantStatus  domain.SessionStatus
		wantPR      domain.PRReason
		wantPRState domain.PRState
		wantState   domain.SessionState
	}{
		{
			name:       "ci failing dominates everything",
			in:         OpenPRInput{CIFailing: true, ChangesRequested: true, Approved: true, Mergeable: true},
			wantStatus: domain.StatusCIFailed,
			wantPR:     domain.PRReasonCIFailing,
			wantState:  domain.SessionWorking,
		},
		{
			name:        "draft with failing CI maps to ci_failed",
			in:          OpenPRInput{Draft: true, CIFailing: true, ChangesRequested: true, Approved: true, Mergeable: true},
			wantStatus:  domain.StatusCIFailed,
			wantPR:      domain.PRReasonCIFailing,
			wantPRState: domain.PRDraft,
			wantState:   domain.SessionWorking,
		},
		{
			name:        "draft ignores review and merge states",
			in:          OpenPRInput{Draft: true, ChangesRequested: true, Approved: true, Mergeable: true, ReviewPending: true, IdleBeyond: true},
			wantStatus:  domain.StatusDraft,
			wantPR:      domain.PRReasonInProgress,
			wantPRState: domain.PRDraft,
			wantState:   domain.SessionWorking,
		},
		{
			name:       "changes requested before approval states",
			in:         OpenPRInput{ChangesRequested: true, Approved: true, Mergeable: true},
			wantStatus: domain.StatusChangesRequested,
			wantPR:     domain.PRReasonChangesRequested,
			wantState:  domain.SessionWorking,
		},
		{
			name:       "bot comments get distinct PR reason",
			in:         OpenPRInput{BotComments: true, Approved: true, Mergeable: true},
			wantStatus: domain.StatusChangesRequested,
			wantPR:     domain.PRReasonBotComments,
			wantState:  domain.SessionWorking,
		},
		{
			name:       "merge conflicts get distinct PR reason",
			in:         OpenPRInput{MergeConflicts: true, Approved: true},
			wantStatus: domain.StatusPROpen,
			wantPR:     domain.PRReasonMergeConflicts,
			wantState:  domain.SessionWorking,
		},
		{
			name:       "approved + mergeable -> mergeable",
			in:         OpenPRInput{Approved: true, Mergeable: true},
			wantStatus: domain.StatusMergeable,
			wantPR:     domain.PRReasonMergeReady,
			wantState:  domain.SessionIdle,
		},
		{
			name:       "mergeable without formal approval (no required review) -> mergeable",
			in:         OpenPRInput{Mergeable: true},
			wantStatus: domain.StatusMergeable,
			wantPR:     domain.PRReasonMergeReady,
			wantState:  domain.SessionIdle,
		},
		{
			name:       "approved but not mergeable -> approved",
			in:         OpenPRInput{Approved: true},
			wantStatus: domain.StatusApproved,
			wantPR:     domain.PRReasonApproved,
			wantState:  domain.SessionIdle,
		},
		{
			name:       "review pending",
			in:         OpenPRInput{ReviewPending: true},
			wantStatus: domain.StatusReviewPending,
			wantPR:     domain.PRReasonReviewPending,
			wantState:  domain.SessionIdle,
		},
		{
			name:       "idle beyond threshold -> stuck",
			in:         OpenPRInput{IdleBeyond: true},
			wantStatus: domain.StatusStuck,
			wantPR:     domain.PRReasonInProgress,
			wantState:  domain.SessionStuck,
		},
		{
			name:       "review pending wins over idle-beyond",
			in:         OpenPRInput{ReviewPending: true, IdleBeyond: true},
			wantStatus: domain.StatusReviewPending,
			wantPR:     domain.PRReasonReviewPending,
			wantState:  domain.SessionIdle,
		},
		{
			name:       "nothing set -> plain open",
			in:         OpenPRInput{},
			wantStatus: domain.StatusPROpen,
			wantPR:     domain.PRReasonInProgress,
			wantState:  domain.SessionWorking,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ResolveOpenPRDecision(tt.in)
			if got.Status != tt.wantStatus {
				t.Errorf("Status = %q, want %q", got.Status, tt.wantStatus)
			}
			if got.PRReason != tt.wantPR {
				t.Errorf("PRReason = %q, want %q", got.PRReason, tt.wantPR)
			}
			wantPRState := tt.wantPRState
			if wantPRState == "" {
				wantPRState = domain.PROpen
			}
			if got.PRState != wantPRState {
				t.Errorf("PRState = %q, want %q", got.PRState, wantPRState)
			}
			if got.SessionState != tt.wantState {
				t.Errorf("SessionState = %q, want %q", got.SessionState, tt.wantState)
			}
		})
	}
}

func TestResolveOpenPRDecisionEvidence(t *testing.T) {
	tests := []struct {
		name string
		in   OpenPRInput
		want string
	}{
		{
			name: "condition with PR number and URL",
			in:   OpenPRInput{CIFailing: true, Number: 123, URL: "https://example.com/pr/123"},
			want: "ci_failing #123 https://example.com/pr/123",
		},
		{
			name: "condition with number only",
			in:   OpenPRInput{Approved: true, Mergeable: true, Number: 7},
			want: "merge_ready #7",
		},
		{
			name: "no identity falls back to the bare condition",
			in:   OpenPRInput{},
			want: "pr_open",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ResolveOpenPRDecision(tt.in).Evidence; got != tt.want {
				t.Errorf("Evidence = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestDecidersDeriveConsistently(t *testing.T) {
	// Every decision a decider produces must be self-consistent: the display
	// Status it reports must equal what DeriveLegacyStatus produces from the
	// canonical (session, pr) sub-states it emits. This locks the deciders and
	// the display-derivation against drifting apart.
	//
	// The ResolveTerminalPRStateDecision none/open default is intentionally
	// excluded — it is a documented no-op for misuse, not a real verdict.
	var decisions []LifecycleDecision

	for _, in := range []OpenPRInput{
		{Draft: true, CIFailing: true},
		{Draft: true, ChangesRequested: true, Approved: true, Mergeable: true, ReviewPending: true, IdleBeyond: true},
		{CIFailing: true},
		{ChangesRequested: true},
		{BotComments: true},
		{MergeConflicts: true},
		{Approved: true, Mergeable: true},
		{Mergeable: true},
		{Approved: true},
		{ReviewPending: true},
		{IdleBeyond: true},
		{},
	} {
		decisions = append(decisions, ResolveOpenPRDecision(in))
	}

	decisions = append(decisions,
		ResolveTerminalPRStateDecision(domain.PRMerged),
		ResolveTerminalPRStateDecision(domain.PRClosed),
	)

	for _, in := range []ProbeInput{
		{KillRequested: true, Now: t0},
		{Runtime: domain.RuntimeAlive, Process: ProcessAlive, Now: t0},
		{Runtime: domain.RuntimeMissing, Process: ProcessIndeterminate, Now: t0},
		{Runtime: domain.RuntimeExited, Process: ProcessDead, Now: t0},
	} {
		decisions = append(decisions, ResolveProbeDecision(in))
	}

	for _, d := range decisions {
		l := domain.CanonicalSessionLifecycle{
			Session: domain.SessionSubstate{State: d.SessionState, Reason: d.SessionReason},
			PR:      domain.PRSubstate{State: d.PRState, Reason: d.PRReason},
		}
		if got := domain.DeriveLegacyStatus(l); got != d.Status {
			t.Errorf("decision %+v: Status=%q but DeriveLegacyStatus=%q", d, d.Status, got)
		}
	}
}

func TestResolveTerminalPRStateDecision(t *testing.T) {
	tests := []struct {
		name       string
		pr         domain.PRState
		wantStatus domain.SessionStatus
		wantState  domain.SessionState
		wantReason domain.SessionReason
		wantPR     domain.PRReason
	}{
		{
			name:       "merged parks idle awaiting decision",
			pr:         domain.PRMerged,
			wantStatus: domain.StatusMerged,
			wantState:  domain.SessionIdle,
			wantReason: domain.ReasonMergedWaitingDecision,
			wantPR:     domain.PRReasonMerged,
		},
		{
			name:       "closed drops to idle",
			pr:         domain.PRClosed,
			wantStatus: domain.StatusIdle,
			wantState:  domain.SessionIdle,
			wantReason: domain.ReasonAwaitingUserInput,
			wantPR:     domain.PRReasonClosedUnmerged,
		},
		{
			name:       "non-terminal none is a working no-op",
			pr:         domain.PRNone,
			wantStatus: domain.StatusWorking,
			wantState:  domain.SessionWorking,
			wantReason: domain.ReasonTaskInProgress,
		},
		{
			name:       "non-terminal open is a working no-op",
			pr:         domain.PROpen,
			wantStatus: domain.StatusWorking,
			wantState:  domain.SessionWorking,
			wantReason: domain.ReasonTaskInProgress,
		},
		{
			name:       "non-terminal draft is a working no-op",
			pr:         domain.PRDraft,
			wantStatus: domain.StatusWorking,
			wantState:  domain.SessionWorking,
			wantReason: domain.ReasonTaskInProgress,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ResolveTerminalPRStateDecision(tt.pr)
			if got.Status != tt.wantStatus {
				t.Errorf("Status = %q, want %q", got.Status, tt.wantStatus)
			}
			if got.SessionState != tt.wantState {
				t.Errorf("SessionState = %q, want %q", got.SessionState, tt.wantState)
			}
			if got.SessionReason != tt.wantReason {
				t.Errorf("SessionReason = %q, want %q", got.SessionReason, tt.wantReason)
			}
			if tt.wantPR != "" && got.PRReason != tt.wantPR {
				t.Errorf("PRReason = %q, want %q", got.PRReason, tt.wantPR)
			}
		})
	}
}

func TestCreateDetectingDecision(t *testing.T) {
	const ev = "runtime_lost runtime=missing process=indeterminate"
	hash := HashEvidence(ev)

	t.Run("first entry records attempt 1 and stays detecting", func(t *testing.T) {
		got := CreateDetectingDecision(DetectingInput{Evidence: ev, ProposedReason: domain.ReasonRuntimeLost, Now: t0})
		if got.Status != domain.StatusDetecting || got.SessionState != domain.SessionDetecting {
			t.Fatalf("want detecting, got Status=%q State=%q", got.Status, got.SessionState)
		}
		if got.Detecting == nil || got.Detecting.Attempts != 1 {
			t.Fatalf("want attempts=1, got %+v", got.Detecting)
		}
		if !got.Detecting.StartedAt.Equal(t0) {
			t.Errorf("StartedAt = %v, want %v", got.Detecting.StartedAt, t0)
		}
		if got.Detecting.EvidenceHash != hash {
			t.Errorf("EvidenceHash = %q, want %q", got.Detecting.EvidenceHash, hash)
		}
		if got.SessionReason != domain.ReasonRuntimeLost {
			t.Errorf("SessionReason = %q, want %q", got.SessionReason, domain.ReasonRuntimeLost)
		}
	})

	t.Run("unchanged evidence climbs the counter", func(t *testing.T) {
		prior := &domain.DetectingState{Attempts: 1, StartedAt: t0, EvidenceHash: hash}
		got := CreateDetectingDecision(DetectingInput{Evidence: ev, ProposedReason: domain.ReasonRuntimeLost, Prior: prior, Now: t0.Add(time.Minute)})
		if got.Detecting == nil || got.Detecting.Attempts != 2 {
			t.Fatalf("want attempts=2, got %+v", got.Detecting)
		}
		if !got.Detecting.StartedAt.Equal(t0) {
			t.Errorf("StartedAt must be preserved, got %v", got.Detecting.StartedAt)
		}
	})

	t.Run("escalates to stuck on the third unchanged tick", func(t *testing.T) {
		prior := &domain.DetectingState{Attempts: DetectingMaxAttempts - 1, StartedAt: t0, EvidenceHash: hash}
		got := CreateDetectingDecision(DetectingInput{Evidence: ev, ProposedReason: domain.ReasonRuntimeLost, Prior: prior, Now: t0.Add(time.Minute)})
		if got.Status != domain.StatusStuck || got.SessionState != domain.SessionStuck {
			t.Fatalf("want stuck, got Status=%q State=%q", got.Status, got.SessionState)
		}
		if got.Detecting != nil {
			t.Errorf("stuck decision must drop detecting memory, got %+v", got.Detecting)
		}
		if got.SessionReason != domain.ReasonRuntimeLost {
			t.Errorf("escalation should carry the why, got %q", got.SessionReason)
		}
	})

	t.Run("changing evidence resets the counter but preserves StartedAt", func(t *testing.T) {
		prior := &domain.DetectingState{Attempts: DetectingMaxAttempts - 1, StartedAt: t0, EvidenceHash: hash}
		got := CreateDetectingDecision(DetectingInput{Evidence: "different evidence", ProposedReason: domain.ReasonRuntimeLost, Prior: prior, Now: t0.Add(time.Minute)})
		if got.Status != domain.StatusDetecting {
			t.Fatalf("changed evidence should stay detecting, got %q", got.Status)
		}
		if got.Detecting == nil || got.Detecting.Attempts != 1 {
			t.Fatalf("counter should reset to 1, got %+v", got.Detecting)
		}
		if !got.Detecting.StartedAt.Equal(t0) {
			t.Errorf("StartedAt must survive an evidence change, got %v", got.Detecting.StartedAt)
		}
	})

	t.Run("duration cap escalates even below the attempt count", func(t *testing.T) {
		prior := &domain.DetectingState{Attempts: 1, StartedAt: t0, EvidenceHash: hash}
		got := CreateDetectingDecision(DetectingInput{Evidence: ev, ProposedReason: domain.ReasonRuntimeLost, Prior: prior, Now: t0.Add(DetectingMaxDuration)})
		if got.Status != domain.StatusStuck {
			t.Fatalf("want stuck from duration cap, got %q", got.Status)
		}
	})

	t.Run("duration cap fires even when evidence keeps flapping", func(t *testing.T) {
		prior := &domain.DetectingState{Attempts: 1, StartedAt: t0, EvidenceHash: hash}
		got := CreateDetectingDecision(DetectingInput{Evidence: "ever-changing", ProposedReason: domain.ReasonRuntimeLost, Prior: prior, Now: t0.Add(DetectingMaxDuration + time.Minute)})
		if got.Status != domain.StatusStuck {
			t.Fatalf("duration cap must override a reset counter, got %q", got.Status)
		}
	})
}

func TestProbeDetectingEscalationFlow(t *testing.T) {
	// An unchanging ambiguous probe should escalate to stuck after exactly
	// DetectingMaxAttempts ticks.
	in := ProbeInput{Runtime: domain.RuntimeMissing, Process: ProcessIndeterminate, Now: t0}
	d := ResolveProbeDecision(in)
	for i := 1; i < DetectingMaxAttempts; i++ {
		if d.Status != domain.StatusDetecting {
			t.Fatalf("tick %d: expected detecting, got %q", i, d.Status)
		}
		in.Prior = d.Detecting
		in.Now = t0.Add(time.Duration(i) * time.Second)
		d = ResolveProbeDecision(in)
	}
	if d.Status != domain.StatusStuck {
		t.Fatalf("expected escalation to stuck after %d ticks, got %q", DetectingMaxAttempts, d.Status)
	}
}

func TestHashEvidence(t *testing.T) {
	t.Run("identical strings hash identically", func(t *testing.T) {
		if HashEvidence("same input") != HashEvidence("same input") {
			t.Error("identical evidence must hash equal")
		}
	})

	t.Run("different evidence hashes differently", func(t *testing.T) {
		if HashEvidence("runtime_lost") == HashEvidence("agent_process_exited") {
			t.Error("distinct evidence must hash differently")
		}
	})

	t.Run("only the timestamp differs -> equal hash", func(t *testing.T) {
		a := "probe failed at 2026-05-26T12:00:00Z runtime=missing"
		b := "probe failed at 2026-05-26T12:05:43.218Z runtime=missing"
		if HashEvidence(a) != HashEvidence(b) {
			t.Errorf("restamped evidence should hash equal:\n a=%q\n b=%q", a, b)
		}
	})

	t.Run("bare time-of-day stripped", func(t *testing.T) {
		if HashEvidence("idle since 12:00:00") != HashEvidence("idle since 13:30:59") {
			t.Error("time-of-day differences should be stripped")
		}
	})

	t.Run("unix epoch stripped", func(t *testing.T) {
		if HashEvidence("last seen 1716724800") != HashEvidence("last seen 1716728400") {
			t.Error("epoch differences should be stripped")
		}
	})

	t.Run("a real content change still changes the hash", func(t *testing.T) {
		a := "probe at 2026-05-26T12:00:00Z runtime=missing"
		b := "probe at 2026-05-26T12:00:00Z runtime=alive"
		if HashEvidence(a) == HashEvidence(b) {
			t.Error("non-timestamp content change must change the hash")
		}
	})

	t.Run("whitespace differences are normalised", func(t *testing.T) {
		if HashEvidence("runtime=missing   process=dead") != HashEvidence("runtime=missing process=dead") {
			t.Error("collapsed whitespace should hash equal")
		}
	})
}
