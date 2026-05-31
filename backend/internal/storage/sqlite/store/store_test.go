package store_test

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
)

func newTestStore(t *testing.T) *sqlite.Store {
	t.Helper()
	s, err := sqlite.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func seedProject(t *testing.T, s *sqlite.Store, id string) {
	t.Helper()
	if err := s.UpsertProject(context.Background(), sqlite.ProjectRow{
		ID: domain.ProjectID(id), Path: "/tmp/" + id, RegisteredAt: time.Now().UTC().Truncate(time.Second),
	}); err != nil {
		t.Fatalf("seed project %s: %v", id, err)
	}
}

func sampleRecord(project string) domain.SessionRecord {
	now := time.Now().UTC().Truncate(time.Second)
	return domain.SessionRecord{
		ProjectID: domain.ProjectID(project),
		Kind:      domain.KindWorker,
		Harness:   domain.HarnessClaudeCode,
		Activity:  domain.ActivitySubstate{State: domain.ActivityActive, LastActivityAt: now, Source: domain.SourceNative},
		Metadata:  domain.SessionMetadata{Branch: "feat/x", WorkspacePath: "/ws"},
		CreatedAt: now,
		UpdatedAt: now,
	}
}

func TestProjectCRUDAndArchive(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "mer")

	got, ok, err := s.GetProject(ctx, "mer")
	if err != nil || !ok {
		t.Fatalf("get: ok=%v err=%v", ok, err)
	}
	if got.ID != "mer" || got.Path != "/tmp/mer" {
		t.Fatalf("project = %+v", got)
	}
	if list, _ := s.ListProjects(ctx); len(list) != 1 {
		t.Fatalf("active list = %d, want 1", len(list))
	}
	// archive hides from the active list but still resolves by id.
	if err := s.ArchiveProject(ctx, "mer", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if list, _ := s.ListProjects(ctx); len(list) != 0 {
		t.Fatalf("after archive, active list = %d, want 0", len(list))
	}
	if _, ok, _ := s.GetProject(ctx, "mer"); !ok {
		t.Fatal("archived project must still resolve by id")
	}
}

func TestSessionCreateAssignsPerProjectID(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "mer")
	seedProject(t, s, "ao")

	r1, err := s.CreateSession(ctx, sampleRecord("mer"))
	if err != nil {
		t.Fatal(err)
	}
	r2, _ := s.CreateSession(ctx, sampleRecord("mer"))
	r3, _ := s.CreateSession(ctx, sampleRecord("ao"))
	if r1.ID != "mer-1" || r2.ID != "mer-2" || r3.ID != "ao-1" {
		t.Fatalf("ids = %s, %s, %s; want mer-1, mer-2, ao-1", r1.ID, r2.ID, r3.ID)
	}
	got, ok, err := s.GetSession(ctx, "mer-1")
	if err != nil || !ok {
		t.Fatalf("get: ok=%v err=%v", ok, err)
	}
	if got.Activity.State != domain.ActivityActive || got.IsTerminated ||
		got.Harness != domain.HarnessClaudeCode || got.Metadata.Branch != "feat/x" {
		t.Fatalf("round-trip mismatch: %+v", got)
	}
	if list, _ := s.ListSessions(ctx, "mer"); len(list) != 2 {
		t.Fatalf("list mer = %d, want 2", len(list))
	}
	if all, _ := s.ListAllSessions(ctx); len(all) != 3 {
		t.Fatalf("list all = %d, want 3", len(all))
	}
}

func TestSessionUpdateActivityAndTermination(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "mer")
	r, _ := s.CreateSession(ctx, sampleRecord("mer"))

	r.Activity = domain.ActivitySubstate{State: domain.ActivityWaitingInput, LastActivityAt: r.CreatedAt, Source: domain.SourceHook}
	r.IsTerminated = true
	if err := s.UpdateSession(ctx, r); err != nil {
		t.Fatal(err)
	}
	got, _, _ := s.GetSession(ctx, r.ID)
	if got.Activity.State != domain.ActivityWaitingInput || !got.IsTerminated {
		t.Fatalf("update not persisted: %+v", got)
	}

	got.IsTerminated = false
	got.Activity.State = domain.ActivityActive
	_ = s.UpdateSession(ctx, got)
	again, _, _ := s.GetSession(ctx, r.ID)
	if again.IsTerminated || again.Activity.State != domain.ActivityActive {
		t.Fatalf("activity/termination should update, got %+v", again)
	}
}

func TestPRCRUD(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "mer")
	r, _ := s.CreateSession(ctx, sampleRecord("mer"))
	now := time.Now().UTC().Truncate(time.Second)

	pr := domain.PRRow{
		URL: "https://gh/pr/1", SessionID: r.ID, Number: 1,
		Review: domain.ReviewRequired, CI: domain.CIFailing, Mergeability: domain.MergeBlocked, UpdatedAt: now,
	}
	if err := s.UpsertPR(ctx, pr); err != nil {
		t.Fatal(err)
	}
	got, ok, err := s.GetPR(ctx, pr.URL)
	if err != nil || !ok || got != pr {
		t.Fatalf("get pr: ok=%v err=%v got=%+v", ok, err, got)
	}
	if list, _ := s.ListPRsBySession(ctx, string(r.ID)); len(list) != 1 {
		t.Fatalf("list prs = %d, want 1", len(list))
	}
	if err := s.DeletePR(ctx, pr.URL); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := s.GetPR(ctx, pr.URL); ok {
		t.Fatal("pr should be gone")
	}
}

