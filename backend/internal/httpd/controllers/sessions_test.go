package controllers_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	sessionsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/session"
)

type fakeSessionService struct {
	sessions        map[domain.SessionID]domain.Session
	listFilters     []sessionsvc.ListFilter
	sent            string
	cleanupProjects []domain.ProjectID
	cleanupResult   []domain.SessionID
	cleanupSkipped  []sessionsvc.CleanupSkipped
	claimErr        error
	listPRErr       error
	gitStatus       ports.GitStatus
	gitErr          error
	gitErrResult    ports.GitCommitResult // result returned alongside gitErr (push-leg failures carry a SHA)
	gitStaged       []domain.SessionID
	gitDiscarded    []domain.SessionID
	gitCommits      []string
	gitPushes       []bool
}

func newFakeSessionService() *fakeSessionService {
	now := time.Now().UTC()
	s := domain.Session{SessionRecord: domain.SessionRecord{ID: "ao-1", ProjectID: "ao", Kind: domain.KindWorker, Activity: domain.Activity{State: domain.ActivityIdle, LastActivityAt: now}, CreatedAt: now, UpdatedAt: now}, Status: domain.StatusIdle, TerminalHandleID: "ao-1/terminal_0"}
	return &fakeSessionService{sessions: map[domain.SessionID]domain.Session{s.ID: s}}
}

func (f *fakeSessionService) List(_ context.Context, filter sessionsvc.ListFilter) ([]domain.Session, error) {
	f.listFilters = append(f.listFilters, filter)
	var out []domain.Session
	for _, s := range f.sessions {
		if filter.ProjectID != "" && s.ProjectID != filter.ProjectID {
			continue
		}
		if filter.Active != nil && s.IsTerminated == *filter.Active {
			continue
		}
		if filter.OrchestratorOnly && s.Kind != domain.KindOrchestrator {
			continue
		}
		out = append(out, s)
	}
	return out, nil
}

func (f *fakeSessionService) Spawn(_ context.Context, cfg ports.SpawnConfig) (domain.Session, error) {
	now := time.Now().UTC()
	s := domain.Session{SessionRecord: domain.SessionRecord{ID: domain.SessionID(string(cfg.ProjectID) + "-2"), ProjectID: cfg.ProjectID, IssueID: cfg.IssueID, Kind: cfg.Kind, Harness: cfg.Harness, Activity: domain.Activity{State: domain.ActivityIdle, LastActivityAt: now}, CreatedAt: now, UpdatedAt: now}, Status: domain.StatusIdle}
	f.sessions[s.ID] = s
	return s, nil
}

func (f *fakeSessionService) SpawnOrchestrator(ctx context.Context, projectID domain.ProjectID, clean bool) (domain.Session, error) {
	if clean {
		active := true
		existing, err := f.List(ctx, sessionsvc.ListFilter{ProjectID: projectID, Active: &active, OrchestratorOnly: true})
		if err != nil {
			return domain.Session{}, err
		}
		for _, o := range existing {
			if _, err := f.Kill(ctx, o.ID); err != nil {
				return domain.Session{}, err
			}
		}
	}
	return f.Spawn(ctx, ports.SpawnConfig{ProjectID: projectID, Kind: domain.KindOrchestrator})
}

func (f *fakeSessionService) Get(_ context.Context, id domain.SessionID) (domain.Session, error) {
	return f.sessions[id], nil
}

func (f *fakeSessionService) Restore(_ context.Context, id domain.SessionID) (domain.Session, error) {
	s := f.sessions[id]
	s.IsTerminated = false
	s.IsArchived = false
	s.Status = domain.StatusIdle
	f.sessions[id] = s
	return s, nil
}

func (f *fakeSessionService) Archive(_ context.Context, id domain.SessionID) error {
	s, ok := f.sessions[id]
	if !ok {
		return apierr.NotFound("SESSION_NOT_FOUND", "Unknown session")
	}
	if !s.IsTerminated {
		return apierr.Conflict("SESSION_NOT_TERMINATED", "Kill the worker before archiving it", nil)
	}
	s.IsArchived = true
	f.sessions[id] = s
	return nil
}

