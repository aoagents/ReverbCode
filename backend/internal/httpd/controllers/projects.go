// Package controllers holds the HTTP-facing controllers for the /api/v1
// surface. Each controller groups one resource's routes, exposes a Register
// method that wires them on a chi.Router, and carries the service dependency
// it will eventually call.
//
// In the route-shell PR (#20) every handler returns 501 with a PlannedRoute
// body documenting the future contract. Handler-impl PRs in the same lane
// flip routes to real logic one at a time without touching the wiring.
package controllers

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/stubs"
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
// or 404 (no sibling, e.g. POST /projects/{id} for repair → moved to
// /projects/{id}/repair). The mapping lives in each canonical handler's
// PlannedRoute.Legacy so consumers can discover the migration.
func (c *ProjectsController) Register(r chi.Router) {
	r.Get("/projects", c.list)
	r.Post("/projects", c.add)
	r.Post("/projects/reload", c.reload) // BEFORE /projects/{id}
	r.Get("/projects/{id}", c.get)
	r.Patch("/projects/{id}", c.updateConfig)
	r.Delete("/projects/{id}", c.remove)
	r.Post("/projects/{id}/repair", c.repair)
}

// list — GET /api/v1/projects
func (c *ProjectsController) list(w http.ResponseWriter, r *http.Request) {
	stubs.NotImplemented(w, r, stubs.PlannedRoute{
		Route: "GET /api/v1/projects",
		Response: map[string]any{
			"200": map[string]any{"projects": "[]domain.ProjectSummary"},
		},
		Errors: []stubs.PlannedError{
			{Status: 500, Code: "PROJECTS_LIST_FAILED", Message: "Failed to load projects"},
		},
		Notes: "Wraps the registry list. Includes degraded entries with a resolveError field.",
	})
}

// add — POST /api/v1/projects
func (c *ProjectsController) add(w http.ResponseWriter, r *http.Request) {
	stubs.NotImplemented(w, r, stubs.PlannedRoute{
		Route: "POST /api/v1/projects",
		Request: map[string]any{
			"body": map[string]any{
				"path":      "string (required; supports ~ home-expansion; must point to a git repo)",
				"projectId": "string (optional; defaults to basename(path))",
				"name":      "string (optional; defaults to projectId)",
			},
		},
		Response: map[string]any{
			"201": map[string]any{"project": "domain.Project"},
		},
		Errors: []stubs.PlannedError{
			{Status: 400, Code: "INVALID_JSON", Message: "Invalid JSON body"},
			{Status: 400, Code: "PATH_REQUIRED", Message: "Repository path is required"},
			{Status: 400, Code: "NOT_A_GIT_REPO", Message: "Repository path must point to a git repository"},
			{
				Status: 409, Code: "PATH_ALREADY_REGISTERED",
				Message: "A project at this path is already registered",
				Details: map[string]any{
					"existingProjectId":  "string",
					"suggestedProjectId": "string",
				},
			},
			{
				Status: 409, Code: "ID_ALREADY_REGISTERED",
				Message: "A project with this id is already registered for a different path",
				Details: map[string]any{
					"existingProjectId":  "string",
					"suggestedProjectId": "string",
				},
			},
		},
	})
}

// get — GET /api/v1/projects/{id}
func (c *ProjectsController) get(w http.ResponseWriter, r *http.Request) {
	stubs.NotImplemented(w, r, stubs.PlannedRoute{
		Route: "GET /api/v1/projects/{id}",
		Request: map[string]any{
			"path": map[string]any{"id": "ProjectID"},
		},
		Response: map[string]any{
			"200": map[string]any{
				"status":  `"ok" | "degraded"`,
				"project": "domain.Project (status=ok) | domain.DegradedProject (status=degraded)",
			},
		},
		Errors: []stubs.PlannedError{
			{Status: 404, Code: "PROJECT_NOT_FOUND", Message: "Unknown project"},
			{Status: 500, Code: "PROJECT_LOAD_FAILED", Message: "Failed to load project"},
		},
		Notes: "REST-audit R5: degraded projects return 200 with a status discriminator, not 200 with an error field.",
	})
}

