package controllers_test

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	reviewcore "github.com/aoagents/agent-orchestrator/backend/internal/review"
	reviewsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/review"
)

type fakeReviewService struct {
	triggerErr error
}

func (f *fakeReviewService) Trigger(context.Context, domain.SessionID) (reviewcore.TriggerResult, error) {
	if f.triggerErr != nil {
		return reviewcore.TriggerResult{}, f.triggerErr
	}
	return reviewcore.TriggerResult{Run: domain.ReviewRun{ID: "run-1"}, Created: true}, nil
}

func (f *fakeReviewService) Submit(context.Context, domain.SessionID, string, domain.ReviewVerdict, string, string) (domain.ReviewRun, error) {
	return domain.ReviewRun{}, nil
}

func (f *fakeReviewService) List(context.Context, domain.SessionID) (reviewcore.SessionReviews, error) {
	return reviewcore.SessionReviews{}, nil
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
	srv := newReviewTestServer(t, &fakeReviewService{triggerErr: err})

	body, status, headers := doRequest(t, srv, "POST", "/api/v1/sessions/mer-1/reviews/trigger", "")
	assertJSON(t, headers)
	assertErrorCode(t, body, status, http.StatusUnprocessableEntity, "REVIEWER_BINARY_NOT_FOUND")

	var got errorBody
	mustJSON(t, body, &got)
	if !strings.Contains(got.Message, "claude") || !strings.Contains(got.Message, ports.ErrAgentBinaryNotFound.Error()) {
		t.Fatalf("message = %q, want reviewer binary cause", got.Message)
	}
}
