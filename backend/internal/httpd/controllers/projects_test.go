package controllers_test

// Route-shell tests for /api/v1/projects. Builds the full router (so the
// /api/v1 mount, middleware, NotFound, and MethodNotAllowed handlers are
// exercised together). With a Manager wired the handlers run their full
// decode→call→encode path; with no Manager (the route-shell state) every
// canonical route returns 500 SERVICE_UNAVAILABLE (the route is implemented,
// the service isn't). Legacy paths the REST audit dropped return 405 (sibling
// method exists) or 404 (no sibling); reload/repair are never registered.

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/httpx"
	"github.com/aoagents/agent-orchestrator/backend/internal/project"
)

func newTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	// Discard logger keeps test output clean — the access-log middleware
	// added in base #10·1a wants a non-nil *slog.Logger.
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := httptest.NewServer(httpd.NewRouter(config.Config{}, log, nil))
	t.Cleanup(srv.Close)
	return srv
}

// newTestServerWithManager builds the router with a real project.Manager wired
// in, so the handlers run their full decode→call→encode path instead of the
// nil-Mgr 500 SERVICE_UNAVAILABLE short-circuit.
func newTestServerWithManager(t *testing.T, mgr project.Manager) *httptest.Server {
	t.Helper()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := httptest.NewServer(httpd.NewRouterWithAPI(config.Config{}, log, nil, httpd.APIDeps{Projects: mgr}))
	t.Cleanup(srv.Close)
	return srv
}

// fakeManager is a project.Manager that returns canned values and records what
// the handlers passed it, so the transport tests can assert both directions.
type fakeManager struct {
	summaries []project.Summary
	getResult project.GetResult
	project   project.Project
	removeRes project.RemoveResult
	err       error // when non-nil, every method returns it

	called   bool // set by every method, so tests can assert the handler did/didn't reach the Manager
	gotID    domain.ProjectID
	gotAdd   project.AddInput
	gotPatch project.UpdateConfigInput
}

func (f *fakeManager) List(context.Context) ([]project.Summary, error) {
	f.called = true
	return f.summaries, f.err
}

func (f *fakeManager) Get(_ context.Context, id domain.ProjectID) (project.GetResult, error) {
	f.called, f.gotID = true, id
	return f.getResult, f.err
}

func (f *fakeManager) Add(_ context.Context, in project.AddInput) (project.Project, error) {
	f.called, f.gotAdd = true, in
	return f.project, f.err
}

func (f *fakeManager) UpdateConfig(_ context.Context, id domain.ProjectID, patch project.UpdateConfigInput) (project.Project, error) {
	f.called, f.gotID, f.gotPatch = true, id, patch
	return f.project, f.err
}

func (f *fakeManager) Remove(_ context.Context, id domain.ProjectID) (project.RemoveResult, error) {
	f.called, f.gotID = true, id
	return f.removeRes, f.err
}

// TestProjects_List_OK exercises the response side: a Manager result is encoded
// into the { projects } envelope.
func TestProjects_List_OK(t *testing.T) {
	mgr := &fakeManager{summaries: []project.Summary{
		{ID: "p1", Name: "One", SessionPrefix: "one"},
		{ID: "p2", Name: "Two", SessionPrefix: "two", ResolveError: "boom"},
	}}
	srv := newTestServerWithManager(t, mgr)

	body, status, _ := doRequest(t, srv, "GET", "/api/v1/projects", "")
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200\nbody=%s", status, body)
	}
	var got struct {
		Projects []map[string]any `json:"projects"`
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("unmarshal: %v\nbody=%s", err, body)
	}
	if len(got.Projects) != 2 {
		t.Fatalf("projects len = %d, want 2", len(got.Projects))
	}
	if got.Projects[0]["sessionPrefix"] != "one" {
		t.Errorf("projects[0].sessionPrefix = %v, want one", got.Projects[0]["sessionPrefix"])
	}
}

// TestProjects_Add_Created exercises the request side (body decoded into
// AddInput) and the response side (201 + { project }).
func TestProjects_Add_Created(t *testing.T) {
	mgr := &fakeManager{project: project.Project{ID: "p1", Name: "One", Path: "/repo"}}
	srv := newTestServerWithManager(t, mgr)

	body, status, _ := doRequest(t, srv, "POST", "/api/v1/projects", `{"path":"/repo","projectId":"p1"}`)
	if status != http.StatusCreated {
		t.Fatalf("status = %d, want 201\nbody=%s", status, body)
	}
	if mgr.gotAdd.Path != "/repo" {
		t.Errorf("Manager got path %q, want /repo", mgr.gotAdd.Path)
	}
	if mgr.gotAdd.ProjectID == nil || *mgr.gotAdd.ProjectID != "p1" {
		t.Errorf("Manager got projectId %v, want p1", mgr.gotAdd.ProjectID)
	}
	var got struct {
		Project map[string]any `json:"project"`
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("unmarshal: %v\nbody=%s", err, body)
	}
	if got.Project["id"] != "p1" {
		t.Errorf("project.id = %v, want p1", got.Project["id"])
	}
}

