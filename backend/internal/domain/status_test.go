package domain

import "testing"

func rec(activity ActivityState, terminated bool) SessionRecord {
	return SessionRecord{Activity: ActivitySubstate{State: activity}, IsTerminated: terminated}
}

func pr(facts PRFacts) *PRFacts { return &facts }

func TestDeriveStatusFromSessionFactsAndPR(t *testing.T) {
	tests := []struct {
		name string
		rec  SessionRecord
		pr   *PRFacts
		want SessionStatus
	}{
		{"terminated", rec(ActivityExited, true), nil, StatusTerminated},
		{"merged-pr", rec(ActivityIdle, true), pr(PRFacts{Merged: true}), StatusMerged},
		{"needs-input", rec(ActivityWaitingInput, false), pr(PRFacts{CI: CIFailing}), StatusNeedsInput},
		{"blocked", rec(ActivityBlocked, false), pr(PRFacts{CI: CIFailing}), StatusStuck},
		{"ci-failed", rec(ActivityIdle, false), pr(PRFacts{CI: CIFailing}), StatusCIFailed},
		{"draft", rec(ActivityIdle, false), pr(PRFacts{Draft: true}), StatusDraft},
		{"changes-requested", rec(ActivityIdle, false), pr(PRFacts{Review: ReviewChangesRequest}), StatusChangesRequested},
		{"mergeable", rec(ActivityIdle, false), pr(PRFacts{Mergeability: MergeMergeable}), StatusMergeable},
		{"approved", rec(ActivityIdle, false), pr(PRFacts{Review: ReviewApproved}), StatusApproved},
		{"review-pending", rec(ActivityIdle, false), pr(PRFacts{Review: ReviewRequired}), StatusReviewPending},
		{"pr-open", rec(ActivityIdle, false), pr(PRFacts{}), StatusPROpen},
		{"working", rec(ActivityActive, false), nil, StatusWorking},
		{"idle", rec(ActivityIdle, false), nil, StatusIdle},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := DeriveStatus(tt.rec, tt.pr); got != tt.want {
				t.Fatalf("got %q, want %q", got, tt.want)
			}
		})
	}
}
