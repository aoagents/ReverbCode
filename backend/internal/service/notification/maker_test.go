package notification

import (
	"context"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func TestDefaultMakerCopyForV1Types(t *testing.T) {
	maker := DefaultMaker{}
	for _, tc := range []struct {
		typ     domain.NotificationType
		wantT   string
		wantSub string
	}{
		{domain.NotificationCIFailing, "CI failed", "failing check"},
		{domain.NotificationReviewChanges, "Changes requested", "Review feedback"},
		{domain.NotificationMergeConflicts, "Merge conflicts", "rebase"},
		{domain.NotificationMergeReady, "Ready to merge", "approved and green"},
		{domain.NotificationMergeCompleted, "Merged", "was merged"},
		{domain.NotificationSessionInput, "Input needed", "waiting for you"},
		{domain.NotificationSessionExited, "Session exited", "stopped unexpectedly"},
	} {
		t.Run(string(tc.typ), func(t *testing.T) {
			got, err := maker.Make(context.Background(), MakeInput{
				Intent: domain.NotificationIntent{Type: tc.typ, SessionID: "mer-1"},
				Facts:  EnrichedFacts{SessionLabel: "mer-1", FailedChecks: []domain.PullRequestCheck{{Name: "build"}}},
			})
			if err != nil {
				t.Fatal(err)
			}
			if got.Title != tc.wantT || !strings.Contains(got.Summary, tc.wantSub) {
				t.Fatalf("content = %+v", got)
			}
			if len([]rune(got.Title)) > 40 || len([]rune(got.Summary)) > 120 {
				t.Fatalf("copy too long: %+v", got)
			}
		})
	}
}

func TestDefaultMakerKeepsEvidenceOutOfSummary(t *testing.T) {
	longLog := strings.Repeat("boom ", 100)
	got, err := (DefaultMaker{}).Make(context.Background(), MakeInput{
		Intent: domain.NotificationIntent{Type: domain.NotificationCIFailing, SessionID: "mer-1", Context: domain.NotificationIntentContext{Facts: map[string]any{"logTail": longLog}}},
		Facts:  EnrichedFacts{SessionLabel: "mer-1", FailedChecks: []domain.PullRequestCheck{{Name: "build", LogTail: longLog}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(got.Summary, "boom") {
		t.Fatalf("summary leaked log evidence: %q", got.Summary)
	}
}
