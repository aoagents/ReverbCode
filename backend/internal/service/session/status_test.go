package session

import (
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

var statusNow = time.Date(2026, 6, 10, 12, 0, 0, 0, time.UTC)

// statusRec builds a session whose agent HAS delivered a hook signal; the
// no-signal cases below zero FirstSignalAt explicitly.
func statusRec(activity domain.ActivityState, terminated bool) domain.SessionRecord {
	return domain.SessionRecord{
		Activity:      domain.Activity{State: activity, LastActivityAt: statusNow},
		FirstSignalAt: statusNow,
		IsTerminated:  terminated,
	}
}

// silentRec builds a live session that has never delivered a hook signal,
// seeded (spawned/restored) `age` before the derivation time.
func silentRec(age time.Duration) domain.SessionRecord {
	return domain.SessionRecord{
		Activity: domain.Activity{State: domain.ActivityIdle, LastActivityAt: statusNow.Add(-age)},
	}
}

func statusPR(facts domain.PRFacts) []domain.PRFacts { return []domain.PRFacts{facts} }

func TestServiceDerivesStatusFromSessionFactsAndPR(t *testing.T) {
	tests := []struct {
		name string
		rec  domain.SessionRecord
		pr   []domain.PRFacts
		// hookless marks a harness with no activity pipeline (signalCapable
		// false): silence is its permanent normal state, never stalled.
		hookless bool
		want     domain.SessionStatus
	}{
		// Terminated and merged both collapse to Idle.
		{"terminated", statusRec(domain.ActivityExited, true), nil, false, domain.StatusIdle},
		{"merged-pr", statusRec(domain.ActivityIdle, true), statusPR(domain.PRFacts{Merged: true}), false, domain.StatusIdle},

		// waiting_input outranks every PR fact.
		{"needs-input", statusRec(domain.ActivityWaitingInput, false), statusPR(domain.PRFacts{CI: domain.CIFailing}), false, domain.StatusNeedsInput},

		// Stopped on an unfinished PR is Stalled, not Ready: the agent had the
		// move and quit.
		{"stopped-ci-failed", statusRec(domain.ActivityIdle, false), statusPR(domain.PRFacts{CI: domain.CIFailing}), false, domain.StatusStalled},
		{"stopped-draft", statusRec(domain.ActivityIdle, false), statusPR(domain.PRFacts{Draft: true}), false, domain.StatusStalled},
		{"stopped-changes-requested", statusRec(domain.ActivityIdle, false), statusPR(domain.PRFacts{Review: domain.ReviewChangesRequest}), false, domain.StatusStalled},
		{"stopped-conflicting", statusRec(domain.ActivityIdle, false), statusPR(domain.PRFacts{Mergeability: domain.MergeConflicting}), false, domain.StatusStalled},

		// An active agent on top of any PR keeps Working (active-wins).
		{"active-on-unfinished-pr", statusRec(domain.ActivityActive, false), statusPR(domain.PRFacts{CI: domain.CIFailing}), false, domain.StatusWorking},
		{"active-on-clean-pr", statusRec(domain.ActivityActive, false), statusPR(domain.PRFacts{Mergeability: domain.MergeMergeable}), false, domain.StatusWorking},

		// Stopped on a clean PR is Ready.
		{"stopped-mergeable", statusRec(domain.ActivityIdle, false), statusPR(domain.PRFacts{Mergeability: domain.MergeMergeable}), false, domain.StatusReady},
		{"stopped-approved", statusRec(domain.ActivityIdle, false), statusPR(domain.PRFacts{Review: domain.ReviewApproved}), false, domain.StatusReady},
		{"stopped-review-required", statusRec(domain.ActivityIdle, false), statusPR(domain.PRFacts{Review: domain.ReviewRequired}), false, domain.StatusReady},

		// Bare open PR behaves as no PR: stopped reads Idle.
		{"stopped-bare-pr-open", statusRec(domain.ActivityIdle, false), statusPR(domain.PRFacts{}), false, domain.StatusIdle},

		{"working", statusRec(domain.ActivityActive, false), nil, false, domain.StatusWorking},
		{"idle", statusRec(domain.ActivityIdle, false), nil, false, domain.StatusIdle},

		// A live session whose hook-capable agent never signaled is Stalled
		// once the boot grace passes — never a confident idle.
		{"never-booted-after-grace", silentRec(2 * bootGrace), nil, false, domain.StatusStalled},
		// A hook-less harness can never signal: its silence stays idle forever.
		{"hookless-silent-stays-idle", silentRec(2 * bootGrace), nil, true, domain.StatusIdle},
		// Right after spawn the agent legitimately hasn't called back yet.
		{"silent-within-grace-is-idle", silentRec(10 * time.Second), nil, false, domain.StatusIdle},

		// Termination outranks the never-booted downgrade.
		{
			"terminated-outranks-never-booted",
			domain.SessionRecord{Activity: domain.Activity{State: domain.ActivityExited, LastActivityAt: statusNow.Add(-2 * bootGrace)}, IsTerminated: true},
			nil,
			false,
			domain.StatusIdle,
		},
		// Never-booted silence outranks a bare open PR (neutral → idle anyway).
		{"never-booted-bare-pr", silentRec(2 * bootGrace), statusPR(domain.PRFacts{}), false, domain.StatusStalled},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := deriveStatus(tt.rec, tt.pr, statusNow, !tt.hookless); got != tt.want {
				t.Fatalf("got %q want %q", got, tt.want)
			}
		})
	}
}

