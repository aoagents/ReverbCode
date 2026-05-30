package domain

import "testing"

func TestDeriveLegacyStatus(t *testing.T) {
	tests := []struct {
		name string
		in   CanonicalSessionLifecycle
		want SessionStatus
	}{
		{
			name: "not_started maps to spawning",
			in:   CanonicalSessionLifecycle{Session: SessionSubstate{State: SessionNotStarted, Reason: ReasonSpawnRequested}},
			want: StatusSpawning,
		},
		{
			name: "terminated+manually_killed maps to killed",
			in:   CanonicalSessionLifecycle{Session: SessionSubstate{State: SessionTerminated, Reason: ReasonManuallyKilled}},
			want: StatusKilled,
		},
		{
			name: "terminated+auto_cleanup maps to cleanup",
			in:   CanonicalSessionLifecycle{Session: SessionSubstate{State: SessionTerminated, Reason: ReasonAutoCleanup}},
			want: StatusCleanup,
		},
		{
			name: "terminated+error maps to errored",
			in:   CanonicalSessionLifecycle{Session: SessionSubstate{State: SessionTerminated, Reason: ReasonErrorInProcess}},
			want: StatusErrored,
		},
		{
			name: "hard state needs_input maps directly",
			in:   CanonicalSessionLifecycle{Session: SessionSubstate{State: SessionNeedsInput}},
			want: StatusNeedsInput,
		},
		{
			name: "merged PR dominates an idle session",
			in: CanonicalSessionLifecycle{
				Session: SessionSubstate{State: SessionIdle},
				PR:      PRSubstate{State: PRMerged},
			},
			want: StatusMerged,
		},
		{
			name: "open PR with failing CI dominates idle session",
			in: CanonicalSessionLifecycle{
				Session: SessionSubstate{State: SessionIdle},
				PR:      PRSubstate{State: PROpen, Reason: PRReasonCIFailing},
			},
			want: StatusCIFailed,
		},
		{
			name: "draft PR with failing CI maps to ci_failed",
			in: CanonicalSessionLifecycle{
				Session: SessionSubstate{State: SessionWorking},
				PR:      PRSubstate{State: PRDraft, Reason: PRReasonCIFailing},
			},
			want: StatusCIFailed,
		},
		{
			name: "draft PR ignores review and merge reasons",
			in: CanonicalSessionLifecycle{
				Session: SessionSubstate{State: SessionWorking},
				PR:      PRSubstate{State: PRDraft, Reason: PRReasonMergeReady},
			},
			want: StatusDraft,
		},
		{
			name: "open PR bot comments display as changes_requested",
			in: CanonicalSessionLifecycle{
				Session: SessionSubstate{State: SessionWorking},
				PR:      PRSubstate{State: PROpen, Reason: PRReasonBotComments},
			},
			want: StatusChangesRequested,
		},
		{
			name: "open PR merge conflicts display as plain open",
			in: CanonicalSessionLifecycle{
				Session: SessionSubstate{State: SessionWorking},
				PR:      PRSubstate{State: PROpen, Reason: PRReasonMergeConflicts},
			},
			want: StatusPROpen,
		},
		{
			name: "open PR approved",
			in: CanonicalSessionLifecycle{
				Session: SessionSubstate{State: SessionWorking},
				PR:      PRSubstate{State: PROpen, Reason: PRReasonApproved},
			},
			want: StatusApproved,
		},
		{
			name: "open PR merge_ready maps to mergeable",
			in: CanonicalSessionLifecycle{
				Session: SessionSubstate{State: SessionWorking},
				PR:      PRSubstate{State: PROpen, Reason: PRReasonMergeReady},
			},
			want: StatusMergeable,
		},
		{
			name: "no PR falls through to idle",
			in:   CanonicalSessionLifecycle{Session: SessionSubstate{State: SessionIdle}},
			want: StatusIdle,
		},
		{
			name: "no PR falls through to working",
			in:   CanonicalSessionLifecycle{Session: SessionSubstate{State: SessionWorking}},
			want: StatusWorking,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := DeriveLegacyStatus(tt.in); got != tt.want {
				t.Errorf("DeriveLegacyStatus() = %q, want %q", got, tt.want)
			}
		})
	}
}
