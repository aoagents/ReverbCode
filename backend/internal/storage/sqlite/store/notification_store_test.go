package store_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/gen"
)

func TestNotificationInsertListGetAndDedupe(t *testing.T) {
	s, rec := newNotificationTestSession(t)
	ctx := context.Background()

	row, created, err := s.EnqueueNotification(ctx, sampleNotification(rec, "dedupe-1"))
	if err != nil {
		t.Fatal(err)
	}
	if !created || row.ID == "" || row.Seq == 0 {
		t.Fatalf("enqueue created=%v row=%+v", created, row)
	}
	got, ok, err := s.GetNotification(ctx, string(row.ID))
	if err != nil || !ok {
		t.Fatalf("get ok=%v err=%v", ok, err)
	}
	if got.DedupeKey != "dedupe-1" || got.Actions[0].ID != "open-session" {
		t.Fatalf("get mismatch: %+v", got)
	}
	dup, created, err := s.EnqueueNotification(ctx, sampleNotification(rec, "dedupe-1"))
	if err != nil {
		t.Fatal(err)
	}
	if created || dup.ID != row.ID || dup.Seq != row.Seq {
		t.Fatalf("duplicate should return existing row created=false: created=%v dup=%+v first=%+v", created, dup, row)
	}
	all, err := s.ListNotifications(ctx, sqlite.NotificationFilter{Limit: 10})
	if err != nil || len(all) != 1 {
		t.Fatalf("list all len=%d err=%v", len(all), err)
	}
	byProject, _ := s.ListNotifications(ctx, sqlite.NotificationFilter{ProjectID: string(rec.ProjectID), Limit: 10})
	bySession, _ := s.ListNotifications(ctx, sqlite.NotificationFilter{SessionID: string(rec.ID), Limit: 10})
	if len(byProject) != 1 || len(bySession) != 1 {
		t.Fatalf("project/session lists = %d/%d", len(byProject), len(bySession))
	}
}

func TestNotificationReadUnreadArchiveAndIdempotentCDC(t *testing.T) {
	s, rec := newNotificationTestSession(t)
	ctx := context.Background()
	row, _, err := s.EnqueueNotification(ctx, sampleNotification(rec, "dedupe-read"))
	if err != nil {
		t.Fatal(err)
	}
	createdSeq, _ := s.MaxChangeLogSeq(ctx)

	readAt := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	read, changed, err := s.MarkNotificationRead(ctx, string(row.ID), readAt)
	if err != nil || !changed {
		t.Fatalf("mark read changed=%v err=%v", changed, err)
	}
	if read.ReadAt.IsZero() {
		t.Fatal("read_at not set")
	}
	afterRead, _ := s.MaxChangeLogSeq(ctx)
	if afterRead != createdSeq+1 {
		t.Fatalf("read should emit one CDC event: before=%d after=%d", createdSeq, afterRead)
	}
	_, changed, err = s.MarkNotificationRead(ctx, string(row.ID), readAt.Add(time.Second))
	if err != nil || changed {
		t.Fatalf("repeated mark read should be idempotent changed=false, got changed=%v err=%v", changed, err)
	}
	afterRepeat, _ := s.MaxChangeLogSeq(ctx)
	if afterRepeat != afterRead {
		t.Fatalf("repeated read emitted CDC: before=%d after=%d", afterRead, afterRepeat)
	}

	unread, changed, err := s.MarkNotificationUnread(ctx, string(row.ID))
	if err != nil || !changed || !unread.ReadAt.IsZero() {
		t.Fatalf("mark unread changed=%v err=%v row=%+v", changed, err, unread)
	}
	unreadList, err := s.ListNotifications(ctx, sqlite.NotificationFilter{UnreadOnly: true, Limit: 10})
	if err != nil || len(unreadList) != 1 {
		t.Fatalf("unread list len=%d err=%v", len(unreadList), err)
	}

	archiveSeq, _ := s.MaxChangeLogSeq(ctx)
	archived, changed, err := s.ArchiveNotification(ctx, string(row.ID), readAt.Add(2*time.Second))
	if err != nil || !changed || archived.ArchivedAt.IsZero() {
		t.Fatalf("archive changed=%v err=%v row=%+v", changed, err, archived)
	}
	afterArchive, _ := s.MaxChangeLogSeq(ctx)
	if afterArchive != archiveSeq+1 {
		t.Fatalf("archive should emit one CDC event: before=%d after=%d", archiveSeq, afterArchive)
	}
	_, changed, err = s.ArchiveNotification(ctx, string(row.ID), readAt.Add(3*time.Second))
	if err != nil || changed {
		t.Fatalf("repeated archive should be idempotent changed=false, got changed=%v err=%v", changed, err)
	}
	afterArchiveRepeat, _ := s.MaxChangeLogSeq(ctx)
	if afterArchiveRepeat != afterArchive {
		t.Fatalf("repeated archive emitted CDC: before=%d after=%d", afterArchive, afterArchiveRepeat)
	}
}

