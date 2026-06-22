package controllers_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/controllers"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	reviewcore "github.com/aoagents/agent-orchestrator/backend/internal/review"
	reviewsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/review"
)

type fakeReviewsService struct {
	listWorker    domain.SessionID
	listPRURL     string
	triggerWorker domain.SessionID
	triggerPRURL  string
	triggerErr    error
}

func (f *fakeReviewsService) Trigger(_ context.Context, workerID domain.SessionID, prURL string) (reviewcore.TriggerResult, error) {
	f.triggerWorker = workerID
	f.triggerPRURL = prURL
	if f.triggerErr != nil {
		return reviewcore.TriggerResult{}, f.triggerErr
	}
	return reviewcore.TriggerResult{
		Run:              domain.ReviewRun{ID: "run-1", SessionID: workerID, PRURL: prURL},
		ReviewerHandleID: "reviewer-1",
		Created:          true,
	}, nil
}

func (f *fakeReviewsService) Submit(_ context.Context, workerID domain.SessionID, runID string, verdict domain.ReviewVerdict, body, githubReviewID string) (domain.ReviewRun, error) {
	return domain.ReviewRun{ID: runID, SessionID: workerID, Verdict: verdict, Body: body, GithubReviewID: githubReviewID}, nil
}

func (f *fakeReviewsService) List(_ context.Context, workerID domain.SessionID, prURL string) (reviewcore.SessionReviews, error) {
	f.listWorker = workerID
	f.listPRURL = prURL
	return reviewcore.SessionReviews{
		ReviewerHandleID: "reviewer-1",
		Runs:             []domain.ReviewRun{{ID: "run-1", SessionID: workerID, PRURL: prURL}},
		Targets:          []reviewcore.Target{{PRURL: prURL, ReviewerHandleID: "reviewer-1", Runs: []domain.ReviewRun{{ID: "run-1", SessionID: workerID, PRURL: prURL}}}},
	}, nil
}

func reviewTestRouter(svc *fakeReviewsService) http.Handler {
	r := chi.NewRouter()
	(&controllers.ReviewsController{Svc: svc}).Register(r)
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
	var out controllers.ListReviewsResponse
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

func newReviewTestServer(t *testing.T, svc reviewsvc.Manager) *httptest.Server {
	t.Helper()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := httptest.NewServer(httpd.NewRouterWithControl(config.Config{}, log, nil, httpd.APIDeps{Reviews: svc}, httpd.ControlDeps{}))
	t.Cleanup(srv.Close)
	return srv
}

func TestReviewsTrigger_MissingReviewerBinaryReturns422WithCause(t *testing.T) {
	err := fmt.Errorf("launch reviewer: reviewer command: claude: %w", ports.ErrAgentBinaryNotFound)
	srv := newReviewTestServer(t, &fakeReviewsService{triggerErr: err})

	body, status, headers := doRequest(t, srv, "POST", "/api/v1/sessions/mer-1/reviews/trigger", "")
	assertJSON(t, headers)
	assertErrorCode(t, body, status, http.StatusUnprocessableEntity, "REVIEWER_BINARY_NOT_FOUND")

	var got errorBody
	mustJSON(t, body, &got)
	if !strings.Contains(got.Message, "claude") || !strings.Contains(got.Message, ports.ErrAgentBinaryNotFound.Error()) {
		t.Fatalf("message = %q, want reviewer binary cause", got.Message)
	}
}
