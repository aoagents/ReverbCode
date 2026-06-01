package apispec_test

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apispec"
)

// TestDocEmbedded is the //go:embed smoke test: the embedded document is present
// and is a recognisable OpenAPI 3.1 doc.
func TestDocEmbedded(t *testing.T) {
	if !strings.Contains(string(apispec.Doc()), "openapi: 3.1.0") {
		t.Fatal("embedded openapi.yaml is empty or not an OpenAPI 3.1 document")
	}
}

// TestServeYAML serves the raw embedded document so tooling can fetch it whole.
func TestServeYAML(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/openapi.yaml", nil)
	apispec.ServeYAML(rec, req)

	if rec.Code != 200 {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/yaml") {
		t.Errorf("Content-Type = %q, want application/yaml*", ct)
	}
	if !strings.Contains(rec.Body.String(), "openapi: 3.1.0") {
		t.Errorf("body did not begin with an OpenAPI 3.1 doc")
	}
}