func (f *fakeSessionService) Unarchive(_ context.Context, id domain.SessionID) error {
	s, ok := f.sessions[id]
	if !ok {
		return apierr.NotFound("SESSION_NOT_FOUND", "Unknown session")
	}
	s.IsArchived = false
	f.sessions[id] = s
	return nil
}

func (f *fakeSessionService) Kill(_ context.Context, id domain.SessionID) (bool, error) {
	s := f.sessions[id]
	s.IsTerminated = true
	s.Status = domain.StatusTerminated
	f.sessions[id] = s
	return true, nil
}

func (f *fakeSessionService) RollbackSpawn(_ context.Context, id domain.SessionID) (sessionsvc.RollbackOutcome, error) {
	if _, ok := f.sessions[id]; ok {
		delete(f.sessions, id)
		return sessionsvc.RollbackOutcome{Deleted: true}, nil
	}
	return sessionsvc.RollbackOutcome{}, nil
}

func (f *fakeSessionService) Cleanup(_ context.Context, project domain.ProjectID) (sessionsvc.CleanupOutcome, error) {
	f.cleanupProjects = append(f.cleanupProjects, project)
	cleaned := f.cleanupResult
	if cleaned == nil {
		cleaned = []domain.SessionID{"ao-1"}
	}
	return sessionsvc.CleanupOutcome{Cleaned: cleaned, Skipped: f.cleanupSkipped}, nil
}

func (f *fakeSessionService) Rename(_ context.Context, id domain.SessionID, displayName string) error {
	s, ok := f.sessions[id]
	if !ok {
		return apierr.NotFound("SESSION_NOT_FOUND", "Unknown session")
	}
	s.DisplayName = displayName
	f.sessions[id] = s
	return nil
}

func (f *fakeSessionService) Send(_ context.Context, _ domain.SessionID, message string) error {
	f.sent = message
	return nil
}

func (f *fakeSessionService) ListPRs(_ context.Context, id domain.SessionID) ([]domain.PRFacts, error) {
	if f.listPRErr != nil {
		return nil, f.listPRErr
	}
	if _, ok := f.sessions[id]; !ok {
		return nil, apierr.NotFound("SESSION_NOT_FOUND", "Unknown session")
	}
	return []domain.PRFacts{{URL: "https://github.com/aoagents/agent-orchestrator/pull/142", Number: 142, CI: domain.CIPassing, Review: domain.ReviewRequired, Mergeability: domain.MergeMergeable, UpdatedAt: time.Date(2026, 6, 4, 12, 0, 0, 0, time.UTC)}}, nil
}

func (f *fakeSessionService) ClaimPR(_ context.Context, id domain.SessionID, ref string, opts sessionsvc.ClaimPROptions) (sessionsvc.ClaimPRResult, error) {
	if f.claimErr != nil {
		return sessionsvc.ClaimPRResult{}, f.claimErr
	}
	if _, ok := f.sessions[id]; !ok {
		return sessionsvc.ClaimPRResult{}, apierr.NotFound("SESSION_NOT_FOUND", "Unknown session")
	}
	prs, _ := f.ListPRs(context.Background(), id)
	return sessionsvc.ClaimPRResult{PRs: prs, TakenOverFrom: []domain.SessionID{}, BranchChanged: true}, nil
}

func (f *fakeSessionService) GitStatus(_ context.Context, id domain.SessionID) (ports.GitStatus, error) {
	if f.gitErr != nil {
		return ports.GitStatus{}, f.gitErr
	}
	if _, ok := f.sessions[id]; !ok {
		return ports.GitStatus{}, apierr.NotFound("SESSION_NOT_FOUND", "Unknown session")
	}
	return f.gitStatus, nil
}

func (f *fakeSessionService) GitStageAll(_ context.Context, id domain.SessionID) error {
	if f.gitErr != nil {
		return f.gitErr
	}
	f.gitStaged = append(f.gitStaged, id)
	return nil
}

