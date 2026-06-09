// Package webui serves the built single-page web UI from the daemon. The
// compiled Vite assets are embedded at build time so the daemon ships as a
// single binary with no runtime dependency on the frontend toolchain.
//
// The bundle is produced by `npm run build:frontend`, which runs the ao-web
// Vite build and copies frontend/apps/web/dist/* into dist/ here. When the
// frontend has not been built, dist/ holds only a placeholder index.html (plus
// .gitkeep) so the //go:embed directive still compiles.
package webui

import (
	"embed"
	"io/fs"
	"net/http"
	"strings"
)

//go:embed dist
var embedded embed.FS

// Handler returns an http.Handler that serves the embedded SPA.
//
// Static files resolve directly. Any path that does not map to an embedded file
// falls back to index.html so client-side (TanStack Router) deep links survive
// a hard refresh. The one exception is /assets/*: a miss there is a genuinely
// absent build artifact, so it returns 404 rather than masking the error by
// serving the HTML shell with a 200.
func Handler() http.Handler {
	dist, err := fs.Sub(embedded, "dist")
	if err != nil {
		// dist is a compile-time embed; a failure here is a programming error,
		// not a runtime condition, so failing loudly is correct.
		panic(err)
	}
	fileServer := http.FileServer(http.FS(dist))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(r.URL.Path, "/")
		if name == "" {
			name = "index.html"
		}

		if _, statErr := fs.Stat(dist, name); statErr != nil {
			// Missing asset references are real 404s, not SPA routes.
			if strings.HasPrefix(r.URL.Path, "/assets/") {
				http.NotFound(w, r)
				return
			}
			// Unknown non-asset path: serve the SPA shell so the client router
			// can resolve the deep link. Rewrite onto a clone to avoid mutating
			// the caller's request.
			r = r.Clone(r.Context())
			r.URL.Path = "/"
		}
		fileServer.ServeHTTP(w, r)
	})
}
