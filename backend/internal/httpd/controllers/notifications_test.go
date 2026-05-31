package controllers_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
)

func TestNotificationsAPIListGetFiltersPaginationAndUnreadCount(t *testing.T) {
	srv, store := newNotificationAPIServer(t)
	ctx := context.Background()
	ao1 := seedNotificationSession(t, store, "ao")
	ao2 := seedNotificationSession(t, store, "ao")
	mer := seedNotificationSession(t, store, "mer")

	n1 := enqueueAPINotification(t, store, ao1, "n1", "urgent")
	n2 := enqueueAPINotification(t, store, ao2, "n2", "info")
	n3 := enqueueAPINotification(t, store, mer, "n3", "warning")
	if _, _, err := store.MarkNotificationRead(ctx, string(n2.ID), time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.ArchiveNotification(ctx, string(n3.ID), time.Now().UTC()); err != nil {
		t.Fatal(err)
	}

	body, status, headers := doRequest(t, srv, "GET", "/api/v1/notifications?limit=1", "")
	if status != http.StatusOK {
		t.Fatalf("list status=%d body=%s", status, body)
	}
	assertJSON(t, headers)
	var list notificationListResponse
	mustJSON(t, body, &list)
	if list.Limit != 1 || len(list.Notifications) != 1 || list.UnreadCount != 1 || list.NextBeforeSeq == nil {
		t.Fatalf("list response = %#v", list)
	}

	body, status, _ = doRequest(t, srv, "GET", "/api/v1/notifications?unreadOnly=true", "")
	if status != http.StatusOK {
		t.Fatalf("unread status=%d body=%s", status, body)
	}
	mustJSON(t, body, &list)
	if len(list.Notifications) != 1 || list.Notifications[0].ID != string(n1.ID) {
		t.Fatalf("unread = %#v, want only %s", list.Notifications, n1.ID)
	}

	body, status, _ = doRequest(t, srv, "GET", "/api/v1/notifications?projectId=ao&beforeSeq="+jsonNumber(n2.Seq), "")
	if status != http.StatusOK {
		t.Fatalf("before/project status=%d body=%s", status, body)
	}
	mustJSON(t, body, &list)
	if len(list.Notifications) != 1 || list.Notifications[0].ID != string(n1.ID) {
		t.Fatalf("before/project = %#v", list.Notifications)
	}

	body, status, _ = doRequest(t, srv, "GET", "/api/v1/notifications?includeArchived=true", "")
	if status != http.StatusOK {
		t.Fatalf("includeArchived status=%d body=%s", status, body)
	}
	mustJSON(t, body, &list)
	if len(list.Notifications) != 3 {
		t.Fatalf("includeArchived len=%d want 3; n3=%s", len(list.Notifications), n3.ID)
	}

	body, status, _ = doRequest(t, srv, "GET", "/api/v1/notifications/"+string(n1.ID), "")
	if status != http.StatusOK {
		t.Fatalf("get status=%d body=%s", status, body)
	}
	var got struct {
		Notification notificationRecordBody `json:"notification"`
	}
	mustJSON(t, body, &got)
	if got.Notification.Event.Type != "session.needs_input" || got.Notification.Actions[0].Kind != "open-session" {
		t.Fatalf("notification record = %#v", got.Notification)
	}
}

func TestNotificationsAPIPatchReadArchiveAndReadAll(t *testing.T) {
	srv, store := newNotificationAPIServer(t)
	rec := seedNotificationSession(t, store, "ao")
	n1 := enqueueAPINotification(t, store, rec, "patch-1", "urgent")
	n2 := enqueueAPINotification(t, store, rec, "patch-2", "urgent")

	body, status, _ := doRequest(t, srv, "PATCH", "/api/v1/notifications/"+string(n1.ID), `{"read":true,"archived":false}`)
	if status != http.StatusOK {
		t.Fatalf("patch read status=%d body=%s", status, body)
	}
	var patched struct {
		Notification notificationRecordBody `json:"notification"`
	}
	mustJSON(t, body, &patched)
	if patched.Notification.ReadAt == nil || patched.Notification.ArchivedAt != nil {
		t.Fatalf("patched = %#v", patched.Notification)
	}

	body, status, _ = doRequest(t, srv, "PATCH", "/api/v1/notifications/"+string(n1.ID), `{"read":false}`)
	if status != http.StatusOK {
		t.Fatalf("patch unread status=%d body=%s", status, body)
	}
	mustJSON(t, body, &patched)
	if patched.Notification.ReadAt != nil {
		t.Fatalf("unread patch = %#v", patched.Notification)
	}

	body, status, _ = doRequest(t, srv, "PATCH", "/api/v1/notifications/"+string(n1.ID), `{"archived":true}`)
	if status != http.StatusOK {
		t.Fatalf("archive status=%d body=%s", status, body)
	}
	mustJSON(t, body, &patched)
	if patched.Notification.ArchivedAt == nil {
		t.Fatalf("archive patch = %#v", patched.Notification)
	}

	body, status, _ = doRequest(t, srv, "POST", "/api/v1/notifications/read-all", `{"projectId":"ao","sessionId":"`+string(rec.ID)+`"}`)
	if status != http.StatusOK {
		t.Fatalf("read-all status=%d body=%s", status, body)
	}
	var readAll struct {
		Updated int `json:"updated"`
	}
	mustJSON(t, body, &readAll)
	if readAll.Updated != 1 {
		t.Fatalf("read-all updated=%d want 1 (n2=%s)", readAll.Updated, n2.ID)
	}
}

func TestNotificationsAPIActionTokenAllowlistAndTargets(t *testing.T) {
	srv, store := newNotificationAPIServer(t)
	rec := seedNotificationSession(t, store, "ao")
	row := enqueueAPINotification(t, store, rec, "action-1", "urgent")

	body, status, _ := doRequest(t, srv, "POST", "/api/v1/notifications/"+string(row.ID)+"/actions/open-session", `{}`)
	assertErrorCode(t, body, status, http.StatusForbidden, "ACTION_NOT_ALLOWED")

	body, status, _ = doActionRequest(t, srv, string(row.ID), "open-session", "test-token")
	if status != http.StatusOK {
		t.Fatalf("action status=%d body=%s", status, body)
	}
	var result struct {
		OK     bool `json:"ok"`
		Result struct {
			Route string `json:"route"`
		} `json:"result"`
	}
	mustJSON(t, body, &result)
	if !result.OK || result.Result.Route != "/projects/ao/sessions/"+string(rec.ID) {
		t.Fatalf("action result = %#v", result)
	}

	body, status, _ = doActionRequest(t, srv, string(row.ID), "merge-pr", "test-token")
	assertErrorCode(t, body, status, http.StatusConflict, "ACTION_PRECONDITION_FAILED")
}

func newNotificationAPIServer(t *testing.T) (*httptest.Server, *sqlite.Store) {
	t.Helper()
	store, err := sqlite.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := httptest.NewServer(httpd.NewRouterWithAPI(config.Config{}, log, nil, httpd.APIDeps{
		Notifications: store,
		ActionToken:   "test-token",
	}))
	t.Cleanup(srv.Close)
	return srv, store
}

func seedNotificationSession(t *testing.T, store *sqlite.Store, project string) domain.SessionRecord {
	t.Helper()
	ctx := context.Background()
	if _, ok, err := store.GetProject(ctx, project); err != nil {
		t.Fatal(err)
	} else if !ok {
		if err := store.UpsertProject(ctx, sqlite.ProjectRow{ID: project, Path: "/tmp/" + project, RegisteredAt: time.Now().UTC()}); err != nil {
			t.Fatal(err)
		}
	}
	rec, err := store.CreateSession(ctx, domain.SessionRecord{
		ProjectID: domain.ProjectID(project),
		Kind:      domain.KindWorker,
		Lifecycle: domain.CanonicalSessionLifecycle{
			Version: domain.LifecycleVersion,
			IsAlive: true,
			Session: domain.SessionSubstate{State: domain.SessionWorking},
			Activity: domain.ActivitySubstate{
				State: domain.ActivityActive, LastActivityAt: time.Now().UTC(), Source: domain.SourceNative,
			},
		},
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatal(err)
	}
	return rec
}

func enqueueAPINotification(t *testing.T, store *sqlite.Store, rec domain.SessionRecord, dedupe string, priority string) domain.Notification {
	t.Helper()
	now := time.Now().UTC().Truncate(time.Second)
	row, _, err := store.EnqueueNotification(context.Background(), domain.Notification{
		ProjectID:    rec.ProjectID,
		SessionID:    rec.ID,
		Source:       "lifecycle",
		EventType:    "reaction.agent-needs-input",
		SemanticType: "session.needs_input",
		Priority:     priority,
		Message:      "Agent needs input to continue.",
		Payload:      json.RawMessage(`{"schemaVersion":3,"semanticType":"session.needs_input","subject":{"session":{"id":"` + string(rec.ID) + `","projectId":"` + string(rec.ProjectID) + `"}}}`),
		Actions: []domain.NotificationAction{
			{ID: "open-session", Kind: "open-session", Label: "Open session", Route: "/projects/" + string(rec.ProjectID) + "/sessions/" + string(rec.ID)},
			{ID: "merge-pr", Kind: "merge-pr", Label: "Merge PR"},
		},
		DedupeKey: dedupe,
		CauseKey:  "agent-needs-input",
		CreatedAt: now,
		UpdatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	return row
}

func doActionRequest(t *testing.T, srv *httptest.Server, notificationID string, actionID string, token string) ([]byte, int, http.Header) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/api/v1/notifications/"+notificationID+"/actions/"+actionID, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("X-AO-Action-Token", token)
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return body, resp.StatusCode, resp.Header
}

type notificationListResponse struct {
	Notifications []notificationRecordBody `json:"notifications"`
	UnreadCount   int                      `json:"unreadCount"`
	Limit         int                      `json:"limit"`
	NextBeforeSeq *int64                   `json:"nextBeforeSeq"`
}

type notificationRecordBody struct {
	Seq        int64        `json:"seq"`
	ID         string       `json:"id"`
	ReadAt     any          `json:"readAt"`
	ArchivedAt any          `json:"archivedAt"`
	Event      eventBody    `json:"event"`
	Actions    []actionBody `json:"actions"`
}

type eventBody struct {
	Type string `json:"type"`
}

type actionBody struct {
	ID    string `json:"id"`
	Kind  string `json:"kind"`
	Route string `json:"route"`
	URL   string `json:"url"`
}

func jsonNumber(n int64) string {
	b, _ := json.Marshal(n)
	return string(b)
}
