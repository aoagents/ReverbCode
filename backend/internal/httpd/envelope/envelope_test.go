package envelope_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/envelope"
)

func TestWriteErrorIncludesUnderlyingCause(t *testing.T) {
	cases := []struct {
		name       string
		err        error
		wantStatus int
		wantCode   string
		wantMsg    string
	}{
		{
			name:       "untyped error includes cause",
			err:        errors.New("sql: Scan error on column index 10"),
			wantStatus: http.StatusInternalServerError,
			wantCode:   "INTERNAL_ERROR",
			wantMsg:    "Internal server error: sql: Scan error on column index 10",
		},
		{
			name:       "nil error still provides diagnostic fallback",
			err:        nil,
			wantStatus: http.StatusInternalServerError,
			wantCode:   "INTERNAL_ERROR",
			wantMsg:    "Internal server error: unknown error",
		},
		{
			name:       "wrapped apierr with empty message falls back to outer error string",
			err:        fmt.Errorf("load session: %w", apierr.Internal("SESSION_LOAD_FAILED", "")),
			wantStatus: http.StatusInternalServerError,
			wantCode:   "SESSION_LOAD_FAILED",
			wantMsg:    "load session: ",
		},
		{
			name:       "typed apierr preserves explicit message",
			err:        apierr.NotFound("SESSION_NOT_FOUND", "Unknown session"),
			wantStatus: http.StatusNotFound,
			wantCode:   "SESSION_NOT_FOUND",
			wantMsg:    "Unknown session",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, "/api/v1/sessions", nil)
			envelope.WriteError(rec, req, tc.err)

			if rec.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d", rec.Code, tc.wantStatus)
			}
			var got envelope.APIError
			if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
				t.Fatalf("unmarshal response: %v", err)
			}
			if got.Code != tc.wantCode {
				t.Fatalf("code = %q, want %q", got.Code, tc.wantCode)
			}
			if got.Message == "Internal server error" {
				t.Fatalf("message is bare %q; must include diagnostic cause", got.Message)
			}
			if got.Message != tc.wantMsg {
				t.Fatalf("message = %q, want %q", got.Message, tc.wantMsg)
			}
		})
	}
}
