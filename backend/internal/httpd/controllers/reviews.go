package controllers

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apispec"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/envelope"
	reviewsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/review"
)

// ListReviewsQuery selects one PR's review state. Omit prUrl to list every PR
// review target for the session.
type ListReviewsQuery struct {
	PRURL string `query:"prUrl,omitempty" description:"Tracked PR URL to list review runs for. Omit to list all PR review targets for the session."`
}

// ReviewTargetResponse is one PR's review state within a worker session.
type ReviewTargetResponse struct {
	PRURL            string             `json:"prUrl"`
	ReviewerHandleID string             `json:"reviewerHandleId"`
	Reviews          []domain.ReviewRun `json:"reviews"`
}

// ListReviewsResponse is the body of GET /api/v1/sessions/{sessionId}/reviews.
// reviewerHandleId is the live reviewer pane's runtime handle, for the UI to
// attach its terminal over /mux (empty when no reviewer has run).
type ListReviewsResponse struct {
	ReviewerHandleID string                 `json:"reviewerHandleId"`
	Reviews          []domain.ReviewRun     `json:"reviews"`
	Targets          []ReviewTargetResponse `json:"targets"`
}

// ReviewRunResponse is the body of trigger (200/201) and submit (200). It
// carries the run plus the reviewer pane handle so the UI can attach a terminal.
type ReviewRunResponse struct {
	Review           domain.ReviewRun `json:"review"`
	ReviewerHandleID string           `json:"reviewerHandleId"`
}

// TriggerReviewInput is the optional body of
// POST /api/v1/sessions/{sessionId}/reviews/trigger.
type TriggerReviewInput struct {
	PRURL string `json:"prUrl,omitempty" description:"Tracked PR URL to review. Required when the session owns multiple PRs."`
}

// SubmitReviewInput is the body of POST /api/v1/sessions/{sessionId}/reviews/submit.
type SubmitReviewInput struct {
	RunID          string `json:"runId" description:"Review run id being completed."`
	Verdict        string `json:"verdict" description:"Review verdict: approved or changes_requested."`
	Body           string `json:"body" description:"Review body recorded by AO. Required for changes_requested."`
	GithubReviewID string `json:"githubReviewId" description:"Id of the GitHub PR review the reviewer posted, if any."`
}

// ReviewsController owns the session-scoped /reviews routes. A nil Svc returns 501.
type ReviewsController struct {
	Svc reviewsvc.Manager
}

// Register mounts the review routes on the supplied router.
func (c *ReviewsController) Register(r chi.Router) {
	r.Get("/sessions/{sessionId}/reviews", c.list)
	r.Post("/sessions/{sessionId}/reviews/trigger", c.trigger)
	r.Post("/sessions/{sessionId}/reviews/submit", c.submit)
}

func (c *ReviewsController) list(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "GET", "/api/v1/sessions/{sessionId}/reviews")
		return
	}
	res, err := c.Svc.List(r.Context(), sessionID(r), r.URL.Query().Get("prUrl"))
	if err != nil {
		writeReviewError(w, r, err)
		return
	}
	runs := res.Runs
	if runs == nil {
		runs = []domain.ReviewRun{}
	}
	envelope.WriteJSON(w, http.StatusOK, ListReviewsResponse{ReviewerHandleID: res.ReviewerHandleID, Reviews: runs, Targets: reviewTargetsResponse(res.Targets)})
}

func (c *ReviewsController) trigger(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "POST", "/api/v1/sessions/{sessionId}/reviews/trigger")
		return
	}
	var in TriggerReviewInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil && !errors.Is(err, io.EOF) {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_BODY", "Invalid request body", nil)
		return
	}
	res, err := c.Svc.Trigger(r.Context(), sessionID(r), in.PRURL)
	if err != nil {
		writeReviewError(w, r, err)
		return
	}
	// 201 when a new pass was started; 200 when an existing run for the same
	// commit was reused.
	status := http.StatusOK
	if res.Created {
		status = http.StatusCreated
	}
	envelope.WriteJSON(w, status, ReviewRunResponse{Review: res.Run, ReviewerHandleID: res.ReviewerHandleID})
}

func reviewTargetsResponse(targets []reviewsvc.ReviewTarget) []ReviewTargetResponse {
	out := make([]ReviewTargetResponse, 0, len(targets))
	for _, target := range targets {
		runs := target.Runs
		if runs == nil {
			runs = []domain.ReviewRun{}
		}
		out = append(out, ReviewTargetResponse{
			PRURL:            target.PRURL,
			ReviewerHandleID: target.ReviewerHandleID,
			Reviews:          runs,
		})
	}
	return out
}

func (c *ReviewsController) submit(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "POST", "/api/v1/sessions/{sessionId}/reviews/submit")
		return
	}
	var in SubmitReviewInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_BODY", "Invalid request body", nil)
		return
	}
	run, err := c.Svc.Submit(r.Context(), sessionID(r), in.RunID, domain.ReviewVerdict(in.Verdict), in.Body, in.GithubReviewID)
	if err != nil {
		writeReviewError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, ReviewRunResponse{Review: run})
}

func writeReviewError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, reviewsvc.ErrInvalid):
		envelope.WriteAPIError(w, r, http.StatusUnprocessableEntity, "unprocessable", "REVIEW_INVALID", err.Error(), nil)
	case errors.Is(err, reviewsvc.ErrNotFound):
		envelope.WriteAPIError(w, r, http.StatusNotFound, "not_found", "REVIEW_NOT_FOUND", err.Error(), nil)
	case errors.Is(err, reviewsvc.ErrAgentBinaryNotFound):
		envelope.WriteAPIError(w, r, http.StatusUnprocessableEntity, "unprocessable", "REVIEWER_BINARY_NOT_FOUND", err.Error(), nil)
	default:
		envelope.WriteAPIError(w, r, http.StatusInternalServerError, "internal", "REVIEW_OPERATION_FAILED", "Review operation failed", nil)
	}
}
