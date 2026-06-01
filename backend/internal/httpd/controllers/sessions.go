package controllers

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/envelope"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/project"
	"github.com/aoagents/agent-orchestrator/backend/internal/session"
)

// SessionsController owns the /sessions routes. Mgr nil means the Session
// Manager has not been wired into the daemon yet; the controller answers 503
// "sessions_disabled" so the CLI gets an actionable signal instead of a panic.
type SessionsController struct {
	Mgr session.Spawner
}

// Register mounts the sessions routes on the supplied router.
func (c *SessionsController) Register(r chi.Router) {
	r.Post("/sessions", c.spawn)
}

type spawnRequest struct {
	ProjectID string `json:"projectId"`
	Prompt    string `json:"prompt"`
	Agent     string `json:"agent,omitempty"`
}

type spawnResponse struct {
	SessionID     string `json:"sessionId"`
	WorkspacePath string `json:"workspacePath"`
	RuntimeHandle string `json:"runtimeHandle"`
}

func (c *SessionsController) spawn(w http.ResponseWriter, r *http.Request) {
	if c.Mgr == nil {
		envelope.WriteJSON(w, http.StatusServiceUnavailable, map[string]any{
			"error":   "sessions_disabled",
			"code":    "SESSIONS_DISABLED",
			"message": "Session Manager is not wired in this daemon",
		})
		return
	}

	var in spawnRequest
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_JSON", "Invalid JSON body", nil)
		return
	}
	projectID := strings.TrimSpace(in.ProjectID)
	prompt := strings.TrimSpace(in.Prompt)
	if projectID == "" {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "PROJECT_ID_REQUIRED", "projectId is required", nil)
		return
	}
	if prompt == "" {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "PROMPT_REQUIRED", "prompt is required", nil)
		return
	}

	harness := domain.AgentHarness(strings.TrimSpace(in.Agent))
	if harness == "" {
		harness = domain.HarnessClaudeCode
	}

	sess, err := c.Mgr.Spawn(r.Context(), ports.SpawnConfig{
		ProjectID: domain.ProjectID(projectID),
		Kind:      domain.KindWorker,
		Harness:   harness,
		Prompt:    prompt,
	})
	if err != nil {
		writeSpawnError(w, r, err)
		return
	}

	envelope.WriteJSON(w, http.StatusCreated, spawnResponse{
		SessionID:     string(sess.ID),
		WorkspacePath: sess.Metadata.WorkspacePath,
		RuntimeHandle: sess.Metadata.RuntimeHandleID,
	})
}

// writeSpawnError maps an SM-returned error to the right HTTP status. A
// project.Error in the chain (most commonly "unknown project" from the
// projectresolver) becomes 404; anything else surfaces as 500 SPAWN_FAILED.
func writeSpawnError(w http.ResponseWriter, r *http.Request, err error) {
	var pe *project.Error
	if errors.As(err, &pe) {
		status := http.StatusInternalServerError
		switch pe.Kind {
		case "bad_request":
			status = http.StatusBadRequest
		case "not_found":
			status = http.StatusNotFound
		case "conflict":
			status = http.StatusConflict
		}
		envelope.WriteAPIError(w, r, status, pe.Kind, pe.Code, pe.Message, pe.Details)
		return
	}
	envelope.WriteAPIError(w, r, http.StatusInternalServerError, "internal", "SPAWN_FAILED", "Failed to spawn session", nil)
}
