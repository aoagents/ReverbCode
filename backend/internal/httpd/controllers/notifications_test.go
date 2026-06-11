package controllers_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/controllers"
	notificationsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/notification"
)

type fakeNotificationService struct {
	gotFilter notificationsvc.ListFilter
	items     []notificationsvc.Notification
	err       error
}

func (f *fakeNotificationService) ListUnread(_ context.Context, filter notificationsvc.ListFilter) ([]notificationsvc.Notification, error) {
	f.gotFilter = filter
	return f.items, f.err
}

func newNotificationTestServer(t *testing.T, svc controllers.NotificationService) *httptest.Server {
	t.Helper()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := httptest.NewServer(httpd.NewRouterWithControl(config.Config{}, log, nil, httpd.APIDeps{Notifications: svc}, httpd.ControlDeps{}))
	t.Cleanup(srv.Close)
	return srv
}

func TestNotificationsAPI_ListUnread(t *testing.T) {
	now := time.Date(2026, 6, 11, 10, 0, 0, 0, time.UTC)
	svc := &fakeNotificationService{items: []notificationsvc.Notification{{
		NotificationRecord: domain.NotificationRecord{ID: "ntf_1", SessionID: "mer-1", ProjectID: "mer", Type: domain.NotificationNeedsInput, Title: "checkout-flow needs input", Body: "The agent is waiting for your response.", Status: domain.NotificationUnread, CreatedAt: now},
		Target:             notificationsvc.Target{Kind: notificationsvc.TargetSession, SessionID: "mer-1"},
	}}}
	srv := newNotificationTestServer(t, svc)

	body, status, _ := doRequest(t, srv, "GET", "/api/v1/notifications?projectId=mer&limit=10", "")
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", status, body)
	}
	if svc.gotFilter.ProjectID != "mer" || svc.gotFilter.Limit != 10 {
		t.Fatalf("filter = %+v", svc.gotFilter)
	}
	var resp struct {
		Notifications []struct {
			ID        string `json:"id"`
			SessionID string `json:"sessionId"`
			ProjectID string `json:"projectId"`
			Type      string `json:"type"`
			Status    string `json:"status"`
			Target    struct {
				Kind      string `json:"kind"`
				SessionID string `json:"sessionId"`
			} `json:"target"`
		} `json:"notifications"`
	}
	mustJSON(t, body, &resp)
	if len(resp.Notifications) != 1 || resp.Notifications[0].ID != "ntf_1" || resp.Notifications[0].Target.Kind != "session" {
		t.Fatalf("resp = %+v", resp)
	}
}

func TestNotificationsAPI_DefaultsAndCapsLimit(t *testing.T) {
	svc := &fakeNotificationService{}
	srv := newNotificationTestServer(t, svc)

	_, status, _ := doRequest(t, srv, "GET", "/api/v1/notifications?limit=999", "")
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200", status)
	}
	if svc.gotFilter.Limit != notificationsvc.MaxListLimit {
		t.Fatalf("limit = %d, want cap %d", svc.gotFilter.Limit, notificationsvc.MaxListLimit)
	}
}

func TestNotificationsAPI_RejectsUnsupportedStatus(t *testing.T) {
	srv := newNotificationTestServer(t, &fakeNotificationService{})

	body, status, _ := doRequest(t, srv, "GET", "/api/v1/notifications?status=read", "")
	assertErrorCode(t, body, status, http.StatusBadRequest, "INVALID_QUERY")
}

func TestNotificationsAPI_WithoutServiceIs501(t *testing.T) {
	srv := newNotificationTestServer(t, nil)

	body, status, _ := doRequest(t, srv, "GET", "/api/v1/notifications", "")
	assertErrorCode(t, body, status, http.StatusNotImplemented, "NOT_IMPLEMENTED")
}