// TestProjects_BodyValidation covers the request validation the transport does
// before any Manager logic: the JSON body must decode. Malformed and empty
// bodies both fail closed with 400 INVALID_JSON on every route that reads a
// body (POST add, PATCH updateConfig), the locked envelope is returned, and the
// Manager is never reached.
func TestProjects_BodyValidation(t *testing.T) {
	cases := []struct {
		name, method, path, body string
	}{
		{name: "add/malformed", method: "POST", path: "/api/v1/projects", body: `{not json`},
		{name: "add/empty", method: "POST", path: "/api/v1/projects", body: ``},
		{name: "patch/malformed", method: "PATCH", path: "/api/v1/projects/p1", body: `{not json`},
		{name: "patch/empty", method: "PATCH", path: "/api/v1/projects/p1", body: ``},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mgr := &fakeManager{}
			srv := newTestServerWithManager(t, mgr)
			body, status, _ := doRequest(t, srv, tc.method, tc.path, tc.body)
			if status != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400\nbody=%s", status, body)
			}
			assertEnvelope(t, body, "INVALID_JSON")
			if mgr.called {
				t.Error("Manager was called despite a body that failed to decode")
			}
		})
	}
}

// TestProjects_Get_Discriminator confirms GetResult maps onto the { status,
// project } envelope for both ok and degraded.
func TestProjects_Get_Discriminator(t *testing.T) {
	t.Run("ok", func(t *testing.T) {
		mgr := &fakeManager{getResult: project.GetResult{
			Status:  "ok",
			Project: &project.Project{ID: "p1", Name: "One"},
		}}
		srv := newTestServerWithManager(t, mgr)
		body, status, _ := doRequest(t, srv, "GET", "/api/v1/projects/p1", "")
		if status != http.StatusOK {
			t.Fatalf("status = %d, want 200\nbody=%s", status, body)
		}
		if mgr.gotID != "p1" {
			t.Errorf("Manager got id %q, want p1", mgr.gotID)
		}
		var got struct {
			Status  string         `json:"status"`
			Project map[string]any `json:"project"`
		}
		_ = json.Unmarshal(body, &got)
		if got.Status != "ok" || got.Project["id"] != "p1" {
			t.Errorf("got status=%q project=%v, want ok/p1", got.Status, got.Project)
		}
	})
	t.Run("degraded", func(t *testing.T) {
		mgr := &fakeManager{getResult: project.GetResult{
			Status:   "degraded",
			Degraded: &project.Degraded{ID: "p1", ResolveError: "bad config"},
		}}
		srv := newTestServerWithManager(t, mgr)
		body, status, _ := doRequest(t, srv, "GET", "/api/v1/projects/p1", "")
		if status != http.StatusOK {
			t.Fatalf("status = %d, want 200\nbody=%s", status, body)
		}
		var got struct {
			Status  string         `json:"status"`
			Project map[string]any `json:"project"`
		}
		_ = json.Unmarshal(body, &got)
		if got.Status != "degraded" || got.Project["resolveError"] != "bad config" {
			t.Errorf("got status=%q project=%v, want degraded/bad config", got.Status, got.Project)
		}
	})
}

// TestProjects_Remove_OK checks the { projectId, removedStorageDir } body.
func TestProjects_Remove_OK(t *testing.T) {
	mgr := &fakeManager{removeRes: project.RemoveResult{ProjectID: "p1", RemovedStorageDir: true}}
	srv := newTestServerWithManager(t, mgr)
	body, status, _ := doRequest(t, srv, "DELETE", "/api/v1/projects/p1", "")
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200\nbody=%s", status, body)
	}
	var got struct {
		ProjectID         string `json:"projectId"`
		RemovedStorageDir bool   `json:"removedStorageDir"`
	}
	_ = json.Unmarshal(body, &got)
	if got.ProjectID != "p1" || !got.RemovedStorageDir {
		t.Errorf("got %+v, want {p1 true}", got)
	}
}

// TestProjects_UpdateConfig_OK exercises PATCH: the body is decoded into
// UpdateConfigInput, the Manager is called, and { project } returns 200.
func TestProjects_UpdateConfig_OK(t *testing.T) {
	mgr := &fakeManager{project: project.Project{ID: "p1", Name: "One", Agent: project.AgentConfig{"default": "claude"}}}
	srv := newTestServerWithManager(t, mgr)

	body, status, _ := doRequest(t, srv, "PATCH", "/api/v1/projects/p1", `{"agent":{"default":"claude"}}`)
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200\nbody=%s", status, body)
	}
	if mgr.gotID != "p1" {
		t.Errorf("Manager got id %q, want p1", mgr.gotID)
	}
	if mgr.gotPatch.Agent["default"] != "claude" {
		t.Errorf("Manager got patch agent %v, want claude", mgr.gotPatch.Agent)
	}
	var got struct {
		Project map[string]any `json:"project"`
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("unmarshal: %v\nbody=%s", err, body)
	}
	if got.Project["id"] != "p1" {
		t.Errorf("project.id = %v, want p1", got.Project["id"])
	}
}

