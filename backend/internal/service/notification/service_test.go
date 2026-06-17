package notification

import (
	"context"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

type fakeStore struct {
	rows []domain.NotificationRecord
	err  error
}

func (f *fakeStore) CreateNotification(context.Context, domain.NotificationRecord) (domain.NotificationRecord, bool, error) {
	return domain.NotificationRecord{}, false, nil
}

func (f *fakeStore) ListUnreadNotifications(_ context.Context, _ int) ([]domain.NotificationRecord, error) {
	return f.rows, f.err
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

func TestListUnreadRequiresStore(t *testing.T) {
	_, err := New(Deps{}).ListUnread(context.Background(), ListFilter{})
	if err == nil {
		t.Fatal("want missing store error")
	}
}
