package webui

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// An unknown, non-asset path must fall back to the embedded index.html (the SPA
// shell) so client-side router deep links survive a hard refresh.
func TestHandler_ServesIndexForUnknownPath(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/sessions/abc/deep/link", nil)

	Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("unknown path: got status %d, want %d", rec.Code, http.StatusOK)
	}
	if body := rec.Body.String(); !strings.Contains(body, "frontend not built") {
		t.Fatalf("unknown path: body did not serve the index shell, got %q", body)
	}
}

// A missing asset is a genuinely absent build artifact, not a router route, so
// it must 404 rather than be masked by the HTML shell.
func TestHandler_NotFoundForMissingAsset(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/assets/missing.js", nil)

	Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("missing asset: got status %d, want %d", rec.Code, http.StatusNotFound)
	}
}

// The root path serves the embedded index.html directly.
func TestHandler_ServesIndexForRoot(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)

	Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("root: got status %d, want %d", rec.Code, http.StatusOK)
	}
	if body := rec.Body.String(); !strings.Contains(body, "frontend not built") {
		t.Fatalf("root: body did not serve the index shell, got %q", body)
	}
}
