package controllers_test

// Route-shell tests for /api/v1/projects. Builds the full router (so the
// /api/v1 mount, middleware, NotFound, and MethodNotAllowed handlers are
// exercised together) and asserts every canonical route returns 501 with
// the locked envelope + a `spec` slice sourced from apispec/openapi.yaml.
// Legacy paths that the REST audit dropped return 405 (sibling method
// exists) or 404 (no sibling); the legacy → canonical mapping itself is
// documented in the YAML via x-replaces.

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
// asserts it returns 501 with the locked envelope. wantOpID double-checks
// that the embedded spec slice is the right operation (not e.g. the
// /projects/{id} block leaking into /projects/reload because of route
// shadowing).
func TestProjectsRoutes_Canonical501(t *testing.T) {
	srv := newTestServer(t)

	cases := []struct {
		method, path, body, wantOpID string
		wantReplaces                 []string
	}{
		{method: "GET", path: "/api/v1/projects", wantOpID: "listProjects"},
		{method: "POST", path: "/api/v1/projects", body: `{}`, wantOpID: "addProject"},
		{method: "GET", path: "/api/v1/projects/p1", wantOpID: "getProject"},
		{method: "PATCH", path: "/api/v1/projects/p1", body: `{}`, wantOpID: "updateProjectConfig"},
		{method: "DELETE", path: "/api/v1/projects/p1", wantOpID: "removeProject"},
		{
			method: "POST", path: "/api/v1/projects/p1/repair",
			wantOpID:     "repairProject",
			wantReplaces: []string{"POST /api/v1/projects/{id}"},
		},
		{method: "POST", path: "/api/v1/projects/reload", wantOpID: "reloadProjects"},
	}

	for _, tc := range cases {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			body, status, headers := doRequest(t, srv, tc.method, tc.path, tc.body)

			if status != http.StatusNotImplemented {
				t.Fatalf("status = %d, want 501\nbody=%s", status, body)
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
			if got.Spec == nil {
				t.Fatal("envelope.spec missing — apispec failed to find the operation")
			}
			if op, _ := got.Spec["operationId"].(string); op != tc.wantOpID {
				t.Errorf("spec.operationId = %q, want %q", op, tc.wantOpID)
			}
			gotReplaces := stringSlice(got.Spec["x-replaces"])
			if !equalStrings(gotReplaces, tc.wantReplaces) {
				t.Errorf("spec.x-replaces = %v, want %v", gotReplaces, tc.wantReplaces)
			}
		})
	}
}

// TestProjectsRoutes_LegacyUnregistered confirms the REST-audit-dropped TS
// paths are deliberately unregistered. PUT and POST on /projects/{id} hit
// sibling-method paths so chi returns 405. The legacy → canonical mapping
// is documented on the canonical operation via x-replaces (covered above).
func TestProjectsRoutes_LegacyUnregistered(t *testing.T) {
	srv := newTestServer(t)

	cases := []struct {
		method, path, wantCode, why string
		wantStatus                  int
	}{
		{method: "PUT", path: "/api/v1/projects/p1", wantStatus: 405, wantCode: "METHOD_NOT_ALLOWED", why: "R3 PUT not registered"},
		{method: "POST", path: "/api/v1/projects/p1", wantStatus: 405, wantCode: "METHOD_NOT_ALLOWED", why: "R4 repair moved to /repair"},
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

// TestProjectsRoutes_ReloadBeforeID is the chi-ordering safety check. If
// the {id} wildcard were registered first, POST /projects/reload would
// match {id}="reload" → repair handler instead of the reload handler. We
// assert reload's spec slice (operationId=reloadProjects), not repair's.
func TestProjectsRoutes_ReloadBeforeID(t *testing.T) {
	srv := newTestServer(t)
	body, status, _ := doRequest(t, srv, "POST", "/api/v1/projects/reload", "")
	if status != http.StatusNotImplemented {
		t.Fatalf("status = %d, want 501", status)
	}
	var e envelope
	_ = json.Unmarshal(body, &e)
	if op, _ := e.Spec["operationId"].(string); op != "reloadProjects" {
		t.Errorf("reload was shadowed by {id}: spec.operationId = %q", op)
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

// envelope mirrors the locked APIError + apispec spec field on the wire.
// We declare it in the test rather than importing apispec's private type
// so the test pins the JSON contract independently of internal renames.
type envelope struct {
	Error     string         `json:"error"`
	Code      string         `json:"code"`
	Message   string         `json:"message"`
	RequestID string         `json:"requestId"`
	Spec      map[string]any `json:"spec"`
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

// stringSlice coerces an `any` that holds a YAML-decoded sequence into a
// []string. yaml.v3 decodes sequences as []any; we only call this on slices
// whose elements we know are strings.
func stringSlice(v any) []string {
	raw, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(raw))
	for _, item := range raw {
		s, _ := item.(string)
		out = append(out, s)
	}
	return out
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

func firstLine(b []byte) string {
	if i := strings.IndexByte(string(b), '\n'); i >= 0 {
		return string(b[:i])
	}
	return string(b)
}
