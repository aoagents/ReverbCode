package controllers

import (
	"context"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apispec"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/envelope"
)

// SettingsService is the controller-facing app settings contract.
type SettingsService interface {
	GetAgentDefaults(ctx context.Context) (domain.AgentDefaults, error)
	SetAgentDefaults(ctx context.Context, defaults domain.AgentDefaults) (domain.AgentDefaults, error)
}

// SettingsController owns app-wide user settings routes. Nil keeps routes
// registered but returns OpenAPI-backed 501s.
type SettingsController struct {
	Svc SettingsService
}

// Register mounts the settings routes on the supplied router.
func (c *SettingsController) Register(r chi.Router) {
	r.Get("/settings/agents", c.getAgentDefaults)
	r.Put("/settings/agents", c.setAgentDefaults)
}

func (c *SettingsController) getAgentDefaults(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "GET", "/api/v1/settings/agents")
		return
	}
	defaults, err := c.Svc.GetAgentDefaults(r.Context())
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, AgentDefaultsResponse{
		AgentDefaults: defaults,
		Configured:    defaults.Complete(),
	})
}

func (c *SettingsController) setAgentDefaults(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "PUT", "/api/v1/settings/agents")
		return
	}
	var in AgentDefaultsRequest
	if err := decodeJSONStrict(r, &in); err != nil {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_JSON", "Invalid JSON body", nil)
		return
	}
	defaults, err := c.Svc.SetAgentDefaults(r.Context(), in.AgentDefaults)
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, AgentDefaultsResponse{
		AgentDefaults: defaults,
		Configured:    defaults.Complete(),
	})
}
