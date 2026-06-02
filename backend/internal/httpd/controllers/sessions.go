package controllers

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"unicode"

	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apispec"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/envelope"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	sessionsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/session"
	sessionmanager "github.com/aoagents/agent-orchestrator/backend/internal/session_manager"
)

const (
	maxPromptLen  = 4096
	maxMessageLen = 4096
)

// SessionService is the controller-facing session service contract.
type SessionService interface {
	List(ctx context.Context, filter sessionsvc.ListFilter) ([]domain.Session, error)
	Spawn(ctx context.Context, cfg ports.SpawnConfig) (domain.Session, error)
	Get(ctx context.Context, id domain.SessionID) (domain.Session, error)
	Restore(ctx context.Context, id domain.SessionID) (domain.Session, error)
	Kill(ctx context.Context, id domain.SessionID) (bool, error)
	Send(ctx context.Context, id domain.SessionID, message string) error
}

// SessionsController owns the session routes. Nil keeps routes registered but
// returns OpenAPI-backed 501s.
type SessionsController struct {
	Svc SessionService
}

// Register mounts the session routes on the supplied router.
func (c *SessionsController) Register(r chi.Router) {
	r.Get("/sessions", c.list)
	r.Post("/sessions", c.spawn)
	r.Get("/sessions/{sessionId}", c.get)
	r.Patch("/sessions/{sessionId}", c.rename)
	r.Post("/sessions/{sessionId}/restore", c.restore)
	r.Post("/sessions/{sessionId}/kill", c.kill)
	r.Post("/sessions/{sessionId}/send", c.send)
	r.Post("/orchestrators", c.spawnOrchestrator)
}

func (c *SessionsController) list(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "GET", "/api/v1/sessions")
		return
	}
	filter, err := parseSessionListFilter(r)
	if err != nil {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_QUERY", err.Error(), nil)
		return
	}
	sessions, err := c.Svc.List(r.Context(), filter)
	if err != nil {
		writeSessionError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, ListSessionsResponse{Sessions: sessions})
}

func (c *SessionsController) spawn(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "POST", "/api/v1/sessions")
		return
	}
	var in SpawnSessionRequest
	if err := decodeJSON(r, &in); err != nil {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_JSON", "Invalid JSON body", nil)
		return
	}
	if in.ProjectID == "" {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "PROJECT_ID_REQUIRED", "projectId is required", nil)
		return
	}
	if len(in.Prompt) > maxPromptLen {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "PROMPT_TOO_LONG", "prompt is too long", nil)
		return
	}
	if in.Kind == "" {
		in.Kind = domain.KindWorker
	}
	sess, err := c.Svc.Spawn(r.Context(), ports.SpawnConfig{ProjectID: in.ProjectID, IssueID: in.IssueID, Kind: in.Kind, Harness: in.Harness, Branch: in.Branch, Prompt: in.Prompt, AgentRules: in.AgentRules})
	if err != nil {
		writeSessionError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusCreated, SessionResponse{Session: sess})
}

func (c *SessionsController) get(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "GET", "/api/v1/sessions/{sessionId}")
		return
	}
	sess, err := c.Svc.Get(r.Context(), sessionID(r))
	if err != nil {
		writeSessionError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, SessionResponse{Session: sess})
}

func (c *SessionsController) rename(w http.ResponseWriter, r *http.Request) {
	apispec.NotImplemented(w, r, "PATCH", "/api/v1/sessions/{sessionId}")
}

func (c *SessionsController) restore(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "POST", "/api/v1/sessions/{sessionId}/restore")
		return
	}
	sess, err := c.Svc.Restore(r.Context(), sessionID(r))
	if err != nil {
		writeSessionError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, RestoreSessionResponse{OK: true, SessionID: sessionID(r), Session: sess})
}

func (c *SessionsController) kill(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "POST", "/api/v1/sessions/{sessionId}/kill")
		return
	}
	freed, err := c.Svc.Kill(r.Context(), sessionID(r))
	if err != nil {
		writeSessionError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, KillSessionResponse{OK: true, SessionID: sessionID(r), Freed: freed})
}

