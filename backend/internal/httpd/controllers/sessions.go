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
)

const (
	maxPromptLen  = 4096
	maxMessageLen = 4096
)

// SessionService is the controller-facing session service contract.
type SessionService interface {
	List(ctx context.Context, filter sessionsvc.ListFilter) ([]domain.Session, error)
	Spawn(ctx context.Context, cfg ports.SpawnConfig) (domain.Session, error)
	SpawnOrchestrator(ctx context.Context, projectID domain.ProjectID, clean bool) (domain.Session, error)
	Get(ctx context.Context, id domain.SessionID) (domain.Session, error)
	Restore(ctx context.Context, id domain.SessionID) (domain.Session, error)
	Kill(ctx context.Context, id domain.SessionID) (bool, error)
	RollbackSpawn(ctx context.Context, id domain.SessionID) (sessionsvc.RollbackOutcome, error)
	Cleanup(ctx context.Context, project domain.ProjectID) (sessionsvc.CleanupOutcome, error)
	Rename(ctx context.Context, id domain.SessionID, displayName string) error
	Send(ctx context.Context, id domain.SessionID, message string) error
	ListPRs(ctx context.Context, id domain.SessionID) ([]domain.PRFacts, error)
	ClaimPR(ctx context.Context, id domain.SessionID, ref string, opts sessionsvc.ClaimPROptions) (sessionsvc.ClaimPRResult, error)
}

// ActivityRecorder applies an agent activity-state signal to a session. It is
// satisfied directly by *lifecycle.Manager: an activity signal is a pure
// lifecycle reduction (no runtime/workspace teardown), so it bypasses
// SessionService rather than threading a no-op passthrough through the session
// manager.
type ActivityRecorder interface {
	ApplyActivitySignal(ctx context.Context, id domain.SessionID, s ports.ActivitySignal) error
}

// SessionsController owns the session routes. Nil keeps routes registered but
// returns OpenAPI-backed 501s.
type SessionsController struct {
	Svc      SessionService
	Activity ActivityRecorder
}

// Register mounts the session routes on the supplied router.
func (c *SessionsController) Register(r chi.Router) {
	r.Get("/sessions", c.list)
	r.Post("/sessions", c.spawn)
	r.Post("/sessions/cleanup", c.cleanup)
	r.Get("/sessions/{sessionId}", c.get)
	r.Get("/sessions/{sessionId}/pr", c.listPRs)
	r.Post("/sessions/{sessionId}/pr/claim", c.claimPR)
	r.Patch("/sessions/{sessionId}", c.rename)
	r.Post("/sessions/{sessionId}/restore", c.restore)
	r.Post("/sessions/{sessionId}/kill", c.kill)
	r.Post("/sessions/{sessionId}/rollback", c.rollback)
	r.Post("/sessions/{sessionId}/send", c.send)
	r.Post("/sessions/{sessionId}/activity", c.activity)
	r.Get("/orchestrators", c.listOrchestrators)
	r.Post("/orchestrators", c.spawnOrchestrator)
	r.Get("/orchestrators/{id}", c.getOrchestrator)
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
		envelope.WriteError(w, r, err)
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
	sess, err := c.Svc.Spawn(r.Context(), ports.SpawnConfig{ProjectID: in.ProjectID, IssueID: in.IssueID, Kind: in.Kind, Harness: in.Harness, Branch: in.Branch, Prompt: in.Prompt})
	if err != nil {
		envelope.WriteError(w, r, err)
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
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, SessionResponse{Session: sess})
}

func (c *SessionsController) listPRs(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "GET", "/api/v1/sessions/{sessionId}/pr")
		return
	}
	prs, err := c.Svc.ListPRs(r.Context(), sessionID(r))
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, ListSessionPRsResponse{SessionID: sessionID(r), PRs: sessionPRFacts(prs)})
}

func (c *SessionsController) claimPR(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "POST", "/api/v1/sessions/{sessionId}/pr/claim")
		return
	}
	var in ClaimPRRequest
	if err := decodeJSON(r, &in); err != nil {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_JSON", "Invalid JSON body", nil)
		return
	}
	if strings.TrimSpace(in.PR) == "" {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "PR_REQUIRED", "pr is required", nil)
		return
	}
	allowTakeover := true
	if in.AllowTakeover != nil {
		allowTakeover = *in.AllowTakeover
	}
	res, err := c.Svc.ClaimPR(r.Context(), sessionID(r), in.PR, sessionsvc.ClaimPROptions{AllowTakeover: allowTakeover})
	if err != nil {
		writeSessionPRError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, ClaimPRResponse{OK: true, SessionID: sessionID(r), PRs: sessionPRFacts(res.PRs), BranchChanged: res.BranchChanged, TakenOverFrom: nonNilSessionIDs(res.TakenOverFrom)})
}

