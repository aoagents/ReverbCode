package controllers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	reviewcore "github.com/aoagents/agent-orchestrator/backend/internal/review"
)

type fakeReviewsService struct {
	listWorker    domain.SessionID
	listPRURL     string
	triggerWorker domain.SessionID
	triggerPRURL  string
}

func (f *fakeReviewsService) Trigger(_ context.Context, workerID domain.SessionID, prURL string) (reviewcore.TriggerResult, error) {
	f.triggerWorker = workerID
	f.triggerPRURL = prURL
	return reviewcore.TriggerResult{
		Run:              domain.ReviewRun{ID: "run-1", SessionID: workerID, PRURL: prURL},
		ReviewerHandleID: "reviewer-1",
		Created:          true,
	}, nil
}

func (f *fakeReviewsService) Submit(_ context.Context, workerID domain.SessionID, runID string, verdict domain.ReviewVerdict, body string) (domain.ReviewRun, error) {
	return domain.ReviewRun{ID: runID, SessionID: workerID, Verdict: verdict, Body: body}, nil
}

func (f *fakeReviewsService) List(_ context.Context, workerID domain.SessionID, prURL string) (reviewcore.SessionReviews, error) {
	f.listWorker = workerID
	f.listPRURL = prURL
	return reviewcore.SessionReviews{
		ReviewerHandleID: "reviewer-1",
		Runs:             []domain.ReviewRun{{ID: "run-1", SessionID: workerID, PRURL: prURL}},
		Targets:          []reviewcore.ReviewTarget{{PRURL: prURL, ReviewerHandleID: "reviewer-1", Runs: []domain.ReviewRun{{ID: "run-1", SessionID: workerID, PRURL: prURL}}}},
	}, nil
}

func reviewTestRouter(svc *fakeReviewsService) http.Handler {
	r := chi.NewRouter()
	(&ReviewsController{Svc: svc}).Register(r)
	return r
}

func TestReviewsListPassesPRURLQuery(t *testing.T) {
	svc := &fakeReviewsService{}
	req := httptest.NewRequest(http.MethodGet, "/sessions/mer-1/reviews?prUrl=https%3A%2F%2Fgithub.com%2Fo%2Fr%2Fpull%2F1", nil)
	rec := httptest.NewRecorder()

	reviewTestRouter(svc).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	if svc.listWorker != "mer-1" || svc.listPRURL != "https://github.com/o/r/pull/1" {
		t.Fatalf("list args worker=%q pr=%q", svc.listWorker, svc.listPRURL)
	}
	var out ListReviewsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(out.Targets) != 1 || out.Targets[0].PRURL != "https://github.com/o/r/pull/1" {
		t.Fatalf("targets = %+v", out.Targets)
	}
}

func TestReviewsTriggerAcceptsOptionalPRURLBody(t *testing.T) {
	svc := &fakeReviewsService{}
	req := httptest.NewRequest(http.MethodPost, "/sessions/mer-1/reviews/trigger", strings.NewReader(`{"prUrl":"https://github.com/o/r/pull/1"}`))
	rec := httptest.NewRecorder()

	reviewTestRouter(svc).ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	if svc.triggerWorker != "mer-1" || svc.triggerPRURL != "https://github.com/o/r/pull/1" {
		t.Fatalf("trigger args worker=%q pr=%q", svc.triggerWorker, svc.triggerPRURL)
	}
}

func TestReviewsTriggerAcceptsEmptyBody(t *testing.T) {
	svc := &fakeReviewsService{}
	req := httptest.NewRequest(http.MethodPost, "/sessions/mer-1/reviews/trigger", nil)
	rec := httptest.NewRecorder()

	reviewTestRouter(svc).ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	if svc.triggerWorker != "mer-1" || svc.triggerPRURL != "" {
		t.Fatalf("trigger args worker=%q pr=%q", svc.triggerWorker, svc.triggerPRURL)
	}
}