// updateConfig — PATCH /api/v1/projects/{id}
func (c *ProjectsController) updateConfig(w http.ResponseWriter, r *http.Request) {
	stubs.NotImplemented(w, r, stubs.PlannedRoute{
		Route: "PATCH /api/v1/projects/{id}",
		Request: map[string]any{
			"path": map[string]any{"id": "ProjectID"},
			"body": map[string]any{
				"agent":     "string (optional)",
				"runtime":   "string (optional)",
				"tracker":   "TrackerConfig (optional)",
				"scm":       "SCMConfig (optional)",
				"reactions": "map[string]ReactionConfig (optional)",
			},
		},
		Response: map[string]any{
			"200": map[string]any{"project": "domain.Project"},
		},
		Errors: []stubs.PlannedError{
			{Status: 400, Code: "INVALID_JSON", Message: "Invalid JSON body"},
			{
				Status: 400, Code: "IDENTITY_FROZEN",
				Message: "Identity fields cannot be patched",
				Details: map[string]any{"fields": "[]string"},
			},
			{Status: 400, Code: "INVALID_LOCAL_CONFIG", Message: "Local project config failed validation"},
			{Status: 404, Code: "PROJECT_NOT_FOUND", Message: "Unknown project"},
			{Status: 409, Code: "PROJECT_DEGRADED", Message: "Project config is degraded; repair before patching"},
			{Status: 409, Code: "PROJECT_MISSING_PATH", Message: "Project registry entry is missing a path"},
		},
		Notes: "REST-audit R3: legacy PUT alias is NOT registered; PUT /projects/{id} returns 405. R6: returns {project}, not {ok:true}.",
	})
}

// remove — DELETE /api/v1/projects/{id}
func (c *ProjectsController) remove(w http.ResponseWriter, r *http.Request) {
	stubs.NotImplemented(w, r, stubs.PlannedRoute{
		Route: "DELETE /api/v1/projects/{id}",
		Request: map[string]any{
			"path": map[string]any{"id": "ProjectID"},
		},
		Response: map[string]any{
			"200": map[string]any{
				"projectId":         "ProjectID",
				"removedStorageDir": "bool",
			},
		},
		Errors: []stubs.PlannedError{
			{Status: 400, Code: "INVALID_PROJECT_ID", Message: "Project id failed storage-path validation"},
			{Status: 404, Code: "PROJECT_NOT_FOUND", Message: "Unknown project"},
			{Status: 500, Code: "PROJECT_REMOVE_FAILED", Message: "Failed to remove project"},
		},
		Notes: "Side effects (in handler-impl PR): stop project sessions, cleanup managed workspaces, unregister, remove storage dir.",
	})
}

// repair — POST /api/v1/projects/{id}/repair
func (c *ProjectsController) repair(w http.ResponseWriter, r *http.Request) {
	stubs.NotImplemented(w, r, stubs.PlannedRoute{
		Route:  "POST /api/v1/projects/{id}/repair",
		Legacy: []string{"POST /api/v1/projects/{id}"},
		Request: map[string]any{
			"path": map[string]any{"id": "ProjectID"},
		},
		Response: map[string]any{
			"200": map[string]any{"project": "domain.Project"},
		},
		Errors: []stubs.PlannedError{
			{Status: 400, Code: "PROJECT_NOT_DEGRADED", Message: "Project does not need repair"},
			{Status: 400, Code: "REPAIR_NOT_AVAILABLE", Message: "Automatic repair is not available for this degraded config"},
			{Status: 404, Code: "PROJECT_NOT_FOUND", Message: "Unknown project"},
		},
		Notes: "REST-audit R4: replaces the overloaded POST /projects/{id}. Legacy path is NOT registered; consumers must call /repair.",
	})
}

// reload — POST /api/v1/projects/reload
func (c *ProjectsController) reload(w http.ResponseWriter, r *http.Request) {
	stubs.NotImplemented(w, r, stubs.PlannedRoute{
		Route: "POST /api/v1/projects/reload",
		Response: map[string]any{
			"200": map[string]any{
				"reloaded":      "bool",
				"projectCount":  "int",
				"degradedCount": "int",
			},
		},
		Errors: []stubs.PlannedError{
			{Status: 500, Code: "RELOAD_FAILED", Message: "Failed to reload projects"},
		},
		Notes: "Invalidates the cached services registry and re-loads the global config.",
	})
}
