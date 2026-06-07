package domain

import (
	"strings"
	"testing"
	"time"
)

func validIntent() NotificationIntent {
	return NotificationIntent{
		Type:       NotificationCIFailing,
		Priority:   NotificationWarning,
		ProjectID:  "mer",
		SessionID:  "mer-1",
		Source:     "test",
		DedupeKey:  "ci:pr:build:c1",
		OccurredAt: time.Now(),
	}
}

func TestNotificationIntentValidateRejectsMissingRequiredFields(t *testing.T) {
	for _, tc := range []struct {
		name string
		mut  func(*NotificationIntent)
		want string
	}{
		{"type", func(i *NotificationIntent) { i.Type = "" }, "type"},
		{"priority", func(i *NotificationIntent) { i.Priority = "" }, "priority"},
		{"project", func(i *NotificationIntent) { i.ProjectID = "" }, "project"},
		{"session", func(i *NotificationIntent) { i.SessionID = "" }, "session"},
		{"source", func(i *NotificationIntent) { i.Source = "" }, "source"},
		{"dedupe", func(i *NotificationIntent) { i.DedupeKey = "" }, "dedupe"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := validIntent()
			tc.mut(&in)
			err := in.Validate()
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Validate err = %v, want mention %q", err, tc.want)
			}
		})
	}
}

func TestNotificationConstantsMatchStorageValues(t *testing.T) {
	if NotificationUrgent != "urgent" || NotificationPriorityAction != "action" || NotificationWarning != "warning" || NotificationInfo != "info" {
		t.Fatalf("priority constants changed")
	}
	if NotificationUnread != "unread" || NotificationRead != "read" || NotificationDismissed != "dismissed" || NotificationResolved != "resolved" {
		t.Fatalf("status constants changed")
	}
}

func TestNotificationValidateRequiresSessionID(t *testing.T) {
	now := time.Now()
	n := Notification{
		ID:          "n1",
		Type:        NotificationCIFailing,
		Priority:    NotificationWarning,
		Status:      NotificationUnread,
		ProjectID:   "mer",
		Source:      "test",
		DedupeKey:   "ci:pr:build:c1",
		Fingerprint: "fp",
		Title:       "CI failed",
		Summary:     "mer-1 has 1 failing check.",
		OccurredAt:  now,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	err := n.Validate()
	if err == nil || !strings.Contains(err.Error(), "session") {
		t.Fatalf("Validate err = %v, want missing session", err)
	}
}

func TestNotificationActionPayloadIsJSONSafe(t *testing.T) {
	a := NotificationAction{ID: "open_session", Label: "Open", Kind: "route", Route: "session", Payload: map[string]any{"sessionId": SessionID("mer-1")}}
	if err := a.Validate(); err != nil {
		t.Fatalf("action should validate: %v", err)
	}
}
