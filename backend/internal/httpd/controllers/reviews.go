package controllers

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apispec"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/envelope"
	reviewsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/review"
)

// ReviewIDParam is the {id} path parameter on the /reviews/{id} routes.
type ReviewIDParam struct {
	ID string `path:"id" description:"Review run id."`
}

// ListReviewsResponse is the body of GET /api/v1/reviews.
type ListReviewsResponse struct {
	Reviews []reviewsvc.Run `json:"reviews"`
}

// ExecuteReviewInput is the body of POST /api/v1/reviews/execute.
type ExecuteReviewInput struct {
	SessionID string `json:"sessionId" description:"Session whose PR to review."`
}

// ReviewResponse is the { review } body of execute (201) and send (200).
type ReviewResponse struct {
	Review reviewsvc.Run `json:"review"`
}

// ReviewsController owns the /reviews routes. A nil Svc returns 501.
type ReviewsController struct {
	Svc reviewsvc.Manager
}

// Register mounts the review routes on the supplied router.
func (c *ReviewsController) Register(r chi.Router) {
	r.Get("/reviews", c.list)
	r.Post("/reviews/execute", c.execute)
	r.Post("/reviews/{id}/send", c.send)
}

func (c *ReviewsController) list(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "GET", "/api/v1/reviews")
		return
	}
	runs, err := c.Svc.List(r.Context())
	if err != nil {
		writeReviewError(w, r, err)
		return
	}
	if runs == nil {
		runs = []reviewsvc.Run{}
	}
	envelope.WriteJSON(w, http.StatusOK, ListReviewsResponse{Reviews: runs})
}

func (c *ReviewsController) execute(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "POST", "/api/v1/reviews/execute")
		return
	}
	var in ExecuteReviewInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_BODY", "Invalid request body", nil)
		return
	}
	run, err := c.Svc.Execute(r.Context(), in.SessionID)
	if err != nil {
		writeReviewError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusCreated, ReviewResponse{Review: run})
}

func (c *ReviewsController) send(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "POST", "/api/v1/reviews/{id}/send")
		return
	}
	run, err := c.Svc.Send(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		writeReviewError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, ReviewResponse{Review: run})
}

func writeReviewError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, reviewsvc.ErrInvalid):
		envelope.WriteAPIError(w, r, http.StatusUnprocessableEntity, "unprocessable", "REVIEW_INVALID", err.Error(), nil)
	case errors.Is(err, reviewsvc.ErrNotFound):
		envelope.WriteAPIError(w, r, http.StatusNotFound, "not_found", "REVIEW_NOT_FOUND", "Unknown review run", nil)
	default:
		envelope.WriteAPIError(w, r, http.StatusInternalServerError, "internal", "REVIEW_OPERATION_FAILED", "Review operation failed", nil)
	}
}
