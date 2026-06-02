package controllers

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apispec"
)

// PRsController owns the /prs routes. These are registered as 501 shells —
// the SCM poller / webhook layer will implement the actual business logic.
type PRsController struct{}

// Register mounts the PR action routes on the supplied router.
func (c *PRsController) Register(r chi.Router) {
	r.Post("/prs/{id}/merge", c.merge)
	r.Post("/prs/{id}/resolve-comments", c.resolveComments)
}

func (c *PRsController) merge(w http.ResponseWriter, r *http.Request) {
	apispec.NotImplemented(w, r, "POST", "/api/v1/prs/{id}/merge")
}

func (c *PRsController) resolveComments(w http.ResponseWriter, r *http.Request) {
	apispec.NotImplemented(w, r, "POST", "/api/v1/prs/{id}/resolve-comments")
}

// PRIDParam is the {id} path parameter shared by the /prs/{id} routes.
type PRIDParam struct {
	ID string `path:"id" description:"PR number or identifier."`
}

// MergePRResponse is the body of POST /api/v1/prs/{id}/merge (200).
type MergePRResponse struct {
	OK       bool   `json:"ok"`
	PRNumber int    `json:"prNumber"`
	Method   string `json:"method"`
}

// ResolveCommentsRequest is the body of POST /api/v1/prs/{id}/resolve-comments.
type ResolveCommentsRequest struct {
	CommentIDs []string `json:"commentIds,omitempty"`
}

// ResolveCommentsResponse is the body of POST /api/v1/prs/{id}/resolve-comments (200).
type ResolveCommentsResponse struct {
	OK       bool `json:"ok"`
	Resolved int  `json:"resolved"`
}
