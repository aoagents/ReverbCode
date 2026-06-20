package httpd

import (
	"fmt"
	"log/slog"
	"net/http"
	"runtime/debug"
	"strings"
	"time"

	"github.com/go-chi/chi/v5/middleware"

	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/envelope"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func recoverTelemetry(log *slog.Logger, sink ports.EventSink) func(http.Handler) http.Handler {
	log = loggerOrDefault(log)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if rec := recover(); rec != nil {
					stack := string(debug.Stack())
					log.Error("http handler panic",
						"id", middleware.GetReqID(r.Context()),
						"method", r.Method,
						"path", r.URL.Path,
						"panic", fmt.Sprint(rec),
						"stack", stack,
					)
					if sink != nil {
						sink.Emit(r.Context(), ports.TelemetryEvent{
							Name:       "ao.daemon.panic",
							Source:     "http",
							OccurredAt: time.Now().UTC(),
							Level:      ports.TelemetryLevelError,
							RequestID:  middleware.GetReqID(r.Context()),
							Payload: map[string]any{
								"method":     r.Method,
								"path":       r.URL.Path,
								"panic_kind": telemetryPanicKind(rec),
							},
						})
					}
					writeRecoveredError(w, r)
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}

func telemetryPanicKind(rec any) string {
	switch rec.(type) {
	case error:
		return "error"
	case string:
		return "string"
	default:
		return "other"
	}
}

func writeRecoveredError(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, "/api/") {
		envelope.WriteAPIError(w, r, http.StatusInternalServerError, "internal_error", "INTERNAL_ERROR", "Internal server error", nil)
		return
	}
	envelope.WriteJSON(w, http.StatusInternalServerError, map[string]any{
		"status": "error",
	})
}