func (f *fakeSessionService) GitDiscardAll(_ context.Context, id domain.SessionID) error {
	if f.gitErr != nil {
		return f.gitErr
	}
	f.gitDiscarded = append(f.gitDiscarded, id)
	return nil
}

func (f *fakeSessionService) GitCommitAll(_ context.Context, id domain.SessionID, message string, push bool) (ports.GitCommitResult, error) {
	if f.gitErr != nil {
		// gitErrResult is zero for genuine pre-commit failures and carries a SHA
		// for push-leg failures, mirroring CommitAll's real contract.
		return f.gitErrResult, f.gitErr
	}
	if _, ok := f.sessions[id]; !ok {
		return ports.GitCommitResult{}, apierr.NotFound("SESSION_NOT_FOUND", "Unknown session")
	}
	f.gitCommits = append(f.gitCommits, message)
	f.gitPushes = append(f.gitPushes, push)
	return ports.GitCommitResult{SHA: "abc1234", Branch: "ao/ao-1", Pushed: push}, nil
}

func newSessionTestServer(t *testing.T, svc *fakeSessionService) *httptest.Server {
	t.Helper()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := httptest.NewServer(httpd.NewRouterWithControl(config.Config{}, log, nil, httpd.APIDeps{Sessions: svc}, httpd.ControlDeps{}))
	t.Cleanup(srv.Close)
	return srv
}

func TestSessionsRoutes_DefaultToStubsWithoutService(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := httptest.NewServer(httpd.NewRouterWithControl(config.Config{}, log, nil, httpd.APIDeps{}, httpd.ControlDeps{}))
	t.Cleanup(srv.Close)

	body, status, headers := doRequest(t, srv, "GET", "/api/v1/sessions", "")
	assertJSON(t, headers)
	assertErrorCode(t, body, status, http.StatusNotImplemented, "NOT_IMPLEMENTED")
}

func TestSessionsAPI_ListSpawnGetAndActions(t *testing.T) {
	svc := newFakeSessionService()
	srv := newSessionTestServer(t, svc)

	body, status, _ := doRequest(t, srv, "GET", "/api/v1/sessions?project=ao", "")
	if status != http.StatusOK {
		t.Fatalf("GET sessions = %d, want 200; body=%s", status, body)
	}
	var list struct {
		Sessions []sessionBody `json:"sessions"`
	}
	mustJSON(t, body, &list)
	if len(list.Sessions) != 1 || list.Sessions[0].ID != "ao-1" || list.Sessions[0].Status != string(domain.StatusIdle) || list.Sessions[0].TerminalHandleID != "ao-1/terminal_0" {
		t.Fatalf("list = %#v", list)
	}

	body, status, _ = doRequest(t, srv, "POST", "/api/v1/sessions", `{"projectId":"ao","issueId":"ISS-1","kind":"worker","harness":"codex","prompt":"fix"}`)
	if status != http.StatusCreated {
		t.Fatalf("POST session = %d, want 201; body=%s", status, body)
	}
	var spawned struct {
		Session sessionBody `json:"session"`
	}
	mustJSON(t, body, &spawned)
	if spawned.Session.ID != "ao-2" || spawned.Session.IssueID != "ISS-1" || spawned.Session.Harness != "codex" {
		t.Fatalf("spawned = %#v", spawned)
	}

	body, status, _ = doRequest(t, srv, "GET", "/api/v1/sessions/ao-2", "")
	if status != http.StatusOK {
		t.Fatalf("GET session = %d, want 200; body=%s", status, body)
	}

	body, status, _ = doRequest(t, srv, "POST", "/api/v1/sessions/ao-2/send", "{\"message\":\"con\\u0000tinue\"}")
	if status != http.StatusOK || svc.sent != "continue" {
		t.Fatalf("send status=%d sent=%q body=%s", status, svc.sent, body)
	}

	body, status, _ = doRequest(t, srv, "POST", "/api/v1/sessions/ao-2/kill", "")
	if status != http.StatusOK {
		t.Fatalf("kill = %d, want 200; body=%s", status, body)
	}
	var killed struct {
		SessionID string `json:"sessionId"`
		Freed     bool   `json:"freed"`
	}
	mustJSON(t, body, &killed)
	if killed.SessionID != "ao-2" || !killed.Freed {
		t.Fatalf("kill response = %#v", killed)
	}

	body, status, _ = doRequest(t, srv, "POST", "/api/v1/sessions/ao-2/restore", "")
	if status != http.StatusOK {
		t.Fatalf("restore = %d, want 200; body=%s", status, body)
	}

	body, status, _ = doRequest(t, srv, "PATCH", "/api/v1/sessions/ao-2", `{"displayName":"Renamed"}`)
	if status != http.StatusOK {
		t.Fatalf("rename = %d, want 200; body=%s", status, body)
	}
	var renamed struct {
		OK          bool   `json:"ok"`
		SessionID   string `json:"sessionId"`
		DisplayName string `json:"displayName"`
	}
	mustJSON(t, body, &renamed)
	if !renamed.OK || renamed.SessionID != "ao-2" || renamed.DisplayName != "Renamed" {
		t.Fatalf("rename response = %#v", renamed)
	}
	if svc.sessions["ao-2"].DisplayName != "Renamed" {
		t.Fatalf("session displayName not updated: %+v", svc.sessions["ao-2"])
	}

	body, status, _ = doRequest(t, srv, "POST", "/api/v1/orchestrators", `{"projectId":"ao"}`)
	if status != http.StatusCreated {
		t.Fatalf("orchestrator = %d, want 201; body=%s", status, body)
	}
}

