package httpd

import (
	"encoding/json"
	"net/http"
)

// writeJSON serialises v as JSON with the given status. It is the single JSON
// writer for the skeleton; the typed error envelope (open item Q1.3) will build
// on this in a later phase.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	// A write error here means the client went away mid-response; there is
	// nothing useful to do but stop.
	_ = json.NewEncoder(w).Encode(v)
}
