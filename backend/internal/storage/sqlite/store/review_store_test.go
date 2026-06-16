package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func TestReviewUpsertScopesRowsBySessionAndPR(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "mer")
	rec, err := s.CreateSession(ctx, sampleRecord("mer"))
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	now := time.Now().UTC().Truncate(time.Second)

	pr1 := "https://example/pr/1"
	pr2 := "https://example/pr/2"

	// First upsert creates the review row for PR 1.
	if err := s.UpsertReview(ctx, domain.Review{
		ID: "rev-1", SessionID: rec.ID, ProjectID: rec.ProjectID,
		Harness: domain.ReviewerClaudeCode, PRURL: pr1,
		ReviewerHandleID: "review-mer-1",
		CreatedAt:        now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("upsert review: %v", err)
	}
	// A different PR in the same session gets a distinct review row.
	if err := s.UpsertReview(ctx, domain.Review{
		ID: "rev-2", SessionID: rec.ID, ProjectID: rec.ProjectID,
		Harness: domain.ReviewerHarness("greptile"), PRURL: pr2,
		ReviewerHandleID: "review-mer-1b",
		CreatedAt:        now, UpdatedAt: now.Add(time.Second),
	}); err != nil {
		t.Fatalf("upsert second review: %v", err)
	}
	// Upserting the same session+PR reuses that PR's row and keeps its id.
	if err := s.UpsertReview(ctx, domain.Review{
		ID: "rev-3", SessionID: rec.ID, ProjectID: rec.ProjectID,
		Harness: domain.ReviewerHarness("greptile"), PRURL: pr1,
		ReviewerHandleID: "review-mer-1c",
		CreatedAt:        now, UpdatedAt: now.Add(2 * time.Second),
	}); err != nil {
		t.Fatalf("upsert first review again: %v", err)
	}

	got, ok, err := s.GetReviewBySessionAndPR(ctx, rec.ID, pr1)
	if err != nil || !ok {
		t.Fatalf("get review: ok=%v err=%v", ok, err)
	}
	if got.ID != "rev-1" {
		t.Fatalf("upsert created a new row, want reuse: id=%q", got.ID)
	}
	if got.Harness != domain.ReviewerHarness("greptile") || got.PRURL != pr1 || got.ReviewerHandleID != "review-mer-1c" {
		t.Fatalf("upsert did not refresh fields: %+v", got)
	}
	reviews, err := s.ListReviewsBySession(ctx, rec.ID)
	if err != nil {
		t.Fatalf("list reviews: %v", err)
	}
	if len(reviews) != 2 {
		t.Fatalf("reviews = %+v, want 2", reviews)
	}

	// A run inserts running and updates to complete/changes_requested.
	if err := s.InsertReviewRun(ctx, domain.ReviewRun{
		ID: "run-1", ReviewID: got.ID, SessionID: rec.ID, Harness: domain.ReviewerHarness("greptile"),
		PRURL: pr1, TargetSHA: "sha1", Status: domain.ReviewRunRunning, Verdict: domain.VerdictNone,
		CreatedAt: now,
	}); err != nil {
		t.Fatalf("insert run: %v", err)
	}
	if err := s.InsertReviewRun(ctx, domain.ReviewRun{
		ID: "run-2", ReviewID: "rev-2", SessionID: rec.ID, Harness: domain.ReviewerHarness("greptile"),
		PRURL: pr2, TargetSHA: "sha1", Status: domain.ReviewRunRunning, Verdict: domain.VerdictNone,
		CreatedAt: now,
	}); err != nil {
		t.Fatalf("insert run: %v", err)
	}
	if ok, err := s.UpdateReviewRunResult(ctx, "run-1", domain.ReviewRunComplete, domain.VerdictChangesRequested, "please fix"); err != nil {
		t.Fatalf("update run: %v", err)
	} else if !ok {
		t.Fatal("update run: got ok=false")
	}

	gotRun, ok, err := s.GetReviewRun(ctx, "run-1")
	if err != nil || !ok {
		t.Fatalf("get run: ok=%v err=%v", ok, err)
	}
	if gotRun.ID != "run-1" || gotRun.SessionID != rec.ID || gotRun.TargetSHA != "sha1" {
		t.Fatalf("get run = %+v", gotRun)
	}

	bySHA, ok, err := s.GetReviewRunBySessionPRAndSHA(ctx, rec.ID, pr1, "sha1")
	if err != nil || !ok {
		t.Fatalf("by sha: ok=%v err=%v", ok, err)
	}
	if bySHA.ID != "run-1" || bySHA.Status != domain.ReviewRunComplete || bySHA.Verdict != domain.VerdictChangesRequested || bySHA.Body != "please fix" {
		t.Fatalf("run result not persisted: %+v", bySHA)
	}
	bySHA, ok, err = s.GetReviewRunBySessionPRAndSHA(ctx, rec.ID, pr2, "sha1")
	if err != nil || !ok {
		t.Fatalf("by sha for second pr: ok=%v err=%v", ok, err)
	}
	if bySHA.ID != "run-2" {
		t.Fatalf("same sha on second pr should resolve second run, got %+v", bySHA)
	}
	if _, ok, _ := s.GetReviewRunBySessionPRAndSHA(ctx, rec.ID, pr1, "other"); ok {
		t.Fatal("unexpected run for a different sha")
	}

	runs, err := s.ListReviewRunsBySession(ctx, rec.ID)
	if err != nil {
		t.Fatalf("list runs: %v", err)
	}
	if len(runs) != 2 {
		t.Fatalf("list runs = %+v", runs)
	}
	runs, err = s.ListReviewRunsBySessionAndPR(ctx, rec.ID, pr1)
	if err != nil {
		t.Fatalf("list runs by pr: %v", err)
	}
	if len(runs) != 1 || runs[0].ID != "run-1" {
		t.Fatalf("list runs by pr = %+v", runs)
	}

	if ok, err := s.UpdateReviewRunResult(ctx, "run-1", domain.ReviewRunComplete, domain.VerdictApproved, "again"); err != nil {
		t.Fatalf("second update: %v", err)
	} else if ok {
		t.Fatal("second update completed an already-complete run")
	}
}

func TestReviewGettersMissing(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	if _, ok, err := s.GetReviewBySessionAndPR(ctx, "mer-1", "https://example/pr/1"); err != nil || ok {
		t.Fatalf("missing review: ok=%v err=%v", ok, err)
	}
	if _, ok, err := s.GetReviewRunBySessionPRAndSHA(ctx, "mer-1", "https://example/pr/1", "sha1"); err != nil || ok {
		t.Fatalf("missing run: ok=%v err=%v", ok, err)
	}
	if _, ok, err := s.GetReviewRun(ctx, "run-missing"); err != nil || ok {
		t.Fatalf("missing run by id: ok=%v err=%v", ok, err)
	}
}
