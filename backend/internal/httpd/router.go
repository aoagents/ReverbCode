// Package httpd builds and runs the daemon's HTTP surface. Phase 1a is the
// skeleton: the middleware stack, liveness/readiness probes, and a graceful
// run loop. Route registration (/api/v1, /events, /mux, /) lands in later
// phases on top of the router this package builds.
package httpd

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/aoagents/agent-orchestrator/backend/internal/config"
)

// NewRouter builds the root router with the standard middleware stack and the
// health probes mounted.
//
// Middleware order (outermost first):
//
//	Recoverer  → turn a handler panic into 500 instead of crashing the daemon
//	RequestID  → attach a request id for correlation
//	Logger     → structured access log, carrying the request id
//	RealIP     → normalise client IP (loopback proxy from the dev server)
//
// The per-request Timeout from the decision table is deliberately NOT applied
// globally: it must wrap only the /api/v1 REST surface, never the long-lived
// SSE (/events) or WebSocket (/mux) surfaces, nor the always-must-answer health
// probes. It is therefore applied per-surface when those subrouters are mounted
// in Phase 1b; cfg.RequestTimeout carries the value through to that point.
func NewRouter(cfg config.Config) chi.Router {
	r := chi.NewRouter()

	r.Use(middleware.Recoverer)
	r.Use(middleware.RequestID)
	r.Use(middleware.Logger)
	r.Use(middleware.RealIP)

	mountHealth(r)

	return r
}

// mountHealth registers the liveness and readiness probes the Electron
// supervisor polls before letting the renderer connect.
func mountHealth(r chi.Router) {
	r.Get("/healthz", handleHealthz)
	r.Get("/readyz", handleReadyz)
}

// handleHealthz is the liveness probe: it answers 200 as long as the process is
// up and serving. It does no dependency checks by design.
func handleHealthz(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// handleReadyz is the readiness probe. In the 1a skeleton the daemon is ready
// as soon as it is listening; later phases will gate this on dependency
// initialisation (e.g. store/event-bus warm-up).
func handleReadyz(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
}
