package controllers_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
)

type fakeSettingsService struct {
	defaults domain.AgentDefaults
	err      error
	saved    domain.AgentDefaults
}

func (f *fakeSettingsService) GetAgentDefaults(context.Context) (domain.AgentDefaults, error) {
	return f.defaults, f.err
}

func (f *fakeSettingsService) SetAgentDefaults(_ context.Context, defaults domain.AgentDefaults) (domain.AgentDefaults, error) {
	if f.err != nil {
		return domain.AgentDefaults{}, f.err
	}
	f.saved = defaults
	f.defaults = defaults
	return defaults, nil
}

func newSettingsTestServer(t *testing.T, svc *fakeSettingsService) *httptest.Server {
	t.Helper()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := httptest.NewServer(httpd.NewRouterWithControl(config.Config{}, log, nil, httpd.APIDeps{Settings: svc}, httpd.ControlDeps{}))
	t.Cleanup(srv.Close)
	return srv
}

func TestSettingsAPI_AgentDefaultsRoundTrip(t *testing.T) {
	svc := &fakeSettingsService{}
	srv := newSettingsTestServer(t, svc)

	body, status, _ := doRequest(t, srv, "GET", "/api/v1/settings/agents", "")
	if status != http.StatusOK {
		t.Fatalf("GET settings = %d, want 200; body=%s", status, body)
	}
	var got struct {
		DefaultWorkerAgent       string `json:"defaultWorkerAgent"`
		DefaultOrchestratorAgent string `json:"defaultOrchestratorAgent"`
		Configured               bool   `json:"configured"`
	}
	mustJSON(t, body, &got)
	if got.Configured || got.DefaultWorkerAgent != "" || got.DefaultOrchestratorAgent != "" {
		t.Fatalf("first-run settings = %#v, want empty incomplete defaults", got)
	}

	body, status, _ = doRequest(t, srv, "PUT", "/api/v1/settings/agents", `{"defaultWorkerAgent":"codex","defaultOrchestratorAgent":"claude-code"}`)
	if status != http.StatusOK {
		t.Fatalf("PUT settings = %d, want 200; body=%s", status, body)
	}
	mustJSON(t, body, &got)
	if !got.Configured || got.DefaultWorkerAgent != "codex" || got.DefaultOrchestratorAgent != "claude-code" {
		t.Fatalf("saved settings = %#v", got)
	}
	if svc.saved.DefaultWorkerAgent != domain.HarnessCodex || svc.saved.DefaultOrchestratorAgent != domain.HarnessClaudeCode {
		t.Fatalf("service saved = %+v", svc.saved)
	}
}

func TestSettingsAPI_ValidationError(t *testing.T) {
	srv := newSettingsTestServer(t, &fakeSettingsService{err: apierr.Invalid("INVALID_AGENT_DEFAULTS", "defaultOrchestratorAgent is required", nil)})

	body, status, _ := doRequest(t, srv, "PUT", "/api/v1/settings/agents", `{"defaultWorkerAgent":"codex"}`)
	assertErrorCode(t, body, status, http.StatusBadRequest, "INVALID_AGENT_DEFAULTS")
}