func TestNotificationJSONConstraintsRejectInvalidPayloadAndActions(t *testing.T) {
	dir := t.TempDir()
	s, err := sqlite.Open(dir)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	seedProject(t, s, "ao")
	rec, err := s.CreateSession(context.Background(), sampleRecord("ao"))
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	ctx := context.Background()

	badPayload := sampleNotification(rec, "bad-payload")
	badPayload.Payload = json.RawMessage(`{"nope"`)
	if _, _, err := s.EnqueueNotification(ctx, badPayload); err == nil {
		t.Fatal("invalid payload JSON should be rejected")
	}

	rawDB, err := sql.Open("sqlite", "file:"+filepath.Join(dir, "ao.db"))
	if err != nil {
		t.Fatalf("open raw db: %v", err)
	}
	t.Cleanup(func() { _ = rawDB.Close() })

	now := time.Now().UTC().Truncate(time.Second)
	_, err = gen.New(rawDB).InsertNotification(ctx, gen.InsertNotificationParams{
		ProjectID:    rec.ProjectID,
		SessionID:    rec.ID,
		Source:       "lifecycle",
		EventType:    "reaction.agent-needs-input",
		SemanticType: "session.needs_input",
		Priority:     "urgent",
		Message:      "bad actions",
		PayloadJson:  `{}`,
		ActionsJson:  `{not-json`,
		DedupeKey:    "bad-actions",
		CauseKey:     "agent-needs-input",
		CreatedAt:    now,
		UpdatedAt:    now,
	})
	if err == nil {
		t.Fatal("invalid actions JSON should be rejected")
	}
}

func TestNotificationCDCForCreateReadArchive(t *testing.T) {
	s, rec := newNotificationTestSession(t)
	ctx := context.Background()
	startSeq, _ := s.MaxChangeLogSeq(ctx)
	row, _, err := s.EnqueueNotification(ctx, sampleNotification(rec, "dedupe-cdc"))
	if err != nil {
		t.Fatal(err)
	}
	_, _, _ = s.MarkNotificationRead(ctx, string(row.ID), time.Now().UTC())
	_, _, _ = s.ArchiveNotification(ctx, string(row.ID), time.Now().UTC())

	evs, err := s.ReadChangeLogAfter(ctx, startSeq, 10)
	if err != nil {
		t.Fatal(err)
	}
	var types []string
	for _, e := range evs {
		types = append(types, e.EventType)
		if e.EventType == "notification_created" && !strings.Contains(e.Payload, `"data"`) {
			t.Fatalf("notification_created payload missing data: %s", e.Payload)
		}
	}
	want := []string{"notification_created", "notification_updated", "notification_updated"}
	if fmt.Sprint(types) != fmt.Sprint(want) {
		t.Fatalf("notification CDC types = %v, want %v", types, want)
	}
}

func TestConcurrentNotificationEnqueueSameDedupeCreatesOneRow(t *testing.T) {
	s, rec := newNotificationTestSession(t)
	ctx := context.Background()
	const n = 20
	var wg sync.WaitGroup
	ids := make(chan domain.NotificationID, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			row, _, err := s.EnqueueNotification(ctx, sampleNotification(rec, "dedupe-concurrent"))
			if err != nil {
				t.Errorf("enqueue: %v", err)
				return
			}
			ids <- row.ID
		}()
	}
	wg.Wait()
	close(ids)
	var first domain.NotificationID
	for id := range ids {
		if first == "" {
			first = id
		}
		if id != first {
			t.Fatalf("all callers should see same id, got %q and %q", first, id)
		}
	}
	rows, err := s.ListNotifications(ctx, sqlite.NotificationFilter{Limit: 10})
	if err != nil || len(rows) != 1 {
		t.Fatalf("list len=%d err=%v", len(rows), err)
	}
}

func newNotificationTestSession(t *testing.T) (*sqlite.Store, domain.SessionRecord) {
	t.Helper()
	s := newTestStore(t)
	seedProject(t, s, "ao")
	rec, err := s.CreateSession(context.Background(), sampleRecord("ao"))
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	return s, rec
}

func sampleNotification(rec domain.SessionRecord, dedupe string) sqlite.NotificationRow {
	now := time.Now().UTC().Truncate(time.Second)
	return sqlite.NotificationRow{
		ProjectID:    rec.ProjectID,
		SessionID:    rec.ID,
		Source:       "lifecycle",
		EventType:    "reaction.agent-needs-input",
		SemanticType: "session.needs_input",
		Priority:     "urgent",
		Message:      "Agent needs input to continue.",
		Payload:      json.RawMessage(`{"schemaVersion":3,"semanticType":"session.needs_input"}`),
		Actions:      []domain.NotificationAction{{ID: "open-session", Kind: "route", Label: "Open session", Route: "/projects/ao/sessions/ao-1"}},
		DedupeKey:    dedupe,
		CauseKey:     "agent-needs-input",
		CreatedAt:    now,
		UpdatedAt:    now,
	}
}
