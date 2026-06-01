// Package controllers holds the HTTP-facing controllers for the /api/v1
// surface. Each controller groups one resource's routes, exposes a Register
// method, and depends on exactly one resource-level Manager interface — never
// directly on stores, lifecycle reducers, or adapters.
package controllers

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apispec"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/envelope"
	projectsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/project"
)

// ProjectsController owns the /projects routes. The controller depends only on
// projectsvc.Manager; nil keeps routes registered but returns OpenAPI-backed 501s.
type ProjectsController struct {
	Mgr projectsvc.Manager
}

// Register mounts the project routes on the supplied router.
func (c *ProjectsController) Register(r chi.Router) {
	r.Get("/projects", c.list)
	r.Post("/projects", c.add)
	r.Get("/projects/{id}", c.get)
	r.Delete("/projects/{id}", c.remove)
}

func (c *ProjectsController) list(w http.ResponseWriter, r *http.Request) {
	if c.Mgr == nil {
		apispec.NotImplemented(w, r, "GET", "/api/v1/projects")
		return
	}
	projects, err := c.Mgr.List(r.Context())
	if err != nil {
		writeProjectError(w, r, err)
		return
	}
	if projects == nil {
		projects = []projectsvc.Summary{}
	}
	envelope.WriteJSON(w, http.StatusOK, ListProjectsResponse{Projects: projects})
}

func (c *ProjectsController) add(w http.ResponseWriter, r *http.Request) {
	if c.Mgr == nil {
		apispec.NotImplemented(w, r, "POST", "/api/v1/projects")
		return
	}
	var in projectsvc.AddInput
	if err := decodeJSON(r, &in); err != nil {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_JSON", "Invalid JSON body", nil)
		return
	}
	p, err := c.Mgr.Add(r.Context(), in)
	if err != nil {
		writeProjectError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusCreated, ProjectResponse{Project: p})
}

func (c *ProjectsController) get(w http.ResponseWriter, r *http.Request) {
	if c.Mgr == nil {
		apispec.NotImplemented(w, r, "GET", "/api/v1/projects/{id}")
		return
	}
	got, err := c.Mgr.Get(r.Context(), projectID(r))
	if err != nil {
		writeProjectError(w, r, err)
		return
	}
	resp, err := newGetProjectResponse(got)
	if err != nil {
		envelope.WriteAPIError(w, r, http.StatusInternalServerError, "internal", "INTERNAL_ERROR", "Internal server error", nil)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, resp)
}

func (c *ProjectsController) remove(w http.ResponseWriter, r *http.Request) {
	if c.Mgr == nil {
		apispec.NotImplemented(w, r, "DELETE", "/api/v1/projects/{id}")
		return
	}
	result, err := c.Mgr.Remove(r.Context(), projectID(r))
	if err != nil {
		writeProjectError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, result)
}

func projectID(r *http.Request) domain.ProjectID {
	return domain.ProjectID(chi.URLParam(r, "id"))
}

func decodeJSON(r *http.Request, out any) error {
	return json.NewDecoder(r.Body).Decode(out)
}

// writeProjectError maps a projectsvc.Error to its HTTP status, falling back to
// 500 for an unrecognized kind or a non-projectsvc.Error.
func writeProjectError(w http.ResponseWriter, r *http.Request, err error) {
	var pe *projectsvc.Error
	if errors.As(err, &pe) {
		status := http.StatusInternalServerError
		switch pe.Kind {
		case "bad_request":
			status = http.StatusBadRequest
		case "not_found":
			status = http.StatusNotFound
		case "conflict":
			status = http.StatusConflict
		case "not_implemented":
			status = http.StatusNotImplemented
		case "internal":
			status = http.StatusInternalServerError
		}
		envelope.WriteAPIError(w, r, status, pe.Kind, pe.Code, pe.Message, pe.Details)
		return
	}
	envelope.WriteAPIError(w, r, http.StatusInternalServerError, "internal", "INTERNAL_ERROR", "Internal server error", nil)
}