func TestSessionsAPI_RenameNotFound(t *testing.T) {
	srv := newSessionTestServer(t, newFakeSessionService())

	body, status, _ := doRequest(t, srv, "PATCH", "/api/v1/sessions/missing-1", `{"displayName":"Renamed"}`)
	assertErrorCode(t, body, status, http.StatusNotFound, "SESSION_NOT_FOUND")
}

func TestSessionsAPI_RenameValidation(t *testing.T) {
	srv := newSessionTestServer(t, newFakeSessionService())

	body, status, _ := doRequest(t, srv, "PATCH", "/api/v1/sessions/ao-1", `{"displayName":"  "}`)
	assertErrorCode(t, body, status, http.StatusBadRequest, "DISPLAY_NAME_REQUIRED")

	body, status, _ = doRequest(t, srv, "PATCH", "/api/v1/sessions/ao-1", `{`)
	assertErrorCode(t, body, status, http.StatusBadRequest, "INVALID_JSON")
}

func TestSessionsAPI_ListOrchestratorsOnly(t *testing.T) {
	svc := newFakeSessionService()
	now := time.Now().UTC()
	svc.sessions["ao-orch"] = domain.Session{
		SessionRecord: domain.SessionRecord{
			ID:        "ao-orch",
			ProjectID: "ao",
			Kind:      domain.KindOrchestrator,
			Activity:  domain.Activity{State: domain.ActivityIdle, LastActivityAt: now},
			CreatedAt: now,
			UpdatedAt: now,
		},
		Status: domain.StatusIdle,
	}
	svc.sessions["other-orch"] = domain.Session{
		SessionRecord: domain.SessionRecord{
			ID:        "other-orch",
			ProjectID: "other",
			Kind:      domain.KindOrchestrator,
			Activity:  domain.Activity{State: domain.ActivityIdle, LastActivityAt: now},
			CreatedAt: now,
			UpdatedAt: now,
		},
		Status: domain.StatusIdle,
	}
	srv := newSessionTestServer(t, svc)

	body, status, _ := doRequest(t, srv, "GET", "/api/v1/orchestrators", "")
	if status != http.StatusOK {
		t.Fatalf("GET orchestrators = %d, want 200; body=%s", status, body)
	}
	var list struct {
		Sessions []sessionBody `json:"sessions"`
	}
	mustJSON(t, body, &list)
	if len(list.Sessions) != 2 {
		t.Fatalf("len(orchestrators) = %d, want 2; body=%s", len(list.Sessions), body)
	}
	got := map[string]string{}
	for _, sess := range list.Sessions {
		got[sess.ID] = sess.Kind
	}
	if got["ao-orch"] != string(domain.KindOrchestrator) || got["other-orch"] != string(domain.KindOrchestrator) {
		t.Fatalf("missing orchestrators: %#v", got)
	}
	if _, ok := got["ao-1"]; ok {
		t.Fatalf("worker session leaked into orchestrator list: %#v", got)
	}
}

