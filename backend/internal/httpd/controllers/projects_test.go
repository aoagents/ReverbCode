package controllers_test

// Route-shell tests for /api/v1/projects. Builds the full router (so the
// /api/v1 mount, middleware, NotFound, and MethodNotAllowed handlers are
// exercised together) and asserts every canonical route returns 501 with the
// locked envelope; legacy paths that the REST audit dropped return 404 or 405.

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd"
)

func newTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	// Discard logger keeps test output clean — the access-log middleware
	// added in base #10·1a wants a non-nil *slog.Logger.
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := httptest.NewServer(httpd.NewRouter(config.Config{}, log))
	t.Cleanup(srv.Close)
	return srv
}

// TestProjectsRoutes_Canonical501 walks every canonical /projects route and
// asserts it returns 501 with the locked envelope. The "wantLegacy" cases
// double-check that the planned body advertises the TS path it replaced.
func TestProjectsRoutes_Canonical501(t *testing.T) {
	srv := newTestServer(t)

	cases := []struct {
		method     string
		path       string
		body       string
		wantRoute  string
		wantLegacy []string
	}{
		{method: "GET", path: "/api/v1/projects", wantRoute: "GET /api/v1/projects"},
		{method: "POST", path: "/api/v1/projects", body: `{}`, wantRoute: "POST /api/v1/projects"},
		{method: "GET", path: "/api/v1/projects/p1", wantRoute: "GET /api/v1/projects/{id}"},
		{method: "PATCH", path: "/api/v1/projects/p1", body: `{}`, wantRoute: "PATCH /api/v1/projects/{id}"},
		{method: "DELETE", path: "/api/v1/projects/p1", wantRoute: "DELETE /api/v1/projects/{id}"},
		{
			method: "POST", path: "/api/v1/projects/p1/repair",
			wantRoute:  "POST /api/v1/projects/{id}/repair",
			wantLegacy: []string{"POST /api/v1/projects/{id}"},
		},
		{method: "POST", path: "/api/v1/projects/reload", wantRoute: "POST /api/v1/projects/reload"},
	}

	for _, tc := range cases {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			body, status, headers := doRequest(t, srv, tc.method, tc.path, tc.body)

			if status != http.StatusNotImplemented {
				t.Fatalf("status = %d, want 501", status)
			}
			if ct := headers.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
				t.Errorf("Content-Type = %q, want JSON", ct)
			}

			var got envelope
			if err := json.Unmarshal(body, &got); err != nil {
				t.Fatalf("unmarshal: %v\nbody=%s", err, body)
			}
			if got.Error != "not_implemented" || got.Code != "NOT_IMPLEMENTED" {
				t.Errorf("envelope error/code = %q/%q, want not_implemented/NOT_IMPLEMENTED", got.Error, got.Code)
			}
			if got.Message == "" {
				t.Error("envelope.message empty")
			}
			if got.Planned.Route != tc.wantRoute {
				t.Errorf("planned.route = %q, want %q", got.Planned.Route, tc.wantRoute)
			}
			if !equalStrings(got.Planned.Legacy, tc.wantLegacy) {
				t.Errorf("planned.legacy = %v, want %v", got.Planned.Legacy, tc.wantLegacy)
			}
		})
	}
}

// TestProjectsRoutes_LegacyUnregistered confirms the REST-audit-dropped TS
// paths are deliberately unregistered. PUT and POST on /projects/{id} hit
// sibling-method paths so chi returns 405; legacy nested paths return 404.
func TestProjectsRoutes_LegacyUnregistered(t *testing.T) {
	srv := newTestServer(t)

	cases := []struct {
		method     string
		path       string
		wantStatus int
		wantCode   string
		why        string
	}{
		// R3: PUT was a PATCH alias in TS; we keep PATCH only. Chi returns
		// 405 because sibling verbs (GET/PATCH/DELETE) exist on the same path.
		{method: "PUT", path: "/api/v1/projects/p1", wantStatus: 405, wantCode: "METHOD_NOT_ALLOWED", why: "R3 PUT not registered"},
		// R4: POST on /projects/{id} used to repair; canonical is /repair.
		// Same path has no sibling POST handler (POST collection is at /projects),
		// chi returns 405.
		{method: "POST", path: "/api/v1/projects/p1", wantStatus: 405, wantCode: "METHOD_NOT_ALLOWED", why: "R4 repair moved"},
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

// TestProjectsRoutes_ReloadBeforeID is the chi-ordering safety check. If the
// {id} wildcard were registered first, POST /projects/reload would match
// {id}="reload" → repair handler instead of the reload handler. We assert
// reload responds with its own planned.route.
func TestProjectsRoutes_ReloadBeforeID(t *testing.T) {
	srv := newTestServer(t)
	body, status, _ := doRequest(t, srv, "POST", "/api/v1/projects/reload", "")
	if status != http.StatusNotImplemented {
		t.Fatalf("status = %d, want 501", status)
	}
	var e envelope
	_ = json.Unmarshal(body, &e)
	if e.Planned.Route != "POST /api/v1/projects/reload" {
		t.Errorf("reload was shadowed by {id}: planned.route = %q", e.Planned.Route)
	}
}

// envelope mirrors the locked APIError + stubs.PlannedRoute on the wire. We
// declare it in the test rather than importing httpd's private type so the
// test pins the JSON contract independently of internal renames.
type envelope struct {
	Error     string  `json:"error"`
	Code      string  `json:"code"`
	Message   string  `json:"message"`
	RequestID string  `json:"requestId"`
	Planned   planned `json:"planned"`
}

type planned struct {
	Route  string   `json:"route"`
	Legacy []string `json:"legacy"`
}

func doRequest(t *testing.T, srv *httptest.Server, method, path, body string) ([]byte, int, http.Header) {
	t.Helper()
	var reqBody *strings.Reader
	if body != "" {
		reqBody = strings.NewReader(body)
	}
	// strings.Reader is not nilable for the no-body case via the io.Reader
	// interface, so branch the constructor explicitly.
	var req *http.Request
	var err error
	if reqBody != nil {
		req, err = http.NewRequest(method, srv.URL+path, reqBody)
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

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
