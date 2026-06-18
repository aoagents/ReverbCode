package httpd

import (
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5/middleware"

	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/envelope"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// requestLogger emits one structured access-log line per request via the
// daemon's slog logger. Chi's built-in middleware.Logger writes to stdout
// using stdlib log; reusing the daemon's slog keeps every line on stderr in
// the same key=value shape as the rest of the daemon (one stream for the
// Electron supervisor to capture, one format to grep).
//
// Status, bytes, and duration come from a wrapped ResponseWriter so the log
// is accurate even when the handler returns without calling WriteHeader. The
// request id is read off the context populated by middleware.RequestID, so
// this middleware must be mounted after it.
//
// A 5xx line additionally carries the raw service error recorded by
// envelope.WriteError: the wire envelope hides internals ("Internal server
// error"), so without this the cause of a 500 was lost entirely.
func requestLogger(log *slog.Logger, sink ports.EventSink) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
			r, capturedErr := envelope.WithErrorCapture(r)
			start := time.Now()
			defer func() {
				attrs := []any{
					"id", middleware.GetReqID(r.Context()),
					"method", r.Method,
					"path", r.URL.Path,
					"status", ww.Status(),
					"bytes", ww.BytesWritten(),
					"duration", time.Since(start),
					"remote", r.RemoteAddr,
				}
				if err := capturedErr(); err != nil && ww.Status() >= http.StatusInternalServerError {
					attrs = append(attrs, "error", err)
				}
				log.Info("http request", attrs...)
				if sink != nil && ww.Status() >= http.StatusInternalServerError {
					payload := map[string]any{
						"method":   r.Method,
						"path":     r.URL.Path,
						"status":   ww.Status(),
						"duration": time.Since(start).Milliseconds(),
					}
					if err := capturedErr(); err != nil {
						payload["error_kind"] = "internal"
						var apiErr *apierr.Error
						if errors.As(err, &apiErr) {
							payload["error_kind"] = telemetryErrorKind(apiErr.Kind)
							if apiErr.Code != "" {
								payload["error_code"] = apiErr.Code
							}
						}
					}
					sink.Emit(r.Context(), ports.TelemetryEvent{
						Name:       "ao.http.5xx",
						Source:     "http",
						OccurredAt: time.Now().UTC(),
						Level:      ports.TelemetryLevelError,
						RequestID:  middleware.GetReqID(r.Context()),
						Payload:    payload,
					})
				}
			}()
			next.ServeHTTP(ww, r)
		})
	}
}

func telemetryErrorKind(kind apierr.Kind) string {
	switch kind {
	case apierr.KindInvalid:
		return "invalid"
	case apierr.KindNotFound:
		return "not_found"
	case apierr.KindConflict:
		return "conflict"
	default:
		return "internal"
	}
}