func (c *SessionsController) rename(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "PATCH", "/api/v1/sessions/{sessionId}")
		return
	}
	var in RenameSessionRequest
	if err := decodeJSON(r, &in); err != nil {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_JSON", "Invalid JSON body", nil)
		return
	}
	displayName := strings.TrimSpace(in.DisplayName)
	if displayName == "" {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "DISPLAY_NAME_REQUIRED", "displayName is required", nil)
		return
	}
	if err := c.Svc.Rename(r.Context(), sessionID(r), displayName); err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, RenameSessionResponse{OK: true, SessionID: sessionID(r), DisplayName: displayName})
}

func (c *SessionsController) restore(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "POST", "/api/v1/sessions/{sessionId}/restore")
		return
	}
	sess, err := c.Svc.Restore(r.Context(), sessionID(r))
	if err != nil {
		envelope.WriteError(w, r, err)
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
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, KillSessionResponse{OK: true, SessionID: sessionID(r), Freed: freed})
}

// rollback undoes a partially-completed spawn: if the session row is still in
// seed state (no workspace, no runtime handle yet), the row is deleted
// outright. If anything observable has landed it falls back to Kill so the
// runtime/workspace are torn down. Used by `ao spawn --claim-pr` to undo a
// session whose claim step failed, avoiding the orphan terminated row a
// plain Kill would leave behind.
func (c *SessionsController) rollback(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "POST", "/api/v1/sessions/{sessionId}/rollback")
		return
	}
	out, err := c.Svc.RollbackSpawn(r.Context(), sessionID(r))
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, RollbackSessionResponse{OK: true, SessionID: sessionID(r), Deleted: out.Deleted, Killed: out.Killed})
}

func (c *SessionsController) cleanup(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "POST", "/api/v1/sessions/cleanup")
		return
	}
	out, err := c.Svc.Cleanup(r.Context(), domain.ProjectID(r.URL.Query().Get("project")))
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	skipped := make([]CleanupSkippedSession, 0, len(out.Skipped))
	for _, skip := range out.Skipped {
		skipped = append(skipped, CleanupSkippedSession{SessionID: skip.SessionID, Reason: skip.Reason})
	}
	envelope.WriteJSON(w, http.StatusOK, CleanupSessionsResponse{OK: true, Cleaned: out.Cleaned, Skipped: skipped})
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
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, SendSessionMessageResponse{OK: true, SessionID: sessionID(r), Message: message})
}

// activity records an agent activity-state signal reported by an agent hook
// (via `ao hooks <agent> <event>`). It funnels through the single
// lifecycle.Manager so the reaper and hooks never race on the session's
// activity/termination columns.
func (c *SessionsController) activity(w http.ResponseWriter, r *http.Request) {
	if c.Activity == nil {
		apispec.NotImplemented(w, r, "POST", "/api/v1/sessions/{sessionId}/activity")
		return
	}
	var in SetActivityRequest
	if err := decodeJSON(r, &in); err != nil {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_JSON", "Invalid JSON body", nil)
		return
	}
	state := domain.ActivityState(in.State)
	switch state {
	case domain.ActivityActive, domain.ActivityIdle, domain.ActivityWaitingInput, domain.ActivityExited:
	default:
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_ACTIVITY_STATE", "Unknown activity state", nil)
		return
	}
	if err := c.Activity.ApplyActivitySignal(r.Context(), sessionID(r), ports.ActivitySignal{Valid: true, State: state}); err != nil {
		if errors.Is(err, ports.ErrSessionNotFound) {
			envelope.WriteAPIError(w, r, http.StatusNotFound, "not_found", "SESSION_NOT_FOUND", "Unknown session", nil)
			return
		}
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, SetActivityResponse{OK: true, SessionID: sessionID(r), State: in.State})
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
	sess, err := c.Svc.SpawnOrchestrator(r.Context(), in.ProjectID, in.Clean)
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusCreated, SpawnOrchestratorResponse{
		Orchestrator: OrchestratorResponse{ID: sess.ID, ProjectID: sess.ProjectID},
	})
}

