package envelope

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5/middleware"

	apierr "github.com/aoagents/agent-orchestrator/backend/internal/httpd/errors"
)

// APIError is the locked wire shape for every non-2xx response.
type APIError struct {
	Error     string         `json:"error"`
	Code      string         `json:"code"`
	Message   string         `json:"message"`
	RequestID string         `json:"requestId,omitempty"`
	Details   map[string]any `json:"details,omitempty"`
}

// WriteJSON serialises v as JSON with the given status.
func WriteJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// WriteAPIError emits the locked envelope for any non-2xx response.
func WriteAPIError(w http.ResponseWriter, r *http.Request, status int, kind, code, message string, details map[string]any) {
	WriteJSON(w, status, APIError{
		Error:     kind,
		Code:      code,
		Message:   message,
		RequestID: middleware.GetReqID(r.Context()),
		Details:   details,
	})
}

// WriteError is the single path from any service error to the wire envelope. It
// renders an *apierr.Error (anywhere in the chain) using its Kind, and falls
// back to a 500 for any other error so internal details never leak. This is the
// only place an apierr.Kind is translated into an HTTP status and wire word.
func WriteError(w http.ResponseWriter, r *http.Request, err error) {
	var e *apierr.Error
	if errors.As(err, &e) {
		status, kind := httpStatus(e.Kind)
		WriteAPIError(w, r, status, kind, e.Code, e.Message, e.Details)
		return
	}
	WriteAPIError(w, r, http.StatusInternalServerError, "internal", "INTERNAL_ERROR", "Internal server error", nil)
}

// httpStatus maps a semantic failure Kind to its HTTP status and wire word.
func httpStatus(k apierr.Kind) (int, string) {
	switch k {
	case apierr.KindInvalid:
		return http.StatusBadRequest, "bad_request"
	case apierr.KindNotFound:
		return http.StatusNotFound, "not_found"
	case apierr.KindConflict:
		return http.StatusConflict, "conflict"
	default:
		return http.StatusInternalServerError, "internal"
	}
}