// TestProjects_ManagerErrorTranslation confirms the controller translates the
// Manager's typed httpx.APIErr into the locked envelope (status + machine code),
// and falls back to an opaque 500 INTERNAL for an untyped error so internals
// never leak.
func TestProjects_ManagerErrorTranslation(t *testing.T) {
	typed := []struct {
		name       string
		err        error
		wantStatus int
		wantCode   string
	}{
		{"not found → 404", httpx.NotFound("PROJECT_NOT_FOUND", "Unknown project"), http.StatusNotFound, "PROJECT_NOT_FOUND"},
		{"bad request → 400", httpx.BadRequest("NOT_A_GIT_REPO", "not a repo", nil), http.StatusBadRequest, "NOT_A_GIT_REPO"},
		{"conflict → 409", httpx.Conflict("PATH_ALREADY_REGISTERED", "dup", map[string]any{"existingProjectId": "ao"}), http.StatusConflict, "PATH_ALREADY_REGISTERED"},
	}
	for _, tc := range typed {
		t.Run(tc.name, func(t *testing.T) {
			srv := newTestServerWithManager(t, &fakeManager{err: tc.err})
			body, status, _ := doRequest(t, srv, "GET", "/api/v1/projects", "")
			if status != tc.wantStatus {
				t.Fatalf("status = %d, want %d\nbody=%s", status, tc.wantStatus, body)
			}
			assertEnvelope(t, body, tc.wantCode)
		})
	}

	t.Run("untyped error → 500 INTERNAL", func(t *testing.T) {
		srv := newTestServerWithManager(t, &fakeManager{err: errors.New("kaboom")})
		body, status, _ := doRequest(t, srv, "GET", "/api/v1/projects", "")
		if status != http.StatusInternalServerError {
			t.Fatalf("status = %d, want 500\nbody=%s", status, body)
		}
		assertEnvelope(t, body, "INTERNAL")
	})
}

// TestProjectsRoutes_NilManager walks every canonical /projects route with no
// Manager wired (the route-shell state) and asserts a 500 SERVICE_UNAVAILABLE —
// NOT a 501: the route and its transport are implemented, only the backing
// service is absent, so it must not claim "not implemented".
func TestProjectsRoutes_NilManager(t *testing.T) {
	srv := newTestServer(t)

	cases := []struct{ method, path, body string }{
		{method: "GET", path: "/api/v1/projects"},
		{method: "POST", path: "/api/v1/projects", body: `{}`},
		{method: "GET", path: "/api/v1/projects/p1"},
		{method: "PATCH", path: "/api/v1/projects/p1", body: `{}`},
		{method: "DELETE", path: "/api/v1/projects/p1"},
	}

	for _, tc := range cases {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			body, status, headers := doRequest(t, srv, tc.method, tc.path, tc.body)

			if status != http.StatusInternalServerError {
				t.Fatalf("status = %d, want 500\nbody=%s", status, body)
			}
			if ct := headers.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
				t.Errorf("Content-Type = %q, want JSON", ct)
			}
			assertEnvelope(t, body, "SERVICE_UNAVAILABLE")

			var got envelope
			_ = json.Unmarshal(body, &got)
			if got.Code == "NOT_IMPLEMENTED" || got.Error == "not_implemented" {
				t.Errorf("must not signal not-implemented; got error/code = %q/%q", got.Error, got.Code)
			}
		})
	}
}