func (c *SessionsController) listOrchestrators(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "GET", "/api/v1/orchestrators")
		return
	}
	sessions, err := c.Svc.List(r.Context(), sessionsvc.ListFilter{OrchestratorOnly: true})
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, ListSessionsResponse{Sessions: sessions})
}

func (c *SessionsController) getOrchestrator(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "GET", "/api/v1/orchestrators/{id}")
		return
	}
	sess, err := c.Svc.Get(r.Context(), orchestratorID(r))
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	if sess.Kind != domain.KindOrchestrator {
		envelope.WriteAPIError(w, r, http.StatusNotFound, "not_found", "SESSION_NOT_FOUND", "Unknown session", nil)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, SessionResponse{Session: sess})
}

func sessionID(r *http.Request) domain.SessionID {
	return domain.SessionID(chi.URLParam(r, "sessionId"))
}

func orchestratorID(r *http.Request) domain.SessionID {
	return domain.SessionID(chi.URLParam(r, "id"))
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

func writeSessionPRError(w http.ResponseWriter, r *http.Request, err error) {
	var claimed ports.PRClaimedByActiveSessionError
	switch {
	case errors.Is(err, sessionsvc.ErrInvalidPRRef):
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_PR_REF", "PR reference must be a github.com PR URL or a number", nil)
	case errors.Is(err, sessionsvc.ErrPRNotFound):
		envelope.WriteAPIError(w, r, http.StatusNotFound, "not_found", "PR_NOT_FOUND", "Unknown PR", nil)
	case errors.Is(err, sessionsvc.ErrPRNotOpen):
		envelope.WriteAPIError(w, r, http.StatusConflict, "conflict", "PR_NOT_OPEN", "PR is not open", nil)
	case errors.As(err, &claimed):
		envelope.WriteAPIError(w, r, http.StatusConflict, "conflict", "PR_CLAIMED_BY_ACTIVE_SESSION", "PR is already claimed by active session "+string(claimed.Owner)+" (omit --no-takeover to steal)", map[string]any{"ownerSessionId": string(claimed.Owner)})
	case errors.Is(err, sessionsvc.ErrSessionNotClaimable):
		envelope.WriteAPIError(w, r, http.StatusUnprocessableEntity, "unprocessable", "SESSION_NOT_CLAIMABLE", "Session cannot claim PRs", nil)
	case errors.Is(err, sessionsvc.ErrSessionNoWorkspace):
		envelope.WriteAPIError(w, r, http.StatusUnprocessableEntity, "unprocessable", "SESSION_NO_WORKSPACE", "Session has no workspace", nil)
	case errors.Is(err, sessionsvc.ErrProjectMismatch):
		envelope.WriteAPIError(w, r, http.StatusUnprocessableEntity, "unprocessable", "PR_PROJECT_MISMATCH", "PR does not belong to the session project", nil)
	case errors.Is(err, sessionsvc.ErrSCMUnavailable):
		envelope.WriteAPIError(w, r, http.StatusServiceUnavailable, "unavailable", "SCM_UNAVAILABLE", "SCM unavailable", nil)
	default:
		envelope.WriteError(w, r, err)
	}
}

func sessionPRFacts(prs []domain.PRFacts) []SessionPRFacts {
	out := make([]SessionPRFacts, 0, len(prs))
	for _, pr := range prs {
		out = append(out, SessionPRFacts{URL: pr.URL, Number: pr.Number, State: prState(pr), CI: pr.CI, Review: pr.Review, Mergeability: pr.Mergeability, ReviewComments: pr.ReviewComments, UpdatedAt: pr.UpdatedAt})
	}
	return out
}

func prState(pr domain.PRFacts) string {
	switch {
	case pr.Merged:
		return string(domain.PRStateMerged)
	case pr.Closed:
		return string(domain.PRStateClosed)
	case pr.Draft:
		return string(domain.PRStateDraft)
	default:
		return string(domain.PRStateOpen)
	}
}

func nonNilSessionIDs(ids []domain.SessionID) []domain.SessionID {
	if ids == nil {
		return []domain.SessionID{}
	}
	return ids
}