func TestSessionsAPI_SendValidation(t *testing.T) {
	srv := newSessionTestServer(t, newFakeSessionService())

	body, status, _ := doRequest(t, srv, "POST", "/api/v1/sessions/ao-1/send", `{"message":""}`)
	assertErrorCode(t, body, status, http.StatusBadRequest, "MESSAGE_REQUIRED")
}

func TestSessionsAPI_ArchiveLifecycle(t *testing.T) {
	svc := newFakeSessionService()
	srv := newSessionTestServer(t, svc)

	// Archiving a running session conflicts — kill first.
	body, status, _ := doRequest(t, srv, "POST", "/api/v1/sessions/ao-1/archive", "")
	assertErrorCode(t, body, status, http.StatusConflict, "SESSION_NOT_TERMINATED")

	if body, status, _ = doRequest(t, srv, "POST", "/api/v1/sessions/ao-1/kill", ""); status != http.StatusOK {
		t.Fatalf("kill = %d, want 200; body=%s", status, body)
	}
	body, status, _ = doRequest(t, srv, "POST", "/api/v1/sessions/ao-1/archive", "")
	if status != http.StatusOK {
		t.Fatalf("archive = %d, want 200; body=%s", status, body)
	}
	var archived struct {
		OK        bool   `json:"ok"`
		SessionID string `json:"sessionId"`
	}
	mustJSON(t, body, &archived)
	if !archived.OK || archived.SessionID != "ao-1" || !svc.sessions["ao-1"].IsArchived {
		t.Fatalf("archive response = %#v, session = %+v", archived, svc.sessions["ao-1"])
	}

	body, status, _ = doRequest(t, srv, "POST", "/api/v1/sessions/ao-1/unarchive", "")
	if status != http.StatusOK || svc.sessions["ao-1"].IsArchived {
		t.Fatalf("unarchive = %d body=%s session=%+v", status, body, svc.sessions["ao-1"])
	}

	body, status, _ = doRequest(t, srv, "POST", "/api/v1/sessions/missing-1/archive", "")
	assertErrorCode(t, body, status, http.StatusNotFound, "SESSION_NOT_FOUND")
}

func TestSessionsAPI_ListArchivedFilter(t *testing.T) {
	svc := newFakeSessionService()
	srv := newSessionTestServer(t, svc)

	body, status, _ := doRequest(t, srv, "GET", "/api/v1/sessions?archived=true", "")
	if status != http.StatusOK {
		t.Fatalf("GET sessions = %d, want 200; body=%s", status, body)
	}
	last := svc.listFilters[len(svc.listFilters)-1]
	if last.Archived == nil || !*last.Archived {
		t.Fatalf("archived filter not parsed: %#v", last)
	}

	body, status, _ = doRequest(t, srv, "GET", "/api/v1/sessions?archived=banana", "")
	assertErrorCode(t, body, status, http.StatusBadRequest, "INVALID_QUERY")
}