// An active session gone silent past hangTimeout is caught as Stalled instead
// of reading a calm Working; within the timeout it stays Working.
func TestDeriveStatusHungActiveSessionStalls(t *testing.T) {
	hung := domain.SessionRecord{
		Activity:      domain.Activity{State: domain.ActivityActive, LastActivityAt: statusNow.Add(-2 * hangTimeout)},
		FirstSignalAt: statusNow.Add(-3 * hangTimeout),
	}
	if got := deriveStatus(hung, nil, statusNow, true); got != domain.StatusStalled {
		t.Fatalf("got %q want stalled", got)
	}
	live := domain.SessionRecord{
		Activity:      domain.Activity{State: domain.ActivityActive, LastActivityAt: statusNow.Add(-1 * time.Minute)},
		FirstSignalAt: statusNow.Add(-1 * time.Hour),
	}
	if got := deriveStatus(live, nil, statusNow, true); got != domain.StatusWorking {
		t.Fatalf("got %q want working", got)
	}
}

// A blocked stacked child cannot merge until its parent does, so its readiness
// is suppressed, but its problem signals still surface as unfinished work.
func TestStackedChildSignals(t *testing.T) {
	parent := domain.PRFacts{URL: "parent", SourceBranch: "feat", Mergeability: domain.MergeMergeable}
	child := func(f domain.PRFacts) domain.PRFacts {
		f.URL = "child"
		f.SourceBranch = "feat/child"
		f.TargetBranch = "feat"
		return f
	}
	tests := []struct {
		name string
		prs  []domain.PRFacts
		want domain.SessionStatus
	}{
		// A blocked child's problem drags the stopped session to Stalled.
		{"blocked-child-ci-failing-stalls", []domain.PRFacts{parent, child(domain.PRFacts{CI: domain.CIFailing})}, domain.StatusStalled},
		{"blocked-child-draft-stalls", []domain.PRFacts{parent, child(domain.PRFacts{Draft: true})}, domain.StatusStalled},
		{"blocked-child-changes-requested-stalls", []domain.PRFacts{parent, child(domain.PRFacts{Review: domain.ReviewChangesRequest})}, domain.StatusStalled},
		{"blocked-child-unresolved-comments-stalls", []domain.PRFacts{parent, child(domain.PRFacts{ReviewComments: true})}, domain.StatusStalled},
		// A blocked child's readiness stays hidden: the parent's clean state
		// alone drives the session to Ready.
		{"blocked-child-mergeable-suppressed", []domain.PRFacts{parent, child(domain.PRFacts{Mergeability: domain.MergeMergeable})}, domain.StatusReady},
		{"blocked-child-approved-suppressed", []domain.PRFacts{parent, child(domain.PRFacts{Review: domain.ReviewApproved})}, domain.StatusReady},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := deriveStatus(statusRec(domain.ActivityIdle, false), tt.prs, statusNow, true); got != tt.want {
				t.Fatalf("got %q want %q", got, tt.want)
			}
		})
	}
}

// Without an injected capability predicate the service must never claim a
// signal-driven stall; with one, capability follows the predicate per harness.
func TestHarnessSignalsCapabilityGate(t *testing.T) {
	if (&Service{}).harnessSignals(domain.HarnessCodex) {
		t.Fatal("zero-value Service reports signal-capable; want incapable (never stalled on silence)")
	}
	s := NewWithDeps(Deps{SignalCapable: func(h domain.AgentHarness) bool { return h == domain.HarnessCodex }})
	if !s.harnessSignals(domain.HarnessCodex) {
		t.Fatal("harnessSignals(codex) = false with codex-capable predicate")
	}
	if s.harnessSignals(domain.HarnessAmp) {
		t.Fatal("harnessSignals(amp) = true with codex-only predicate")
	}
}
