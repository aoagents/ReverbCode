// Package controllers holds the HTTP-facing controllers for the /api/v1
// surface. Each controller groups one resource's routes, exposes a Register
// method that wires them on a chi.Router, and depends on exactly one
// *Manager interface from ports/inbound.go — never on a store, the LCM, an
// adapter, or any other port. Whether the Manager impl reaches past that
// boundary is its own concern.
//
// In the route-shell PR (#20) every handler is a one-line apispec.NotImplemented
// call: the contract lives in the OpenAPI document (apispec/openapi.yaml), and
// the 501 body returns that document's slice for the route so consumers can
// discover the contract from the endpoint itself. When real handlers land,
// the stub one-liner is replaced with the impl; no per-route planned
// metadata in code ever has to be deleted.
package controllers

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apispec"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// ProjectsController owns the 7 canonical /projects routes. The controller
// depends ONLY on ports.ProjectManager — it doesn't know whether the impl
// reaches into the registry, the LCM, an adapter, or all three. Mgr is nil
// while handlers are stubs; impl PRs supply a real ProjectManager.
type ProjectsController struct {
	Mgr ports.ProjectManager
}

// Register mounts the project routes on the supplied router. Route order
// matters: /projects/reload must register before /projects/{id} for the POST
// verb, otherwise chi would treat "reload" as an {id} match for repair.
//
// Legacy paths that the REST audit dropped are deliberately NOT registered
// here. They surface as 405 (sibling method exists, e.g. PUT /projects/{id})
// or 404 (no sibling). The mapping lives in apispec/openapi.yaml as
// `x-replaces` on the canonical operation so consumers discover the
// migration without leaving the spec.
func (c *ProjectsController) Register(r chi.Router) {
	r.Get("/projects", c.list)
	r.Post("/projects", c.add)
	r.Post("/projects/reload", c.reload) // BEFORE /projects/{id}
	r.Get("/projects/{id}", c.get)
	r.Patch("/projects/{id}", c.updateConfig)
	r.Delete("/projects/{id}", c.remove)
	r.Post("/projects/{id}/repair", c.repair)
}

func (c *ProjectsController) list(w http.ResponseWriter, r *http.Request) {
	apispec.NotImplemented(w, r, "GET", "/api/v1/projects")
}

func (c *ProjectsController) add(w http.ResponseWriter, r *http.Request) {
	apispec.NotImplemented(w, r, "POST", "/api/v1/projects")
}

func (c *ProjectsController) get(w http.ResponseWriter, r *http.Request) {
	apispec.NotImplemented(w, r, "GET", "/api/v1/projects/{id}")
}

func (c *ProjectsController) updateConfig(w http.ResponseWriter, r *http.Request) {
	apispec.NotImplemented(w, r, "PATCH", "/api/v1/projects/{id}")
}

func (c *ProjectsController) remove(w http.ResponseWriter, r *http.Request) {
	apispec.NotImplemented(w, r, "DELETE", "/api/v1/projects/{id}")
}

func (c *ProjectsController) repair(w http.ResponseWriter, r *http.Request) {
	apispec.NotImplemented(w, r, "POST", "/api/v1/projects/{id}/repair")
}

func (c *ProjectsController) reload(w http.ResponseWriter, r *http.Request) {
	apispec.NotImplemented(w, r, "POST", "/api/v1/projects/reload")
}
