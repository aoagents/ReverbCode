package sqlite

import (
	"context"
	"strings"
	"testing"
	"time"
)

// A check can change status on the same commit (in_progress -> failed) via
// UpsertPRCheck's ON CONFLICT DO UPDATE. CDC must emit on that transition, not
// only on the first insert — otherwise live clients never see the status change.
func TestPRChecksCDC_EmitsOnInsertAndStatusUpdate(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "mer")
	rec, err := s.CreateSession(ctx, sampleRecord("mer"))
	if err != nil {
		t.Fatal(err)
	}
	url := "https://example/pr/1"
	if err := s.UpsertPR(ctx, PRRow{URL: url, SessionID: string(rec.ID), Number: 1}); err != nil {
		t.Fatal(err)
	}

	now := time.Now()
	mustCheck := func(status string) {
		if err := s.RecordCheck(ctx, PRCheckRow{PRURL: url, Name: "build", CommitHash: "c1", Status: status, CreatedAt: now}); err != nil {
			t.Fatal(err)
		}
	}
	mustCheck("in_progress") // insert  -> event
	mustCheck("failed")      // status change on same commit (update) -> event
	mustCheck("failed")      // no-op re-poll (status unchanged) -> NO event

	rows, err := s.ReadChangeLogAfter(ctx, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	var checkEvents []ChangeLogRow
	for _, r := range rows {
		if r.EventType == "pr_check_recorded" {
			checkEvents = append(checkEvents, r)
		}
	}
	if len(checkEvents) != 2 {
		t.Fatalf("want 2 check CDC events (insert + status change, no-op suppressed), got %d", len(checkEvents))
	}
	if !strings.Contains(checkEvents[1].Payload, `"status":"failed"`) {
		t.Fatalf("the update event should carry the new status, got %q", checkEvents[1].Payload)
	}
}

// WritePRObservation persists scalar facts, checks, and comments in one tx; all
// three should be queryable afterward.
func TestWritePRObservation_PersistsScalarsChecksAndComments(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "mer")
	rec, err := s.CreateSession(ctx, sampleRecord("mer"))
	if err != nil {
		t.Fatal(err)
	}
	url := "https://example/pr/7"
	now := time.Now()

	err = s.WritePRObservation(ctx,
		PRRow{URL: url, SessionID: string(rec.ID), Number: 7, CIState: "failing", UpdatedAt: now},
		[]PRCheckRow{{PRURL: url, Name: "build", CommitHash: "c1", Status: "failed", CreatedAt: now}},
		[]PRCommentRow{{PRURL: url, CommentID: "1", Author: "reviewer", Body: "use a const", CreatedAt: now}},
	)
	if err != nil {
		t.Fatal(err)
	}

	pr, ok, err := s.GetPR(ctx, url)
	if err != nil || !ok || pr.CIState != "failing" {
		t.Fatalf("scalar facts not persisted: ok=%v ci=%q err=%v", ok, pr.CIState, err)
	}
	if checks, _ := s.ListChecks(ctx, url); len(checks) != 1 || checks[0].Status != "failed" {
		t.Fatalf("check not persisted: %+v", checks)
	}
	if comments, _ := s.ListPRComments(ctx, url); len(comments) != 1 || comments[0].Body != "use a const" {
		t.Fatalf("comment not persisted: %+v", comments)
	}
}
