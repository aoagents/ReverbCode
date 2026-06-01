package httpd

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apispec"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/controllers"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/httpx"
	"github.com/aoagents/agent-orchestrator/backend/internal/project"
)

// APIDeps bundles one Manager per resource, each defined in its own feature
// package (project.Manager, later session.Manager, ...). While handlers are
// stubs every field is nil; the handler-impl PR wires real Managers.
type APIDeps struct {
	Projects project.Manager
}

// API owns one controller per resource and exposes the single Register call the
// router invokes to mount the /api/v1 surface.
type API struct {
	cfg      config.Config
	projects *controllers.ProjectsController
}

// NewAPI constructs the API surface from its dependencies. cfg carries the
// per-request timeout so the REST group can apply it without re-reading the
// environment.
func NewAPI(cfg config.Config, deps APIDeps) *API {
	return &API{
		cfg: cfg,
		projects: &controllers.ProjectsController{
			Mgr: deps.Projects,
		},
	}
}

// Register mounts the /api/v1 REST surface on root. It serves the OpenAPI
// document at /api/v1/openapi.yaml and wraps every controller route in a
// per-request Timeout group, so the bounded REST handlers are time-limited
// without affecting the health probes that router.go keeps off the global
// stack.
func (a *API) Register(root chi.Router) {
	timeout := a.cfg.RequestTimeout
	if timeout <= 0 {
		timeout = config.DefaultRequestTimeout
	}

	root.Route("/api/v1", func(r chi.Router) {
		// The OpenAPI document is the source of truth for every contract on
		// this surface; serve it so tooling (SDK generators, OpenAPI
		// validators, the dashboard's developer tools) can fetch the whole
		// spec from the same origin as the routes it describes.
		apispec.RegisterServe(r, "/openapi.yaml")

		r.Group(func(r chi.Router) {
			r.Use(middleware.Timeout(timeout))
			a.projects.Register(r)
			// Additional resource controllers register inside this same
			// timeout group.
		})
	})
}

// notFoundJSON returns the locked envelope for unmatched routes. Chi's default
// 404 is a text/plain body; the API surface must answer JSON so consumers can
// parse it uniformly.
func notFoundJSON(w http.ResponseWriter, r *http.Request) {
	httpx.WriteError(w, r, http.StatusNotFound, "not_found", "ROUTE_NOT_FOUND",
		r.Method+" "+r.URL.Path+" has no handler", nil)
}

// methodNotAllowedJSON returns the locked envelope when a method probes a
// known path without a matching verb (e.g. PUT /projects/{id} after we drop
// the legacy PUT alias).
func methodNotAllowedJSON(w http.ResponseWriter, r *http.Request) {
	httpx.WriteError(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "METHOD_NOT_ALLOWED",
		r.Method+" not allowed on "+r.URL.Path, nil)
}
