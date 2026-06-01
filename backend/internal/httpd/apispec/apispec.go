// Package apispec embeds the generated OpenAPI document (see build.go) and
// serves it verbatim at /api/v1/openapi.yaml. The document is generated from Go
// and drift-checked against build.go (build_internal_test.go), so this package
// only needs to embed and publish it — no parsing or validation at runtime.
package apispec

import (
	_ "embed"
	"net/http"

	"github.com/go-chi/chi/v5"
)

//go:embed openapi.yaml
var openapiYAML []byte

// Doc returns the embedded OpenAPI document bytes. Read-only; callers must not
// mutate the returned slice.
func Doc() []byte { return openapiYAML }

// ServeYAML serves the embedded openapi.yaml document, mounted at
// /api/v1/openapi.yaml so tooling can fetch the whole spec in one request.
func ServeYAML(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/yaml; charset=utf-8")
	_, _ = w.Write(openapiYAML)
}

// RegisterServe mounts ServeYAML at path on the supplied router.
func RegisterServe(r chi.Router, path string) {
	r.Get(path, ServeYAML)
}
