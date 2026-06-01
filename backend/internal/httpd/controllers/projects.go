// Package controllers holds the HTTP-facing controllers for the /api/v1
// surface. Each controller groups one resource's routes and depends only on
// that resource's application-service contract (here, project.Manager) — never
// on a store, the LCM, or an adapter.
//
// Each handler maps the request→response transport: decode the body into the
// project command, call the Manager, and encode the typed wire envelope (or
// translate the Manager's typed httpx.APIErr into the locked error envelope via
// writeErr). When Mgr is nil (no Manager wired) the handlers return 500
// SERVICE_UNAVAILABLE — the route IS implemented, only the backing service is absent.
package controllers

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/httpx"
	"github.com/aoagents/agent-orchestrator/backend/internal/project"
)

// ProjectsController owns the 5 canonical /projects routes. Mgr is nil until the
// handler-impl PR supplies a real project.Manager; while nil the handlers return
// 500 SERVICE_UNAVAILABLE.
//
// reload (dropped) and repair (deferred) are intentionally not registered —
// per the route analysis verdicts.
type ProjectsController struct {
	Mgr project.Manager
}

// Register mounts the project routes on the supplied router. REST-audit-dropped
// legacy paths are not registered: they surface as 405 (e.g. PUT or POST on
// /projects/{id}) or 404.
func (c *ProjectsController) Register(r chi.Router) {
	r.Get("/projects", c.list)
	r.Post("/projects", c.add)
	r.Get("/projects/{id}", c.get)
	r.Patch("/projects/{id}", c.updateConfig)
	r.Delete("/projects/{id}", c.remove)
}

func (c *ProjectsController) list(w http.ResponseWriter, r *http.Request) {
	if c.Mgr == nil {
		c.serviceUnavailable(w, r)
		return
	}
	items, err := c.Mgr.List(r.Context())
	if err != nil {
		c.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, ListProjectsResponse{Projects: items})
}

func (c *ProjectsController) add(w http.ResponseWriter, r *http.Request) {
	if c.Mgr == nil {
		c.serviceUnavailable(w, r)
		return
	}
	var in project.AddInput
	if err := httpx.DecodeJSON(r, &in); err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "bad_request", "INVALID_JSON", "Invalid JSON body", nil)
		return
	}
	proj, err := c.Mgr.Add(r.Context(), in)
	if err != nil {
		c.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, ProjectResponse{Project: proj})
}

func (c *ProjectsController) get(w http.ResponseWriter, r *http.Request) {
	if c.Mgr == nil {
		c.serviceUnavailable(w, r)
		return
	}
	res, err := c.Mgr.Get(r.Context(), c.projectID(r))
	if err != nil {
		c.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, newGetProjectResponse(res))
}

func (c *ProjectsController) updateConfig(w http.ResponseWriter, r *http.Request) {
	if c.Mgr == nil {
		c.serviceUnavailable(w, r)
		return
	}
	var patch project.UpdateConfigInput
	if err := httpx.DecodeJSON(r, &patch); err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, "bad_request", "INVALID_JSON", "Invalid JSON body", nil)
		return
	}
	proj, err := c.Mgr.UpdateConfig(r.Context(), c.projectID(r), patch)
	if err != nil {
		c.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, ProjectResponse{Project: proj})
}

func (c *ProjectsController) remove(w http.ResponseWriter, r *http.Request) {
	if c.Mgr == nil {
		c.serviceUnavailable(w, r)
		return
	}
	res, err := c.Mgr.Remove(r.Context(), c.projectID(r))
	if err != nil {
		c.writeErr(w, r, err)
		return
	}
	// RemoveResult is already wire-shaped ({projectId, removedStorageDir}).
	httpx.WriteJSON(w, http.StatusOK, res)
}

// projectID reads the {id} path parameter as a domain.ProjectID.
func (c *ProjectsController) projectID(r *http.Request) domain.ProjectID {
	return domain.ProjectID(chi.URLParam(r, "id"))
}

// writeErr renders a Manager error. Typed httpx.APIErr values (the Manager's
// taxonomy: 400/404/409 with machine codes + details) map straight to the
// locked envelope; anything else is an opaque 500 so internals don't leak.
func (c *ProjectsController) writeErr(w http.ResponseWriter, r *http.Request, err error) {
	if e, ok := httpx.AsAPIErr(err); ok {
		httpx.WriteAPIErr(w, r, e)
		return
	}
	httpx.WriteError(w, r, http.StatusInternalServerError, "internal", "INTERNAL", "Internal error", nil)
}

// serviceUnavailable is the route-shell response while project.Manager is nil.
// The route and its transport ARE implemented — only the backing service is not
// wired yet — so this is a server-side 500 with SERVICE_UNAVAILABLE, NOT a 501
// "not implemented" (which would wrongly say the route doesn't exist yet).
func (c *ProjectsController) serviceUnavailable(w http.ResponseWriter, r *http.Request) {
	httpx.WriteError(w, r, http.StatusInternalServerError, "internal", "SERVICE_UNAVAILABLE",
		"Project service is not available", nil)
}