func TestPRChecksLoopBrakeQuery(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "mer")
	r, _ := s.CreateSession(ctx, sampleRecord("mer"))
	now := time.Now().UTC().Truncate(time.Second)
	_ = s.UpsertPR(ctx, domain.PRRow{URL: "pr1", SessionID: r.ID, UpdatedAt: now})

	// three consecutive failing runs of "build" (one per commit).
	for i := 1; i <= 3; i++ {
		if err := s.RecordCheck(ctx, domain.PRCheckRow{
			PRURL: "pr1", Name: "build", CommitHash: fmt.Sprintf("c%d", i),
			Status: "failed", CreatedAt: now.Add(time.Duration(i) * time.Second),
		}); err != nil {
			t.Fatal(err)
		}
	}
	last3, err := s.RecentCheckStatuses(ctx, "pr1", "build", 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(last3) != 3 || last3[0] != "failed" || last3[1] != "failed" || last3[2] != "failed" {
		t.Fatalf("recent statuses = %v, want 3x failed (loop brake would trip)", last3)
	}
	// a pass on a newer commit breaks the streak.
	_ = s.RecordCheck(ctx, domain.PRCheckRow{PRURL: "pr1", Name: "build", CommitHash: "c4", Status: "passed", CreatedAt: now.Add(4 * time.Second)})
	last3, _ = s.RecentCheckStatuses(ctx, "pr1", "build", 3)
	if last3[0] != "passed" {
		t.Fatalf("most recent should be passed, got %v", last3)
	}
}

func TestPRCommentsReplace(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "mer")
	r, _ := s.CreateSession(ctx, sampleRecord("mer"))
	now := time.Now().UTC().Truncate(time.Second)
	_ = s.UpsertPR(ctx, domain.PRRow{URL: "pr1", SessionID: r.ID, UpdatedAt: now})

	_ = s.ReplacePRComments(ctx, "pr1", []domain.PRComment{
		{ID: "c1", Author: "a", File: "a.go", Line: 1, Body: "nit", CreatedAt: now},
		{ID: "c2", Author: "b", File: "b.go", Line: 2, Body: "bug", Resolved: true, CreatedAt: now.Add(time.Second)},
	})
	if list, _ := s.ListPRComments(ctx, "pr1"); len(list) != 2 {
		t.Fatalf("comments = %d, want 2", len(list))
	}
	// replace with a smaller set drops the rest.
	_ = s.ReplacePRComments(ctx, "pr1", []domain.PRComment{{ID: "c1", Body: "x", CreatedAt: now}})
	if list, _ := s.ListPRComments(ctx, "pr1"); len(list) != 1 {
		t.Fatalf("after replace, comments = %d, want 1", len(list))
	}
}

func TestCDCTriggersPopulateChangeLog(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "mer")

	r, _ := s.CreateSession(ctx, sampleRecord("mer"))
	// a real state change logs; a metadata-only change does not (WHEN guard).
	r.Activity.State = domain.ActivityIdle
	_ = s.UpdateSession(ctx, r)
	r.Metadata.Prompt = "only metadata changed"
	_ = s.UpdateSession(ctx, r)
	// a PR insert logs too.
	_ = s.UpsertPR(ctx, domain.PRRow{URL: "pr1", SessionID: r.ID, UpdatedAt: r.UpdatedAt})

	evs, err := s.ReadChangeLogAfter(ctx, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	var types []string
	for _, e := range evs {
		if e.ProjectID != "mer" {
			t.Fatalf("event project = %s, want mer", e.ProjectID)
		}
		types = append(types, e.EventType)
	}
	want := []string{"session_created", "session_updated", "pr_created"}
	if len(types) != 3 || types[0] != want[0] || types[1] != want[1] || types[2] != want[2] {
		t.Fatalf("change_log event types = %v, want %v (metadata-only update suppressed)", types, want)
	}
	maxSeq, _ := s.MaxChangeLogSeq(ctx)
	if maxSeq != int64(len(evs)) {
		t.Fatalf("max seq = %d, want %d", maxSeq, len(evs))
	}
}

func TestConcurrentSessionCreateAssignsUniqueNums(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "mer")

	const n = 20
	var wg sync.WaitGroup
	ids := make([]string, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			r, err := s.CreateSession(ctx, sampleRecord("mer"))
			if err != nil {
				t.Errorf("create: %v", err)
				return
			}
			ids[i] = string(r.ID)
		}(i)
	}
	wg.Wait()

	seen := map[string]bool{}
	for _, id := range ids {
		if id == "" || seen[id] {
			t.Fatalf("duplicate or empty id: %q in %v", id, ids)
		}
		seen[id] = true
	}
	if all, _ := s.ListAllSessions(ctx); len(all) != n {
		t.Fatalf("created %d sessions, want %d", len(all), n)
	}
}