func TestSessionsAPI_CleanupWithProjectFilter(t *testing.T) {
	svc := newFakeSessionService()
	svc.cleanupResult = []domain.SessionID{"ao-1"}
	svc.cleanupSkipped = []sessionsvc.CleanupSkipped{{SessionID: "ao-2", Reason: "workspace has uncommitted changes"}}
	srv := newSessionTestServer(t, svc)

	body, status, _ := doRequest(t, srv, "POST", "/api/v1/sessions/cleanup?project=ao", "")
	if status != http.StatusOK {
		t.Fatalf("cleanup = %d, want 200; body=%s", status, body)
	}
	var got struct {
		OK      bool     `json:"ok"`
		Cleaned []string `json:"cleaned"`
		Skipped []struct {
			SessionID string `json:"sessionId"`
			Reason    string `json:"reason"`
		} `json:"skipped"`
	}
	mustJSON(t, body, &got)
	if !got.OK || len(got.Cleaned) != 1 || got.Cleaned[0] != "ao-1" {
		t.Fatalf("cleanup response = %#v", got)
	}
	if len(got.Skipped) != 1 || got.Skipped[0].SessionID != "ao-2" || got.Skipped[0].Reason != "workspace has uncommitted changes" {
		t.Fatalf("cleanup skipped = %#v, want preserved workspace with reason", got.Skipped)
	}
	if len(svc.cleanupProjects) != 1 || svc.cleanupProjects[0] != "ao" {
		t.Fatalf("cleanupProjects = %#v, want [ao]", svc.cleanupProjects)
	}
}

func TestSessionsAPI_CleanupWithoutProjectFilter(t *testing.T) {
	svc := newFakeSessionService()
	svc.cleanupResult = []domain.SessionID{"ao-1", "other-1"}
	srv := newSessionTestServer(t, svc)

	body, status, _ := doRequest(t, srv, "POST", "/api/v1/sessions/cleanup", "")
	if status != http.StatusOK {
		t.Fatalf("cleanup = %d, want 200; body=%s", status, body)
	}
	var got struct {
		Cleaned []string `json:"cleaned"`
	}
	mustJSON(t, body, &got)
	if len(got.Cleaned) != 2 || got.Cleaned[0] != "ao-1" || got.Cleaned[1] != "other-1" {
		t.Fatalf("cleanup response = %#v", got)
	}
	if len(svc.cleanupProjects) != 1 || svc.cleanupProjects[0] != "" {
		t.Fatalf("cleanupProjects = %#v, want empty project filter", svc.cleanupProjects)
	}
}

type sessionBody struct {
	ID               string `json:"id"`
	ProjectID        string `json:"projectId"`
	IssueID          string `json:"issueId"`
	Kind             string `json:"kind"`
	Harness          string `json:"harness"`
	DisplayName      string `json:"displayName"`
	Status           string `json:"status"`
	TerminalHandleID string `json:"terminalHandleId"`
}

func TestSessionsAPI_PRRoutes(t *testing.T) {
	srv := newSessionTestServer(t, newFakeSessionService())

	body, status, _ := doRequest(t, srv, "GET", "/api/v1/sessions/ao-1/pr", "")
	if status != http.StatusOK {
		t.Fatalf("GET PRs = %d body=%s", status, body)
	}
	var listed struct {
		SessionID string `json:"sessionId"`
		PRs       []struct {
			URL       string `json:"url"`
			Number    int    `json:"number"`
			State     string `json:"state"`
			UpdatedAt string `json:"updatedAt"`
		} `json:"prs"`
	}
	mustJSON(t, body, &listed)
	if listed.SessionID != "ao-1" || len(listed.PRs) != 1 || listed.PRs[0].State != "open" {
		t.Fatalf("GET shape = %#v", listed)
	}

	body, status, _ = doRequest(t, srv, "POST", "/api/v1/sessions/ao-1/pr/claim", `{"pr":"142"}`)
	if status != http.StatusOK {
		t.Fatalf("claim = %d body=%s", status, body)
	}
	var claimed struct {
		OK            bool     `json:"ok"`
		SessionID     string   `json:"sessionId"`
		PRs           []any    `json:"prs"`
		BranchChanged bool     `json:"branchChanged"`
		TakenOverFrom []string `json:"takenOverFrom"`
	}
	mustJSON(t, body, &claimed)
	if !claimed.OK || claimed.SessionID != "ao-1" || len(claimed.PRs) != 1 || !claimed.BranchChanged || len(claimed.TakenOverFrom) != 0 {
		t.Fatalf("claim shape = %#v", claimed)
	}
}

