package notification

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

type fakeStore struct {
	rows      []domain.NotificationRecord
	duplicate bool
	err       error
}

func (f *fakeStore) CreateNotification(_ context.Context, rec domain.NotificationRecord) (domain.NotificationRecord, bool, error) {
	if f.err != nil {
		return domain.NotificationRecord{}, false, f.err
	}
	if f.duplicate {
		return domain.NotificationRecord{}, false, nil
	}
	f.rows = append(f.rows, rec)
	return rec, true, nil
}

func (f *fakeStore) ListUnreadNotifications(_ context.Context, _ int) ([]domain.NotificationRecord, error) {
	return f.rows, nil
}

func TestManagerNotifyPersistsNotification(t *testing.T) {
	st := &fakeStore{}
	now := time.Date(2026, 6, 11, 10, 0, 0, 0, time.UTC)
	mgr := New(Deps{Store: st, Clock: func() time.Time { return now }, NewID: func() string { return "ntf_1" }})

	if err := mgr.Notify(context.Background(), Intent{
		Type:               domain.NotificationNeedsInput,
		SessionID:          "mer-1",
		ProjectID:          "mer",
		SessionDisplayName: "checkout-flow",
	}); err != nil {
		t.Fatalf("Notify: %v", err)
	}
	if len(st.rows) != 1 {
		t.Fatalf("stored rows = %d, want 1", len(st.rows))
	}
	if got := st.rows[0]; got.ID != "ntf_1" || got.CreatedAt != now || got.Status != domain.NotificationUnread || got.Title != "checkout-flow needs input" {
		t.Fatalf("stored notification = %+v", got)
	}
}

func TestManagerNotifyDuplicateIsIgnored(t *testing.T) {
	st := &fakeStore{duplicate: true}
	mgr := New(Deps{Store: st, Clock: func() time.Time { return time.Now() }, NewID: func() string { return "ntf_1" }})

	err := mgr.Notify(context.Background(), Intent{Type: domain.NotificationNeedsInput, SessionID: "mer-1", ProjectID: "mer", CreatedAt: time.Now()})
	if err != nil {
		t.Fatalf("Notify duplicate: %v", err)
	}
	if len(st.rows) != 0 {
		t.Fatalf("duplicate should not persist, got %+v", st.rows)
	}
}

func TestManagerNotifyRejectsUnknownType(t *testing.T) {
	mgr := New(Deps{Store: &fakeStore{}, Clock: func() time.Time { return time.Now() }})
	err := mgr.Notify(context.Background(), Intent{Type: "surprise", SessionID: "mer-1", ProjectID: "mer"})
	if !errors.Is(err, domain.ErrInvalidNotificationType) {
		t.Fatalf("err = %v, want invalid type", err)
	}
}

func TestListUnreadAddsTargets(t *testing.T) {
	st := &fakeStore{rows: []domain.NotificationRecord{
		{ID: "n1", SessionID: "mer-1", ProjectID: "mer", Type: domain.NotificationNeedsInput, Title: "needs", Status: domain.NotificationUnread, CreatedAt: time.Now()},
		{ID: "n2", SessionID: "mer-1", ProjectID: "mer", PRURL: "https://github.com/o/r/pull/1", Type: domain.NotificationReadyToMerge, Title: "ready", Status: domain.NotificationUnread, CreatedAt: time.Now()},
	}}
	mgr := New(Deps{Store: st})
	got, err := mgr.ListUnread(context.Background(), ListFilter{Limit: 10})
	if err != nil {
		t.Fatalf("ListUnread: %v", err)
	}
	if got[0].Target.Kind != TargetSession || got[1].Target.Kind != TargetPR || got[1].Target.PRURL == "" {
		t.Fatalf("targets = %+v", got)
	}
}
