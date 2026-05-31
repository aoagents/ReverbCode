package notification

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func TestActionAllowlistAcceptsSafeActionsAndRejectsCallbacks(t *testing.T) {
	row := actionTestNotification()
	if err := ValidateAction(row, domain.NotificationAction{ID: "open-session", Kind: "open-session", Route: "/projects/ao/sessions/ao-1"}); err != nil {
		t.Fatalf("safe route rejected: %v", err)
	}
	if err := ValidateAction(row, domain.NotificationAction{ID: "open-pr", Kind: "open-pr", URL: "https://github.com/aoagents/agent-orchestrator/pull/1"}); err != nil {
		t.Fatalf("safe url rejected: %v", err)
	}
	if err := ValidateAction(row, domain.NotificationAction{ID: "open-session", Kind: "open-session", Route: "javascript:alert(1)"}); err == nil {
		t.Fatal("unsafe internal route should be rejected")
	}
	if err := ValidateAction(row, domain.NotificationAction{ID: "callback", Kind: "callback", Method: "POST"}); err == nil {
		t.Fatal("arbitrary callback action should be rejected")
	}
}

func TestExternalURLHostAllowlist(t *testing.T) {
	allowed := []string{
		"https://github.com/aoagents/agent-orchestrator/pull/1",
		"https://gitlab.com/group/project/-/merge_requests/1",
		"https://linear.app/ao/issue/AO-1",
		"https://sub.github.com/path",
	}
	for _, raw := range allowed {
		if err := ValidateExternalURL(raw); err != nil {
			t.Fatalf("%s rejected: %v", raw, err)
		}
	}
	rejected := []string{
		"http://github.com/aoagents/agent-orchestrator/pull/1",
		"javascript:alert(1)",
		"data:text/plain,hi",
		"https://evil.example/pr/1",
		"https://github.com.evil.example/pr/1",
	}
	for _, raw := range rejected {
		if err := ValidateExternalURL(raw); err == nil {
			t.Fatalf("%s should be rejected", raw)
		}
	}
}

func TestExecuteMergePRPreconditionFailure(t *testing.T) {
	store := &memoryActionStore{row: actionTestNotification()}
	result, err := ExecuteAction(context.Background(), store, string(store.row.ID), "merge-pr", time.Now().UTC())
	if err == nil {
		t.Fatalf("merge-pr should fail precondition, got result=%+v", result)
	}
	ae, ok := IsActionError(err)
	if !ok || ae.Code != "ACTION_PRECONDITION_FAILED" || ae.StatusCode != 409 {
		t.Fatalf("merge-pr err = %#v ok=%v", ae, ok)
	}
}

func actionTestNotification() domain.Notification {
	return domain.Notification{
		ID:           "ntf_action",
		ProjectID:    "ao",
		SessionID:    "ao-1",
		SemanticType: "session.needs_input",
		Priority:     "urgent",
		Payload:      json.RawMessage(`{"schemaVersion":3,"subject":{"pr":{"url":"https://github.com/aoagents/agent-orchestrator/pull/1"}}}`),
		Actions: []domain.NotificationAction{
			{ID: "open-session", Kind: "open-session", Label: "Open session", Route: "/projects/ao/sessions/ao-1"},
			{ID: "open-pr", Kind: "open-pr", Label: "Open PR", URL: "https://github.com/aoagents/agent-orchestrator/pull/1"},
			{ID: "merge-pr", Kind: "merge-pr", Label: "Merge PR"},
		},
	}
}

type memoryActionStore struct {
	row domain.Notification
}

func (s *memoryActionStore) GetNotification(context.Context, string) (domain.Notification, bool, error) {
	return s.row, true, nil
}

func (s *memoryActionStore) MarkNotificationRead(_ context.Context, _ string, at time.Time) (domain.Notification, bool, error) {
	s.row.ReadAt = at
	return s.row, true, nil
}

func (s *memoryActionStore) ArchiveNotification(_ context.Context, _ string, at time.Time) (domain.Notification, bool, error) {
	s.row.ArchivedAt = at
	return s.row, true, nil
}