// TestProjectsRoutes_LegacyUnregistered confirms the dropped/deferred paths
// are deliberately unregistered and fall through to the right handler:
//   - PUT/POST on /projects/{id} match the {id} path with no such method → 405.
//     (POST is the dropped legacy repair overload.)
//   - POST /projects/reload also matches {id}="reload" with no POST → 405,
//     since reload was dropped rather than registered.
//   - POST /projects/{id}/repair is a two-segment path with no route at all,
//     so it 404s; repair is deferred.
func TestProjectsRoutes_LegacyUnregistered(t *testing.T) {
	srv := newTestServer(t)

	cases := []struct {
		method, path, wantCode, why string
		wantStatus                  int
	}{
		{method: "PUT", path: "/api/v1/projects/p1", wantStatus: 405, wantCode: "METHOD_NOT_ALLOWED", why: "R3 PUT not registered"},
		{method: "POST", path: "/api/v1/projects/p1", wantStatus: 405, wantCode: "METHOD_NOT_ALLOWED", why: "R4 repair overload unregistered"},
		{method: "POST", path: "/api/v1/projects/reload", wantStatus: 405, wantCode: "METHOD_NOT_ALLOWED", why: "reload dropped; matches {id} with no POST"},
		{method: "POST", path: "/api/v1/projects/p1/repair", wantStatus: 404, wantCode: "ROUTE_NOT_FOUND", why: "repair deferred; no route registered"},
	}

	for _, tc := range cases {
		t.Run(tc.why, func(t *testing.T) {
			body, status, _ := doRequest(t, srv, tc.method, tc.path, "")
			if status != tc.wantStatus {
				t.Fatalf("%s %s = %d, want %d", tc.method, tc.path, status, tc.wantStatus)
			}
			var e envelope
			if err := json.Unmarshal(body, &e); err != nil {
				t.Fatalf("unmarshal: %v\nbody=%s", err, body)
			}
			if e.Code != tc.wantCode {
				t.Errorf("code = %q, want %q", e.Code, tc.wantCode)
			}
		})
	}
}

// TestProjectsRoutes_MissingRoute confirms the JSON 404 envelope (not chi's
// default text/plain) for routes that don't exist at all.
func TestProjectsRoutes_MissingRoute(t *testing.T) {
	srv := newTestServer(t)
	body, status, headers := doRequest(t, srv, "GET", "/api/v1/projects/p1/does-not-exist", "")

	if status != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", status)
	}
	if ct := headers.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("Content-Type = %q, want JSON (router must override chi's text/plain default)", ct)
	}
	var e envelope
	if err := json.Unmarshal(body, &e); err != nil {
		t.Fatalf("unmarshal: %v\nbody=%s", err, body)
	}
	if e.Code != "ROUTE_NOT_FOUND" {
		t.Errorf("code = %q, want ROUTE_NOT_FOUND", e.Code)
	}
}

// TestOpenAPIYAMLServed confirms the embedded spec is reachable at the
// documented path so external tooling can fetch it.
func TestOpenAPIYAMLServed(t *testing.T) {
	srv := newTestServer(t)
	body, status, headers := doRequest(t, srv, "GET", "/api/v1/openapi.yaml", "")
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200", status)
	}
	if ct := headers.Get("Content-Type"); !strings.HasPrefix(ct, "application/yaml") {
		t.Errorf("Content-Type = %q, want application/yaml*", ct)
	}
	if !strings.Contains(string(body), "openapi: 3.1.0") {
		t.Errorf("served body did not start with an OpenAPI 3.1 doc — first bytes:\n%s", firstLine(body))
	}
}

// envelope mirrors the locked APIError on the wire. We declare it in the test
// rather than importing httpx's type so the test pins the JSON contract
// independently of internal renames.
type envelope struct {
	Error     string `json:"error"`
	Code      string `json:"code"`
	Message   string `json:"message"`
	RequestID string `json:"requestId"`
}

// assertEnvelope decodes body as the locked APIError and checks the machine
// code plus that the envelope is fully populated. This pins R9: every non-2xx
// response carries error, code, message, and a correlation requestId (the
// router's RequestID middleware always tags it).
func assertEnvelope(t *testing.T, body []byte, wantCode string) {
	t.Helper()
	var e envelope
	if err := json.Unmarshal(body, &e); err != nil {
		t.Fatalf("unmarshal envelope: %v\nbody=%s", err, body)
	}
	if e.Code != wantCode {
		t.Errorf("code = %q, want %q", e.Code, wantCode)
	}
	if e.Error == "" {
		t.Error("envelope.error empty")
	}
	if e.Message == "" {
		t.Error("envelope.message empty")
	}
	if e.RequestID == "" {
		t.Error("envelope.requestId empty — RequestID middleware not applied?")
	}
}

func doRequest(t *testing.T, srv *httptest.Server, method, path, body string) ([]byte, int, http.Header) {
	t.Helper()
	var req *http.Request
	var err error
	if body != "" {
		req, err = http.NewRequest(method, srv.URL+path, strings.NewReader(body))
	} else {
		req, err = http.NewRequest(method, srv.URL+path, nil)
	}
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("do request: %v", err)
	}
	defer resp.Body.Close()
	buf := make([]byte, 0, 1024)
	tmp := make([]byte, 512)
	for {
		n, rerr := resp.Body.Read(tmp)
		if n > 0 {
			buf = append(buf, tmp[:n]...)
		}
		if rerr != nil {
			break
		}
	}
	return buf, resp.StatusCode, resp.Header
}

func firstLine(b []byte) string {
	if i := strings.IndexByte(string(b), '\n'); i >= 0 {
		return string(b[:i])
	}
	return string(b)
}
