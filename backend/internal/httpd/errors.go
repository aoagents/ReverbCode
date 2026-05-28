package httpd

import (
	"net/http"

	"github.com/go-chi/chi/v5/middleware"
)

// APIError is the locked wire shape for every non-2xx response. It supersedes
// the legacy TS `{error: "msg"}` bag with a machine-readable Code and a
// RequestID for log correlation (sourced from chi's RequestID middleware).
//
// Details is open so collision-style errors can carry typed sub-fields
// (e.g. existingProjectId, suggestedProjectId on POST /projects 409s).
type APIError struct {
	Error     string         `json:"error"`   // short kind, e.g. "not_found"
	Code      string         `json:"code"`    // SCREAMING_SNAKE, e.g. "PROJECT_NOT_FOUND"
	Message   string         `json:"message"` // human-readable
	RequestID string         `json:"requestId,omitempty"`
	Details   map[string]any `json:"details,omitempty"`
}

// writeAPIError emits the locked envelope for any non-2xx response. The
// request id falls back to empty when the chi middleware hasn't tagged the
// request (e.g. in tests that bypass NewRouter).
func writeAPIError(w http.ResponseWriter, r *http.Request, status int, kind, code, message string, details map[string]any) {
	writeJSON(w, status, APIError{
		Error:     kind,
		Code:      code,
		Message:   message,
		RequestID: middleware.GetReqID(r.Context()),
		Details:   details,
	})
}
