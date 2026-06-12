package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func TestReviewUpsertReusesRowAndRunRoundTrip(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "mer")
	rec, err := s.CreateSession(ctx, sampleRecord("mer"))
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	now := time.Now().UTC().Truncate(time.Second)

	// First upsert creates the review row.
	if err := s.UpsertReview(ctx, domain.Review{
		ID: "rev-1", SessionID: rec.ID, ProjectID: rec.ProjectID,
		Harness: domain.ReviewerClaudeCode, PRURL: "https://example/pr/1",
		CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("upsert review: %v", err)
	}
	// Second upsert with the same session reuses the row (session_id UNIQUE),
	// refreshing harness/pr_url but keeping the original id.
	if err := s.UpsertReview(ctx, domain.Review{
		ID: "rev-2", SessionID: rec.ID, ProjectID: rec.ProjectID,
		Harness: domain.ReviewerHarness("greptile"), PRURL: "https://example/pr/2",
		CreatedAt: now, UpdatedAt: now.Add(time.Second),
	}); err != nil {
		t.Fatalf("upsert review (reuse): %v", err)
	}
	got, ok, err := s.GetReviewBySession(ctx, rec.ID)
	if err != nil || !ok {
		t.Fatalf("get review: ok=%v err=%v", ok, err)
	}
	if got.ID != "rev-1" {
		t.Fatalf("upsert created a new row, want reuse: id=%q", got.ID)
	}
	if got.Harness != domain.ReviewerHarness("greptile") || got.PRURL != "https://example/pr/2" {
		t.Fatalf("upsert did not refresh fields: %+v", got)
	}

	// A run inserts pending and updates to complete/changes_requested.
	if err := s.InsertReviewRun(ctx, domain.ReviewRun{
		ID: "run-1", ReviewID: got.ID, SessionID: rec.ID, Harness: domain.ReviewerHarness("greptile"),
		PRURL: got.PRURL, Status: domain.ReviewRunRunning, Verdict: domain.VerdictNone,
		Iteration: 1, CreatedAt: now,
	}); err != nil {
		t.Fatalf("insert run: %v", err)
	}
	if err := s.UpdateReviewRunResult(ctx, "run-1", domain.ReviewRunComplete, domain.VerdictChangesRequested, "please fix"); err != nil {
		t.Fatalf("update run: %v", err)
	}

	latest, ok, err := s.GetLatestReviewRunBySession(ctx, rec.ID)
	if err != nil || !ok {
		t.Fatalf("latest run: ok=%v err=%v", ok, err)
	}
	if latest.Status != domain.ReviewRunComplete || latest.Verdict != domain.VerdictChangesRequested || latest.Body != "please fix" {
		t.Fatalf("run result not persisted: %+v", latest)
	}

	runs, err := s.ListReviewRunsBySession(ctx, rec.ID)
	if err != nil {
		t.Fatalf("list runs: %v", err)
	}
	if len(runs) != 1 || runs[0].ID != "run-1" {
		t.Fatalf("list runs = %+v", runs)
	}
}

func TestReviewGettersMissing(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	if _, ok, err := s.GetReviewBySession(ctx, "mer-1"); err != nil || ok {
		t.Fatalf("missing review: ok=%v err=%v", ok, err)
	}
	if _, ok, err := s.GetLatestReviewRunBySession(ctx, "mer-1"); err != nil || ok {
		t.Fatalf("missing run: ok=%v err=%v", ok, err)
	}
}