func TestSessionsAPI_ClaimPRErrors(t *testing.T) {
	cases := []struct {
		name string
		body string
		err  error
		code int
		want string
	}{
		{"bad json", `{`, nil, http.StatusBadRequest, "INVALID_JSON"},
		{"missing pr", `{}`, nil, http.StatusBadRequest, "PR_REQUIRED"},
		{"invalid ref", `{"pr":"x"}`, sessionsvc.ErrInvalidPRRef, http.StatusBadRequest, "INVALID_PR_REF"},
		{"session missing", `{"pr":"142"}`, apierr.NotFound("SESSION_NOT_FOUND", "Unknown session"), http.StatusNotFound, "SESSION_NOT_FOUND"},
		{"pr missing", `{"pr":"142"}`, sessionsvc.ErrPRNotFound, http.StatusNotFound, "PR_NOT_FOUND"},
		{"not open", `{"pr":"142"}`, sessionsvc.ErrPRNotOpen, http.StatusConflict, "PR_NOT_OPEN"},
		{"claimed", `{"pr":"142","allowTakeover":false}`, ports.PRClaimedByActiveSessionError{Owner: "ao-2"}, http.StatusConflict, "PR_CLAIMED_BY_ACTIVE_SESSION"},
		{"not claimable", `{"pr":"142"}`, sessionsvc.ErrSessionNotClaimable, http.StatusUnprocessableEntity, "SESSION_NOT_CLAIMABLE"},
		{"mismatch", `{"pr":"142"}`, sessionsvc.ErrProjectMismatch, http.StatusUnprocessableEntity, "PR_PROJECT_MISMATCH"},
		{"scm", `{"pr":"142"}`, sessionsvc.ErrSCMUnavailable, http.StatusServiceUnavailable, "SCM_UNAVAILABLE"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc := newFakeSessionService()
			svc.claimErr = tc.err
			srv := newSessionTestServer(t, svc)
			body, status, _ := doRequest(t, srv, "POST", "/api/v1/sessions/ao-1/pr/claim", tc.body)
			assertErrorCode(t, body, status, tc.code, tc.want)
		})
	}
}

func TestSessionsAPI_GitStatusAndActions(t *testing.T) {
	svc := newFakeSessionService()
	svc.gitStatus = ports.GitStatus{
		Branch: "ao/ao-1",
		Files:  []ports.GitFileChange{{Path: "main.go", Additions: 3, Deletions: 1, Staged: true}},
	}
	srv := newSessionTestServer(t, svc)

	body, status, headers := doRequest(t, srv, "GET", "/api/v1/sessions/ao-1/git", "")
	assertJSON(t, headers)
	if status != http.StatusOK {
		t.Fatalf("GET git = %d, want 200; body=%s", status, body)
	}
	var got struct {
		SessionID string `json:"sessionId"`
		Branch    string `json:"branch"`
		Files     []struct {
			Path      string `json:"path"`
			Additions int    `json:"additions"`
			Deletions int    `json:"deletions"`
			Staged    bool   `json:"staged"`
		} `json:"files"`
	}
	mustJSON(t, body, &got)
	if got.SessionID != "ao-1" || got.Branch != "ao/ao-1" || len(got.Files) != 1 {
		t.Fatalf("git status shape = %#v", got)
	}
	if f := got.Files[0]; f.Path != "main.go" || f.Additions != 3 || f.Deletions != 1 || !f.Staged {
		t.Fatalf("git file shape = %#v", f)
	}

	body, status, _ = doRequest(t, srv, "POST", "/api/v1/sessions/ao-1/git/stage", "")
	if status != http.StatusOK {
		t.Fatalf("POST stage = %d, want 200; body=%s", status, body)
	}
	if len(svc.gitStaged) != 1 || svc.gitStaged[0] != "ao-1" {
		t.Fatalf("staged sessions = %v", svc.gitStaged)
	}

	body, status, _ = doRequest(t, srv, "POST", "/api/v1/sessions/ao-1/git/discard", "")
	if status != http.StatusOK {
		t.Fatalf("POST discard = %d, want 200; body=%s", status, body)
	}
	if len(svc.gitDiscarded) != 1 || svc.gitDiscarded[0] != "ao-1" {
		t.Fatalf("discarded sessions = %v", svc.gitDiscarded)
	}

	body, status, _ = doRequest(t, srv, "POST", "/api/v1/sessions/ao-1/git/commit", `{"message":"fix: wire git rail","push":true}`)
	if status != http.StatusOK {
		t.Fatalf("POST commit = %d, want 200; body=%s", status, body)
	}
	var committed struct {
		OK     bool   `json:"ok"`
		SHA    string `json:"sha"`
		Branch string `json:"branch"`
		Pushed bool   `json:"pushed"`
	}
	mustJSON(t, body, &committed)
	if !committed.OK || committed.SHA != "abc1234" || committed.Branch != "ao/ao-1" || !committed.Pushed {
		t.Fatalf("commit shape = %#v", committed)
	}
	if len(svc.gitCommits) != 1 || svc.gitCommits[0] != "fix: wire git rail" || !svc.gitPushes[0] {
		t.Fatalf("commit recorded = %v pushes=%v", svc.gitCommits, svc.gitPushes)
	}
}

