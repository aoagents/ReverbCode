package terminal

import (
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func TestAttentionLevelMapping(t *testing.T) {
	cases := []struct {
		name     string
		status   domain.SessionStatus
		activity domain.ActivityState
		want     string
	}{
		// Status-driven zones take priority over activity.
		{"merged is done", domain.StatusMerged, domain.ActivityActive, "done"},
		{"terminated is done", domain.StatusTerminated, domain.ActivityActive, "done"},
		{"mergeable is merge", domain.StatusMergeable, domain.ActivityIdle, "merge"},
		{"approved is merge", domain.StatusApproved, domain.ActivityIdle, "merge"},
		{"needs_input is respond", domain.StatusNeedsInput, domain.ActivityActive, "respond"},
		{"ci_failed is review", domain.StatusCIFailed, domain.ActivityActive, "review"},
		{"changes_requested is review", domain.StatusChangesRequested, domain.ActivityActive, "review"},
		{"review_pending is pending", domain.StatusReviewPending, domain.ActivityActive, "pending"},

		// Activity-driven zones apply only when status is non-actionable.
		{"idle+exited is respond", domain.StatusIdle, domain.ActivityExited, "respond"},
		{"idle+waiting_input is respond", domain.StatusIdle, domain.ActivityWaitingInput, "respond"},
		{"working+active is working", domain.StatusWorking, domain.ActivityActive, "working"},
		{"idle+idle is working", domain.StatusIdle, domain.ActivityIdle, "working"},

		// Non-actionable PR statuses fall through to working.
		{"draft is working", domain.StatusDraft, domain.ActivityActive, "working"},
		{"pr_open is working", domain.StatusPROpen, domain.ActivityActive, "working"},

		// A done/merge status wins even when the agent has exited.
		{"merged+exited stays done", domain.StatusMerged, domain.ActivityExited, "done"},
		{"mergeable+exited stays merge", domain.StatusMergeable, domain.ActivityExited, "merge"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := attentionLevel(tc.status, tc.activity); got != tc.want {
				t.Fatalf("attentionLevel(%q, %q) = %q, want %q", tc.status, tc.activity, got, tc.want)
			}
		})
	}
}

func TestToSessionPatchShape(t *testing.T) {
	ts := time.Date(2024, 1, 2, 3, 4, 5, 0, time.FixedZone("EST", -5*3600))
	s := domain.Session{
		SessionRecord: domain.SessionRecord{
			ID:       "s1",
			Activity: domain.Activity{State: domain.ActivityWaitingInput, LastActivityAt: ts},
		},
		Status: domain.StatusNeedsInput,
	}
	got := toSessionPatch(s)
	want := sessionPatch{
		ID:             "s1",
		Status:         "needs_input",
		Activity:       "waiting_input",
		AttentionLevel: "respond",
		LastActivityAt: "2024-01-02T08:04:05Z", // normalized to UTC
	}
	if got != want {
		t.Fatalf("toSessionPatch:\n got %+v\nwant %+v", got, want)
	}
}

func TestToSessionPatchesPreservesOrder(t *testing.T) {
	in := []domain.Session{
		{SessionRecord: domain.SessionRecord{ID: "a"}, Status: domain.StatusIdle},
		{SessionRecord: domain.SessionRecord{ID: "b"}, Status: domain.StatusWorking},
	}
	got := toSessionPatches(in)
	if len(got) != 2 || got[0].ID != "a" || got[1].ID != "b" {
		t.Fatalf("toSessionPatches order/len wrong: %+v", got)
	}
}
