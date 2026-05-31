package domain

import "testing"

func TestDeriveStatus(t *testing.T) {
	// sess builds a non-terminal lifecycle (no reason).
	sess := func(s SessionState) CanonicalSessionLifecycle {
		return CanonicalSessionLifecycle{Session: SessionSubstate{State: s}}
	}
	// term builds a terminated lifecycle carrying a TerminationReason.
	term := func(r TerminationReason) CanonicalSessionLifecycle {
		return CanonicalSessionLifecycle{Session: SessionSubstate{State: SessionTerminated}, TerminationReason: r}
	}
	openPR := func(mut func(*PRFacts)) PRFacts {
		f := PRFacts{Exists: true, CI: CIUnknown, Review: ReviewNone, Mergeability: MergeUnknown}
		if mut != nil {
			mut(&f)
		}
		return f
	}

	tests := []struct {
		name string
		in   CanonicalSessionLifecycle
		pr   PRFacts
		want SessionStatus
	}{
		{"not_started maps to spawning", sess(SessionNotStarted), PRFacts{}, StatusSpawning},
		{"terminated+manually_killed -> killed", term(TermManuallyKilled), PRFacts{}, StatusKilled},
		{"terminated+runtime_lost -> killed", term(TermRuntimeLost), PRFacts{}, StatusKilled},
		{"terminated+auto_cleanup -> cleanup", term(TermAutoCleanup), PRFacts{}, StatusCleanup},
		{"terminated+pr_merged -> cleanup", term(TermPRMerged), PRFacts{}, StatusCleanup},
		{"terminated+error -> errored", term(TermErrorInProcess), PRFacts{}, StatusErrored},
		{"needs_input maps directly", sess(SessionNeedsInput), PRFacts{}, StatusNeedsInput},
		{"stuck dominates any PR", sess(SessionStuck), openPR(func(f *PRFacts) { f.CI = CIFailing }), StatusStuck},

		{"no PR + idle -> idle", sess(SessionIdle), PRFacts{}, StatusIdle},
		{"no PR + working -> working", sess(SessionWorking), PRFacts{}, StatusWorking},

		{"merged PR dominates idle session", sess(SessionIdle), PRFacts{Exists: true, Merged: true}, StatusMerged},
		{"open PR failing CI -> ci_failed", sess(SessionIdle), openPR(func(f *PRFacts) { f.CI = CIFailing }), StatusCIFailed},
		{"draft PR failing CI -> ci_failed (CI dominates)", sess(SessionWorking), openPR(func(f *PRFacts) { f.Draft = true; f.CI = CIFailing }), StatusCIFailed},
		{"draft PR ignores review state -> draft", sess(SessionWorking), openPR(func(f *PRFacts) { f.Draft = true; f.Review = ReviewApproved }), StatusDraft},
		{"open PR changes_requested", sess(SessionWorking), openPR(func(f *PRFacts) { f.Review = ReviewChangesRequest }), StatusChangesRequested},
		{"open PR review comments -> changes_requested", sess(SessionWorking), openPR(func(f *PRFacts) { f.ReviewComments = true }), StatusChangesRequested},
		{"open PR mergeable", sess(SessionWorking), openPR(func(f *PRFacts) { f.Mergeability = MergeMergeable }), StatusMergeable},
		{"open PR approved", sess(SessionWorking), openPR(func(f *PRFacts) { f.Review = ReviewApproved }), StatusApproved},
		{"open PR review required -> review_pending", sess(SessionWorking), openPR(func(f *PRFacts) { f.Review = ReviewRequired }), StatusReviewPending},
		{"open PR no signal -> pr_open", sess(SessionWorking), openPR(nil), StatusPROpen},
		{"closed PR falls through to soft state", sess(SessionIdle), PRFacts{Exists: true, Closed: true}, StatusIdle},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := DeriveStatus(tt.in, tt.pr); got != tt.want {
				t.Errorf("DeriveStatus() = %q, want %q", got, tt.want)
			}
		})
	}
}