func TestSessionsAPI_GitCommitErrors(t *testing.T) {
	cases := []struct {
		name string
		body string
		err  error
		code int
		want string
	}{
		{"bad json", `{`, nil, http.StatusBadRequest, "INVALID_JSON"},
		{"missing message", `{}`, nil, http.StatusBadRequest, "MESSAGE_REQUIRED"},
		{"blank message", `{"message":"   "}`, nil, http.StatusBadRequest, "MESSAGE_REQUIRED"},
		{"nothing to commit", `{"message":"m"}`, apierr.Conflict("GIT_NOTHING_TO_COMMIT", "No changes to commit", nil), http.StatusConflict, "GIT_NOTHING_TO_COMMIT"},
		{"no workspace", `{"message":"m"}`, apierr.Conflict("SESSION_NO_WORKSPACE", "Session has no workspace", nil), http.StatusConflict, "SESSION_NO_WORKSPACE"},
		// A push-leg failure (GIT_NO_REMOTE / push rejected) is NOT in this table:
		// the commit lands first, so it returns 200 with the SHA — see
		// TestSessionsAPI_GitCommitPushFailureKeepsSHA.
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc := newFakeSessionService()
			svc.gitErr = tc.err
			srv := newSessionTestServer(t, svc)
			body, status, _ := doRequest(t, srv, "POST", "/api/v1/sessions/ao-1/git/commit", tc.body)
			assertErrorCode(t, body, status, tc.code, tc.want)
		})
	}
}

// A failure on the push leg must not lose the commit: CommitAll has already
// resolved the SHA, so the controller returns 200 with the SHA and a PushError
// warning rather than a bare 409 that would leave the committed work invisible.
func TestSessionsAPI_GitCommitPushFailureKeepsSHA(t *testing.T) {
	cases := []struct {
		name string
		err  error
	}{
		{"no remote", apierr.Conflict("GIT_NO_REMOTE", "no remote to push to", nil)},
		{"push rejected", errors.New("gitops: committed deadbeef, push failed: rejected")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc := newFakeSessionService()
			svc.gitErr = tc.err
			svc.gitErrResult = ports.GitCommitResult{SHA: "deadbeef", Branch: "ao/ao-1"}
			srv := newSessionTestServer(t, svc)

			body, status, _ := doRequest(t, srv, "POST", "/api/v1/sessions/ao-1/git/commit", `{"message":"m","push":true}`)
			if status != http.StatusOK {
				t.Fatalf("POST commit = %d, want 200; body=%s", status, body)
			}
			var got struct {
				OK        bool   `json:"ok"`
				SHA       string `json:"sha"`
				Branch    string `json:"branch"`
				Pushed    bool   `json:"pushed"`
				PushError string `json:"pushError"`
			}
			mustJSON(t, body, &got)
			if !got.OK || got.SHA != "deadbeef" || got.Branch != "ao/ao-1" || got.Pushed {
				t.Fatalf("commit shape = %#v", got)
			}
			if got.PushError != tc.err.Error() {
				t.Fatalf("pushError = %q, want %q", got.PushError, tc.err.Error())
			}
		})
	}
}
