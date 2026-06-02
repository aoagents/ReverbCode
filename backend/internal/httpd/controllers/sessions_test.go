package controllers_test

import (
	"net/http"
	"testing"
)

func TestSessionsRoutes_DefaultToStubs(t *testing.T) {
	srv := newTestServer(t)

	cases := []struct {
		method string
		path   string
		body   string
	}{
		{method: "GET", path: "/api/v1/sessions"},
		{method: "POST", path: "/api/v1/sessions", body: `{"projectId":"proj","prompt":"start"}`},
		{method: "GET", path: "/api/v1/sessions/s1"},
		{method: "POST", path: "/api/v1/sessions/s1/restore"},
		{method: "POST", path: "/api/v1/sessions/s1/send", body: `{"message":"hello"}`},
	}

	for _, tc := range cases {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			body, status, headers := doRequest(t, srv, tc.method, tc.path, tc.body)
			assertJSON(t, headers)
			assertErrorCode(t, body, status, http.StatusNotImplemented, "NOT_IMPLEMENTED")
		})
	}
}

func TestSessionsRoutes_DroppedAndDeferredUnregistered(t *testing.T) {
	srv := newTestServer(t)

	cases := []struct {
		method, path, wantCode, why string
		wantStatus                  int
	}{
		{
			method:     "POST",
			path:       "/api/v1/sessions/s1/message",
			wantStatus: http.StatusNotFound,
			wantCode:   "ROUTE_NOT_FOUND",
			why:        "duplicate /message route is dropped in favor of /send",
		},
		{
			method:     "POST",
			path:       "/api/v1/spawn",
			wantStatus: http.StatusNotFound,
			wantCode:   "ROUTE_NOT_FOUND",
			why:        "legacy spawn route is not registered",
		},
		{
			method:     "POST",
			path:       "/api/v1/sessions/s1/kill",
			wantStatus: http.StatusNotFound,
			wantCode:   "ROUTE_NOT_FOUND",
			why:        "kill response bytes are not locked in this first slice",
		},
	}

	for _, tc := range cases {
		t.Run(tc.why, func(t *testing.T) {
			body, status, _ := doRequest(t, srv, tc.method, tc.path, "")
			assertErrorCode(t, body, status, tc.wantStatus, tc.wantCode)
		})
	}
}
