package controllers_test

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd"
)

func newPRsTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := httptest.NewServer(httpd.NewRouter(config.Config{}, log, nil))
	t.Cleanup(srv.Close)
	return srv
}

func TestPRsRoutes_MergeReturns501(t *testing.T) {
	srv := newPRsTestServer(t)

	body, status, headers := doRequest(t, srv, "POST", "/api/v1/prs/1/merge", "")
	assertJSON(t, headers)
	assertErrorCode(t, body, status, http.StatusNotImplemented, "NOT_IMPLEMENTED")
}

func TestPRsRoutes_ResolveCommentsReturns501(t *testing.T) {
	srv := newPRsTestServer(t)

	body, status, headers := doRequest(t, srv, "POST", "/api/v1/prs/1/resolve-comments", "")
	assertJSON(t, headers)
	assertErrorCode(t, body, status, http.StatusNotImplemented, "NOT_IMPLEMENTED")
}

func TestPRsRoutes_MergeIncludesSpecSlice(t *testing.T) {
	srv := newPRsTestServer(t)

	body, _, _ := doRequest(t, srv, "POST", "/api/v1/prs/42/merge", "")
	var resp struct {
		Code string         `json:"code"`
		Spec map[string]any `json:"spec"`
	}
	mustJSON(t, body, &resp)
	if resp.Code != "NOT_IMPLEMENTED" {
		t.Fatalf("code = %q, want NOT_IMPLEMENTED", resp.Code)
	}
	if resp.Spec == nil {
		t.Fatal("spec field missing from 501 body")
	}
}

func TestPRsRoutes_ResolveCommentsIncludesSpecSlice(t *testing.T) {
	srv := newPRsTestServer(t)

	body, _, _ := doRequest(t, srv, "POST", "/api/v1/prs/42/resolve-comments", `{"commentIds":["c1"]}`)
	var resp struct {
		Code string         `json:"code"`
		Spec map[string]any `json:"spec"`
	}
	mustJSON(t, body, &resp)
	if resp.Code != "NOT_IMPLEMENTED" {
		t.Fatalf("code = %q, want NOT_IMPLEMENTED", resp.Code)
	}
	if resp.Spec == nil {
		t.Fatal("spec field missing from 501 body")
	}
}