func (c *SessionsController) send(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "POST", "/api/v1/sessions/{sessionId}/send")
		return
	}
	var in SendSessionMessageRequest
	if err := decodeJSON(r, &in); err != nil {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_JSON", "Invalid JSON body", nil)
		return
	}
	if in.Message == "" {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "MESSAGE_REQUIRED", "Message is required", nil)
		return
	}
	if len(in.Message) > maxMessageLen {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "MESSAGE_TOO_LONG", "Message is too long", nil)
		return
	}
	message := stripUnsafeControlChars(in.Message)
	if err := c.Svc.Send(r.Context(), sessionID(r), message); err != nil {
		writeSessionError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, SendSessionMessageResponse{OK: true, SessionID: sessionID(r), Message: message})
}

func (c *SessionsController) spawnOrchestrator(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "POST", "/api/v1/orchestrators")
		return
	}
	var in SpawnOrchestratorRequest
	if err := decodeJSON(r, &in); err != nil {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_JSON", "Invalid JSON body", nil)
		return
	}
	if in.ProjectID == "" {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "PROJECT_ID_REQUIRED", "projectId is required", nil)
		return
	}
	if in.Clean {
		active := true
		orchestrators, err := c.Svc.List(r.Context(), sessionsvc.ListFilter{ProjectID: in.ProjectID, Active: &active, OrchestratorOnly: true})
		if err != nil {
			writeSessionError(w, r, err)
			return
		}
		for _, existing := range orchestrators {
			if _, err := c.Svc.Kill(r.Context(), existing.ID); err != nil {
				writeSessionError(w, r, err)
				return
			}
		}
	}
	sess, err := c.Svc.Spawn(r.Context(), ports.SpawnConfig{ProjectID: in.ProjectID, Kind: domain.KindOrchestrator})
	if err != nil {
		writeSessionError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusCreated, SpawnOrchestratorResponse{
		Orchestrator: OrchestratorResponse{ID: sess.ID, ProjectID: sess.ProjectID},
	})
}

func sessionID(r *http.Request) domain.SessionID {
	return domain.SessionID(chi.URLParam(r, "sessionId"))
}

func parseSessionListFilter(r *http.Request) (sessionsvc.ListFilter, error) {
	q := r.URL.Query()
	filter := sessionsvc.ListFilter{ProjectID: domain.ProjectID(q.Get("project"))}
	if raw := q.Get("active"); raw != "" {
		active, err := strconv.ParseBool(raw)
		if err != nil {
			return sessionsvc.ListFilter{}, errors.New("active must be a boolean")
		}
		filter.Active = &active
	}
	if raw := q.Get("orchestratorOnly"); raw != "" {
		orchestratorOnly, err := strconv.ParseBool(raw)
		if err != nil {
			return sessionsvc.ListFilter{}, errors.New("orchestratorOnly must be a boolean")
		}
		filter.OrchestratorOnly = orchestratorOnly
	}
	if raw := q.Get("fresh"); raw != "" {
		fresh, err := strconv.ParseBool(raw)
		if err != nil {
			return sessionsvc.ListFilter{}, errors.New("fresh must be a boolean")
		}
		filter.Fresh = fresh
	}
	return filter, nil
}

func stripUnsafeControlChars(message string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) && r != '\n' && r != '\r' && r != '\t' {
			return -1
		}
		return r
	}, message)
}

func writeSessionError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, sessionmanager.ErrNotFound):
		envelope.WriteAPIError(w, r, http.StatusNotFound, "not_found", "SESSION_NOT_FOUND", "Unknown session", nil)
	case errors.Is(err, sessionmanager.ErrNotRestorable):
		envelope.WriteAPIError(w, r, http.StatusConflict, "conflict", "SESSION_NOT_RESTORABLE", "Session is not restorable", nil)
	case errors.Is(err, sessionmanager.ErrTerminated):
		envelope.WriteAPIError(w, r, http.StatusConflict, "conflict", "SESSION_TERMINATED", "Session is terminated", nil)
	case errors.Is(err, sessionmanager.ErrIncompleteHandle):
		envelope.WriteAPIError(w, r, http.StatusConflict, "conflict", "SESSION_INCOMPLETE_HANDLE", "Session is missing runtime or workspace handles", nil)
	case errors.Is(err, sessionmanager.ErrProjectNotResolvable):
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "PROJECT_NOT_RESOLVABLE", "Project is not registered or has no repo — register it with `ao project add`", nil)
	default:
		envelope.WriteAPIError(w, r, http.StatusInternalServerError, "internal", "SESSION_OPERATION_FAILED", "Session operation failed", nil)
	}
}
