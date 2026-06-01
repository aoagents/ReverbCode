package apispec_test

import (
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	yaml "gopkg.in/yaml.v3"

	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apispec"
)

// TestRouteSpecParity asserts the mounted /api/v1 routes and the OpenAPI
// operations are in 1:1 correspondence — so a route can't be added without
// spec coverage, and the spec can't describe a route that isn't served.
func TestRouteSpecParity(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	router := httpd.NewRouter(config.Config{}, log, nil)

	mounted := map[string]bool{}
	err := chi.Walk(router, func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		if strings.HasPrefix(route, "/api/v1/") && route != "/api/v1/openapi.yaml" {
			mounted[strings.ToUpper(method)+" "+route] = true
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk routes: %v", err)
	}
	if len(mounted) == 0 {
		t.Fatal("no /api/v1 routes mounted — router wiring changed?")
	}

	// Build the spec's "METHOD /path" set from the embedded document.
	var doc struct {
		Paths map[string]map[string]yaml.Node `yaml:"paths"`
	}
	if err := yaml.Unmarshal(apispec.Doc(), &doc); err != nil {
		t.Fatalf("parse spec: %v", err)
	}
	httpMethods := map[string]bool{"get": true, "post": true, "put": true, "patch": true, "delete": true}
	specOps := map[string]bool{}
	for path, item := range doc.Paths {
		for method := range item {
			if httpMethods[method] { // skip parameters, summary, etc.
				specOps[strings.ToUpper(method)+" "+path] = true
			}
		}
	}

	// Forward: every mounted route has a spec operation.
	for r := range mounted {
		if !specOps[r] {
			t.Errorf("mounted route %s has no OpenAPI operation", r)
		}
	}
	// Reverse: every spec operation is a mounted route.
	for op := range specOps {
		if !mounted[op] {
			t.Errorf("spec operation %s has no mounted route", op)
		}
	}
}
