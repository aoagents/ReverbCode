package controllers

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apispec"
)

// SessionsController owns the canonical /sessions route shell. Business logic
// lands later; these handlers expose the code-generated contract as 501s.
type SessionsController struct{}

// Register mounts only routes whose request/response bytes are settled enough
// for the first session route-shell slice.
func (c *SessionsController) Register(r chi.Router) {
	r.Get("/sessions", c.list)
	r.Post("/sessions", c.spawn)
	r.Get("/sessions/{id}", c.get)
	r.Post("/sessions/{id}/restore", c.restore)
	r.Post("/sessions/{id}/send", c.send)
}

func (c *SessionsController) list(w http.ResponseWriter, r *http.Request) {
	apispec.NotImplemented(w, r, "GET", "/api/v1/sessions")
}

func (c *SessionsController) spawn(w http.ResponseWriter, r *http.Request) {
	apispec.NotImplemented(w, r, "POST", "/api/v1/sessions")
}

func (c *SessionsController) get(w http.ResponseWriter, r *http.Request) {
	apispec.NotImplemented(w, r, "GET", "/api/v1/sessions/{id}")
}

func (c *SessionsController) restore(w http.ResponseWriter, r *http.Request) {
	apispec.NotImplemented(w, r, "POST", "/api/v1/sessions/{id}/restore")
}

func (c *SessionsController) send(w http.ResponseWriter, r *http.Request) {
	apispec.NotImplemented(w, r, "POST", "/api/v1/sessions/{id}/send")
}
