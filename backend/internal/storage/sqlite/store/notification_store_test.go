package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func seedNotificationSession(ctx context.Context, t *testing.T, s interface {
	UpsertProject(context.Context, domain.ProjectRecord) error
	CreateSession(context.Context, domain.SessionRecord) (domain.SessionRecord, error)
}) domain.SessionRecord {
	t.Helper()
	if err := s.UpsertProject(ctx, domain.ProjectRecord{ID: "mer", Path: "/repo/mer", RegisteredAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	rec, err := s.CreateSession(ctx, sampleRecord("mer"))
	if err != nil {
		t.Fatal(err)
	}
	return rec
}

func sampleNotification(id domain.NotificationID, project domain.ProjectID, session domain.SessionID, dedupe, fp string, at time.Time) domain.Notification {
	return domain.Notification{
		ID: id, Type: domain.NotificationCIFailing, Priority: domain.NotificationWarning, Status: domain.NotificationUnread,
		ProjectID: project, SessionID: session, Source: "test", DedupeKey: dedupe, Fingerprint: fp,
		Title: "CI failed", Summary: "mer-1 has 1 failing check.", Body: "body",
		Subject:    domain.NotificationSubject{Kind: "pull_request", ProjectID: project, SessionID: session, PRURL: "https://github.com/o/r/pull/1", Label: "PR"},
		Data:       map[string]any{"pr": map[string]any{"url": "https://github.com/o/r/pull/1"}, "ci": map[string]any{"failedCount": 1}},
		Actions:    []domain.NotificationAction{{ID: "open_session", Label: "Open session", Kind: "route", Route: "session", Primary: true}},
		OccurredAt: at, CreatedAt: at, UpdatedAt: at,
	}.Normalize()
}

func TestNotificationStoreInsertReadListAndJSONRoundTrip(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	rec := seedNotificationSession(ctx, t, s)
	now := time.Now().UTC().Truncate(time.Second)
	n := sampleNotification("n1", rec.ProjectID, rec.ID, "ci:1", "fp1", now)
	stored, changed, err := s.UpsertNotification(ctx, n)
	if err != nil || !changed {
		t.Fatalf("upsert changed=%v err=%v", changed, err)
	}
	got, ok, err := s.GetNotification(ctx, stored.ID)
	if err != nil || !ok {
		t.Fatalf("get ok=%v err=%v", ok, err)
	}
	if got.Subject.PRURL != n.Subject.PRURL || got.Actions[0].ID != "open_session" || got.Data["pr"] == nil {
		t.Fatalf("json round trip mismatch: %+v", got)
	}
	second := sampleNotification("n2", rec.ProjectID, rec.ID, "ci:2", "fp2", now.Add(time.Minute))
	if _, _, err := s.UpsertNotification(ctx, second); err != nil {
		t.Fatal(err)
	}
	byProject, err := s.ListNotificationsByProject(ctx, rec.ProjectID, 10)
	if err != nil || len(byProject) != 2 || byProject[0].ID != "n2" {
		t.Fatalf("list by project = %+v err=%v", byProject, err)
	}
	bySession, err := s.ListNotificationsBySession(ctx, rec.ID, 10)
	if err != nil || len(bySession) != 2 || bySession[0].ID != "n2" {
		t.Fatalf("list by session = %+v err=%v", bySession, err)
	}
}

func TestNotificationStoreDedupeAndUpdateSameRow(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	rec := seedNotificationSession(ctx, t, s)
	now := time.Now().UTC().Truncate(time.Second)
	n := sampleNotification("n1", rec.ProjectID, rec.ID, "ci:1", "fp1", now)
	first, changed, err := s.UpsertNotification(ctx, n)
	if err != nil || !changed {
		t.Fatalf("first changed=%v err=%v", changed, err)
	}
	same := n
	same.ID = "other"
	again, changed, err := s.UpsertNotification(ctx, same)
	if err != nil || changed || again.ID != first.ID {
		t.Fatalf("same fingerprint got changed=%v id=%s err=%v", changed, again.ID, err)
	}
	updated := n
	updated.ID = "other2"
	updated.Fingerprint = "fp2"
	updated.Summary = "mer-1 has 2 failing checks."
	updated.UpdatedAt = now.Add(time.Minute)
	got, changed, err := s.UpsertNotification(ctx, updated)
	if err != nil || !changed || got.ID != first.ID || got.Summary != updated.Summary {
		t.Fatalf("changed fingerprint got changed=%v notification=%+v err=%v", changed, got, err)
	}
}

func TestNotificationStoreResolve(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	rec := seedNotificationSession(ctx, t, s)
	now := time.Now().UTC().Truncate(time.Second)
	n := sampleNotification("n1", rec.ProjectID, rec.ID, "ci:1", "fp1", now)
	if _, _, err := s.UpsertNotification(ctx, n); err != nil {
		t.Fatal(err)
	}
	resolvedAt := now.Add(time.Hour)
	count, err := s.ResolveNotifications(ctx, domain.NotificationResolveFilter{ProjectID: rec.ProjectID, SessionID: &rec.ID, PRURL: n.Subject.PRURL, Types: []domain.NotificationType{domain.NotificationCIFailing}}, resolvedAt)
	if err != nil || count != 1 {
		t.Fatalf("resolve count=%d err=%v", count, err)
	}
	got, _, err := s.GetNotification(ctx, "n1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != domain.NotificationResolved || got.ResolvedAt == nil || !got.ResolvedAt.Equal(resolvedAt) {
		t.Fatalf("resolved notification = %+v", got)
	}
}

func TestNotificationStoreResolveEscapesDedupeKeyPrefixWildcards(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	rec := seedNotificationSession(ctx, t, s)
	now := time.Now().UTC().Truncate(time.Second)

	literalPercent := sampleNotification("n1", rec.ProjectID, rec.ID, "ci:%:build:c1", "fp1", now)
	if _, _, err := s.UpsertNotification(ctx, literalPercent); err != nil {
		t.Fatal(err)
	}
	plainCI := sampleNotification("n2", rec.ProjectID, rec.ID, "ci:abc:build:c1", "fp2", now)
	if _, _, err := s.UpsertNotification(ctx, plainCI); err != nil {
		t.Fatal(err)
	}

	resolvedAt := now.Add(time.Hour)
	count, err := s.ResolveNotifications(ctx, domain.NotificationResolveFilter{
		ProjectID:         rec.ProjectID,
		DedupeKeyPrefixes: []string{"ci:%:"},
	}, resolvedAt)
	if err != nil || count != 1 {
		t.Fatalf("resolve count=%d err=%v", count, err)
	}
	gotLiteral, _, err := s.GetNotification(ctx, "n1")
	if err != nil {
		t.Fatal(err)
	}
	gotPlain, _, err := s.GetNotification(ctx, "n2")
	if err != nil {
		t.Fatal(err)
	}
	if gotLiteral.Status != domain.NotificationResolved {
		t.Fatalf("literal-percent notification should resolve, got %+v", gotLiteral)
	}
	if gotPlain.Status != domain.NotificationUnread {
		t.Fatalf("plain CI notification should remain unread; prefix wildcard was not escaped: %+v", gotPlain)
	}
}
