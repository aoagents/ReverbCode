package store_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/cdc"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func notificationEvents(ctx context.Context, t *testing.T, s interface {
	EventsAfter(context.Context, int64, int) ([]cdc.Event, error)
}) []cdc.Event {
	t.Helper()
	events, err := s.EventsAfter(ctx, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	var out []cdc.Event
	for _, ev := range events {
		if ev.Type == cdc.EventNotificationCreated || ev.Type == cdc.EventNotificationUpdated {
			out = append(out, ev)
		}
	}
	return out
}

func TestNotificationCDCInsertUpdateNoopAndResolve(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	rec := seedNotificationSession(ctx, t, s)
	now := time.Now().UTC().Truncate(time.Second)
	n := sampleNotification("n1", rec.ProjectID, rec.ID, "ci:1", "fp1", now)
	if _, _, err := s.UpsertNotification(ctx, n); err != nil {
		t.Fatal(err)
	}
	if events := notificationEvents(ctx, t, s); len(events) != 1 || events[0].Type != cdc.EventNotificationCreated {
		t.Fatalf("after insert events = %+v", events)
	}
	// Same fingerprint is a store no-op and must not append change_log rows.
	if _, _, err := s.UpsertNotification(ctx, n); err != nil {
		t.Fatal(err)
	}
	if events := notificationEvents(ctx, t, s); len(events) != 1 {
		t.Fatalf("same fingerprint emitted extra event: %+v", events)
	}
	updated := n
	updated.Fingerprint = "fp2"
	updated.Summary = "updated"
	updated.UpdatedAt = now.Add(time.Minute)
	if _, _, err := s.UpsertNotification(ctx, updated); err != nil {
		t.Fatal(err)
	}
	if events := notificationEvents(ctx, t, s); len(events) != 2 || events[1].Type != cdc.EventNotificationUpdated {
		t.Fatalf("after update events = %+v", events)
	}
	if _, err := s.ResolveNotifications(ctx, domain.NotificationResolveFilter{ProjectID: rec.ProjectID, SessionID: &rec.ID, PRURL: n.Subject.PRURL}, now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if events := notificationEvents(ctx, t, s); len(events) != 3 || events[2].Type != cdc.EventNotificationUpdated {
		t.Fatalf("after resolve events = %+v", events)
	}
}

func TestNotificationCDCPayloadEmbedsRealJSON(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	rec := seedNotificationSession(ctx, t, s)
	now := time.Now().UTC().Truncate(time.Second)
	if _, _, err := s.UpsertNotification(ctx, sampleNotification("n1", rec.ProjectID, rec.ID, "ci:1", "fp1", now)); err != nil {
		t.Fatal(err)
	}
	events := notificationEvents(ctx, t, s)
	if len(events) != 1 {
		t.Fatalf("events = %+v", events)
	}
	var payload map[string]any
	if err := json.Unmarshal(events[0].Payload, &payload); err != nil {
		t.Fatal(err)
	}
	if payload["actionCount"] != float64(1) {
		t.Fatalf("payload action count = %#v", payload["actionCount"])
	}
	if _, ok := payload["actions"].([]any); !ok {
		t.Fatalf("actions should be a JSON array, got %#v", payload["actions"])
	}
	if _, ok := payload["subject"].(map[string]any); !ok {
		t.Fatalf("subject should be a JSON object, got %#v", payload["subject"])
	}
}
