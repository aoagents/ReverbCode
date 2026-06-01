package controllers_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/project"
)

// fakeSpawner records the SpawnConfig it was called with and returns the
// canned Session/error. It satisfies session.Spawner.
type fakeSpawner struct {
	mu      sync.Mutex
	calls   []ports.SpawnConfig
	session domain.Session
	err     error
}

func (f *fakeSpawner) Spawn(_ context.Context, cfg ports.SpawnConfig) (domain.Session, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, cfg)
	if f.err != nil {
		return domain.Session{}, f.err
	}
	return f.session, nil
}

func (f *fakeSpawner) recorded() []ports.SpawnConfig {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]ports.SpawnConfig, len(f.calls))
	copy(out, f.calls)
	return out
}

func sessionsServer(t *testing.T, spawner *fakeSpawner) *httptest.Server {
	t.Helper()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	deps := httpd.APIDeps{}
	if spawner != nil {
		deps.Sessions = spawner
	}
	srv := httptest.NewServer(httpd.NewRouterWithAPI(config.Config{}, log, nil, deps))
	t.Cleanup(srv.Close)
	return srv
}

func TestSessionsAPI_Spawn_Success(t *testing.T) {
	spawner := &fakeSpawner{
		session: domain.Session{
			SessionRecord: domain.SessionRecord{
				ID:        "demo-1",
				ProjectID: "demo",
				Kind:      domain.KindWorker,
				Harness:   domain.HarnessClaudeCode,
				Metadata: domain.SessionMetadata{
					WorkspacePath:   "/tmp/demo-1",
					RuntimeHandleID: "zellij-demo-1",
				},
			},
		},
	}
	srv := sessionsServer(t, spawner)

	body, status, headers := doRequest(t, srv, "POST", "/api/v1/sessions",
		`{"projectId":"demo","prompt":"do the thing","agent":"claude-code"}`)
	if status != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body=%s", status, body)
	}
	assertJSON(t, headers)

	var out struct {
		SessionID     string `json:"sessionId"`
		WorkspacePath string `json:"workspacePath"`
		RuntimeHandle string `json:"runtimeHandle"`
	}
	mustJSON(t, body, &out)
	if out.SessionID != "demo-1" || out.WorkspacePath != "/tmp/demo-1" || out.RuntimeHandle != "zellij-demo-1" {
		t.Fatalf("response = %#v", out)
	}

	got := spawner.recorded()
	if len(got) != 1 {
		t.Fatalf("spawn calls = %d, want 1", len(got))
	}
	if got[0].ProjectID != "demo" || got[0].Prompt != "do the thing" || got[0].Harness != domain.HarnessClaudeCode || got[0].Kind != domain.KindWorker {
		t.Fatalf("recorded spawn = %#v", got[0])
	}
}

func TestSessionsAPI_Spawn_DefaultsAgentToClaudeCode(t *testing.T) {
	spawner := &fakeSpawner{
		session: domain.Session{
			SessionRecord: domain.SessionRecord{ID: "demo-2", ProjectID: "demo"},
		},
	}
	srv := sessionsServer(t, spawner)

	body, status, _ := doRequest(t, srv, "POST", "/api/v1/sessions",
		`{"projectId":"demo","prompt":"do the thing"}`)
	if status != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body=%s", status, body)
	}
	got := spawner.recorded()
	if len(got) != 1 || got[0].Harness != domain.HarnessClaudeCode {
		t.Fatalf("default agent not applied: %#v", got)
	}
}

func TestSessionsAPI_Spawn_BadRequest(t *testing.T) {
	cases := []struct {
		name, body, wantCode string
	}{
		{name: "invalid json", body: `{`, wantCode: "INVALID_JSON"},
		{name: "missing projectId", body: `{"prompt":"x"}`, wantCode: "PROJECT_ID_REQUIRED"},
		{name: "blank projectId", body: `{"projectId":"   ","prompt":"x"}`, wantCode: "PROJECT_ID_REQUIRED"},
		{name: "missing prompt", body: `{"projectId":"demo"}`, wantCode: "PROMPT_REQUIRED"},
		{name: "blank prompt", body: `{"projectId":"demo","prompt":"   "}`, wantCode: "PROMPT_REQUIRED"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			spawner := &fakeSpawner{}
			srv := sessionsServer(t, spawner)
			body, status, _ := doRequest(t, srv, "POST", "/api/v1/sessions", tc.body)
			assertErrorCode(t, body, status, http.StatusBadRequest, tc.wantCode)
			if len(spawner.recorded()) != 0 {
				t.Fatalf("spawn was called for invalid request")
			}
		})
	}
}

func TestSessionsAPI_Spawn_UnknownProject(t *testing.T) {
	spawner := &fakeSpawner{
		err: &project.Error{Kind: "not_found", Code: "PROJECT_NOT_FOUND", Message: "Unknown project"},
	}
	srv := sessionsServer(t, spawner)

	body, status, _ := doRequest(t, srv, "POST", "/api/v1/sessions",
		`{"projectId":"missing","prompt":"x"}`)
	assertErrorCode(t, body, status, http.StatusNotFound, "PROJECT_NOT_FOUND")
}

func TestSessionsAPI_Spawn_UnknownProjectWrapped(t *testing.T) {
	// Mirror the real production wrap: session.Manager.Spawn returns
	// `fmt.Errorf("spawn %s: workspace: %w", id, err)` over the projectresolver
	// chain. The controller must unwrap *project.Error rather than match by
	// string, so errors.As walks the linear %w chain.
	inner := &project.Error{Kind: "not_found", Code: "PROJECT_NOT_FOUND", Message: "Unknown project"}
	spawner := &fakeSpawner{
		err: fmt.Errorf("spawn demo-1: workspace: %w", fmt.Errorf("projectresolver: lookup %q: %w", "missing", inner)),
	}
	srv := sessionsServer(t, spawner)

	body, status, _ := doRequest(t, srv, "POST", "/api/v1/sessions",
		`{"projectId":"missing","prompt":"x"}`)
	assertErrorCode(t, body, status, http.StatusNotFound, "PROJECT_NOT_FOUND")
}

func TestSessionsAPI_Spawn_SessionsDisabled(t *testing.T) {
	srv := sessionsServer(t, nil)
	body, status, _ := doRequest(t, srv, "POST", "/api/v1/sessions",
		`{"projectId":"demo","prompt":"x"}`)
	if status != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503; body=%s", status, body)
	}
	var got errorBody
	mustJSON(t, body, &got)
	if got.Error != "sessions_disabled" {
		t.Fatalf("error = %q, want sessions_disabled\nbody=%s", got.Error, body)
	}
}

func TestSessionsAPI_Spawn_InternalFailure(t *testing.T) {
	spawner := &fakeSpawner{err: errors.New("runtime boom")}
	srv := sessionsServer(t, spawner)

	body, status, _ := doRequest(t, srv, "POST", "/api/v1/sessions",
		`{"projectId":"demo","prompt":"x"}`)
	assertErrorCode(t, body, status, http.StatusInternalServerError, "SPAWN_FAILED")
}
