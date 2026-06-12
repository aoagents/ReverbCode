package controllers

import (
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apispec"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/envelope"
	reviewsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/review"
)

// ListReviewsResponse is the body of GET /api/v1/sessions/{sessionId}/reviews.
type ListReviewsResponse struct {
	Reviews []domain.ReviewRun `json:"reviews"`
}

// ReviewRunResponse is the { review } body of trigger (201).
type ReviewRunResponse struct {
	Review domain.ReviewRun `json:"review"`
}

// ReviewsController owns the session-scoped /reviews routes. A nil Svc returns 501.
type ReviewsController struct {
	Svc reviewsvc.Manager
}

// Register mounts the review routes on the supplied router.
func (c *ReviewsController) Register(r chi.Router) {
	r.Get("/sessions/{sessionId}/reviews", c.list)
	r.Post("/sessions/{sessionId}/reviews/trigger", c.trigger)
}

func (c *ReviewsController) list(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "GET", "/api/v1/sessions/{sessionId}/reviews")
		return
	}
	runs, err := c.Svc.List(r.Context(), sessionID(r))
	if err != nil {
		writeReviewError(w, r, err)
		return
	}
	if runs == nil {
		runs = []domain.ReviewRun{}
	}
	envelope.WriteJSON(w, http.StatusOK, ListReviewsResponse{Reviews: runs})
}

func (c *ReviewsController) trigger(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "POST", "/api/v1/sessions/{sessionId}/reviews/trigger")
		return
	}
	run, err := c.Svc.Trigger(r.Context(), sessionID(r))
	if err != nil {
		writeReviewError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusCreated, ReviewRunResponse{Review: run})
}

func writeReviewError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, reviewsvc.ErrInvalid):
		envelope.WriteAPIError(w, r, http.StatusUnprocessableEntity, "unprocessable", "REVIEW_INVALID", err.Error(), nil)
	case errors.Is(err, reviewsvc.ErrNotFound):
		envelope.WriteAPIError(w, r, http.StatusNotFound, "not_found", "REVIEW_NOT_FOUND", err.Error(), nil)
	default:
		envelope.WriteAPIError(w, r, http.StatusInternalServerError, "internal", "REVIEW_OPERATION_FAILED", "Review operation failed", nil)
	}
}
