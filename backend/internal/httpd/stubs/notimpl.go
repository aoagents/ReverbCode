// Package stubs holds the 501-with-contract-preview helper every route-shell
// handler uses. It lives in its own package (rather than alongside the router
// in httpd) so the per-resource controllers can import it without creating an
// import cycle back into httpd.
package stubs

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5/middleware"
)

// PlannedRoute is the documentation payload every 501 stub returns alongside
// the locked error envelope. It tells API consumers what the endpoint WILL
// look like once the real handler lands, so the dashboard team can scaffold
// against the contract before implementation merges.
//
// Legacy carries the old TS paths this route REPLACES — set on canonical
// routes that subsumed a now-unregistered TS endpoint. #19 (OpenAPI) will
// translate Legacy into an x-replaces extension on the spec.
type PlannedRoute struct {
	Route    string         `json:"route"`
	Legacy   []string       `json:"legacy,omitempty"`
	Request  map[string]any `json:"request,omitempty"`
	Response map[string]any `json:"response,omitempty"`
	Errors   []PlannedError `json:"errors,omitempty"`
	Notes    string         `json:"notes,omitempty"`
}

// PlannedError describes one error variant the future implementation will
// return. Status + Code together identify the variant uniquely.
type PlannedError struct {
	Status  int            `json:"status"`
	Code    string         `json:"code"`
	Message string         `json:"message"`
	Details map[string]any `json:"details,omitempty"`
}

// notImplementedResponse is the full 501 body: the locked APIError envelope
// fields plus a Planned block that documents the future contract.
type notImplementedResponse struct {
	Error     string       `json:"error"`
	Code      string       `json:"code"`
	Message   string       `json:"message"`
	RequestID string       `json:"requestId,omitempty"`
	Planned   PlannedRoute `json:"planned"`
}

// NotImplemented writes a 501 with the locked envelope plus the planned-route
// documentation. Used by every route in the shell; replaced one-by-one with
// real handlers as the lane progresses.
func NotImplemented(w http.ResponseWriter, r *http.Request, planned PlannedRoute) {
	body := notImplementedResponse{
		Error:     "not_implemented",
		Code:      "NOT_IMPLEMENTED",
		Message:   planned.Route + " is registered but not yet implemented",
		RequestID: middleware.GetReqID(r.Context()),
		Planned:   planned,
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusNotImplemented)
	// A write error here means the client went away mid-response; there is
	// nothing useful to do but stop.
	_ = json.NewEncoder(w).Encode(body)
}
