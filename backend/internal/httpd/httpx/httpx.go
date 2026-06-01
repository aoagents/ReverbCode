// Package httpx holds the transport primitives shared across the HTTP surface:
// the JSON writer, the locked error envelope, and a request-body decoder. It
// is a leaf package (no imports of httpd or controllers) so both the router
// (package httpd) and the resource controllers can depend on it without a
// cycle — httpd imports controllers, so the writers can't live in httpd.
package httpx

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5/middleware"
)

// WriteJSON serialises v as JSON with the given status. A write error means the
// client went away mid-response; there is nothing useful to do but stop.
func WriteJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// Error is the locked wire shape for every non-2xx response. It supersedes the
// legacy TS `{error: "msg"}` bag with a machine-readable Code and a RequestID
// for log correlation. Details is open so collision-style errors can carry
// typed sub-fields (e.g. existingProjectId on POST /projects 409s).
type Error struct {
	Error     string         `json:"error" description:"Short kind, e.g. not_found"`
	Code      string         `json:"code" description:"SCREAMING_SNAKE machine code"`
	Message   string         `json:"message" description:"Human-readable detail"`
	RequestID string         `json:"requestId,omitempty"`
	Details   map[string]any `json:"details,omitempty"`
}

// WriteError emits the locked envelope for any non-2xx response. The request id
// falls back to empty when the chi middleware hasn't tagged the request (e.g.
// in tests that bypass the router).
func WriteError(w http.ResponseWriter, r *http.Request, status int, kind, code, message string, details map[string]any) {
	WriteJSON(w, status, Error{
		Error:     kind,
		Code:      code,
		Message:   message,
		RequestID: middleware.GetReqID(r.Context()),
		Details:   details,
	})
}

// APIErr is a typed application error carrying everything needed to render the
// locked envelope: an HTTP status plus kind/code/message/details. Service layers
// (e.g. project.Manager) return these so they don't need an http.ResponseWriter
// or to know transport details; controllers translate them via AsAPIErr +
// WriteAPIErr. This is "the http-layer error type" reused across layers — there
// is deliberately no parallel error type in the feature packages.
type APIErr struct {
	Status  int
	Kind    string
	Code    string
	Message string
	Details map[string]any
}

func (e *APIErr) Error() string {
	if e == nil {
		return ""
	}
	return e.Message
}

func newAPIErr(status int, kind, code, message string, details map[string]any) *APIErr {
	return &APIErr{Status: status, Kind: kind, Code: code, Message: message, Details: details}
}

// BadRequest/NotFound/Conflict/Internal construct typed errors for the common
// statuses. code is the SCREAMING_SNAKE machine code; details is optional.
func BadRequest(code, message string, details map[string]any) *APIErr {
	return newAPIErr(http.StatusBadRequest, "bad_request", code, message, details)
}
func NotFound(code, message string) *APIErr {
	return newAPIErr(http.StatusNotFound, "not_found", code, message, nil)
}
func Conflict(code, message string, details map[string]any) *APIErr {
	return newAPIErr(http.StatusConflict, "conflict", code, message, details)
}
func Internal(code, message string) *APIErr {
	return newAPIErr(http.StatusInternalServerError, "internal", code, message, nil)
}

// AsAPIErr extracts an *APIErr from err, if present.
func AsAPIErr(err error) (*APIErr, bool) {
	var e *APIErr
	if errors.As(err, &e) {
		return e, true
	}
	return nil, false
}

// WriteAPIErr renders an *APIErr as the locked envelope.
func WriteAPIErr(w http.ResponseWriter, r *http.Request, e *APIErr) {
	WriteError(w, r, e.Status, e.Kind, e.Code, e.Message, e.Details)
}

// DecodeJSON decodes the request body into dst. It is lenient about unknown
// keys on purpose: the project config blobs carry passthrough semantics, so
// extra fields must survive rather than fail the request.
func DecodeJSON(r *http.Request, dst any) error {
	return json.NewDecoder(r.Body).Decode(dst)
}
