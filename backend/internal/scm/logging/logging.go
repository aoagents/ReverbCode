package logging

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

const (
	FieldCorrelationID       = "correlation_id"
	FieldProvider            = "provider"
	FieldHost                = "host"
	FieldRepo                = "repo"
	FieldProjectID           = "project_id"
	FieldSessionID           = "session_id"
	FieldSessionCount        = "session_count"
	FieldSnapshotCount       = "snapshot_count"
	FieldChangedCount        = "changed_count"
	FieldChangeRequestNumber = "change_request_number"
	FieldOperation           = "operation"
	FieldCommand             = "command"
	FieldActor               = "actor"
	FieldFreshness           = "freshness"
	FieldDurationMS          = "duration_ms"
	FieldMethod              = "method"
	FieldEndpointTemplate    = "endpoint_template"
	FieldStatusCode          = "status_code"
	FieldErrorKind           = "error_kind"
	FieldCacheHit            = "cache_hit"
	FieldETagPresent         = "etag_present"
	FieldRateLimitLimit      = "rate_limit_limit"
	FieldRateLimitRemaining  = "rate_limit_remaining"
	FieldRateLimitReset      = "rate_limit_reset"
	FieldBackoffUntil        = "backoff_until"
	FieldRateLimitUntil      = "rate_limit_until"
)

const (
	EventObserveStarted       = "scm.observe.started"
	EventObserveCompleted     = "scm.observe.completed"
	EventObserveFailed        = "scm.observe.failed"
	EventSnapshotSaved        = "scm.snapshot.saved"
	EventSnapshotUnchanged    = "scm.snapshot.unchanged"
	EventSnapshotUnavailable  = "scm.snapshot.unavailable"
	EventCommandStarted       = "scm.command.started"
	EventCommandCompleted     = "scm.command.completed"
	EventCommandFailed        = "scm.command.failed"
	EventCommandCacheInvalid  = "scm.command.cache_invalidated"
	EventCommandRefreshFailed = "scm.command.refresh_failed"
	EventCommandAuditFailed   = "scm.command.audit_failed"
	EventTransportRequest     = "scm.transport.request"
	EventTransportResponse    = "scm.transport.response"
	EventTransportFailed      = "scm.transport.failed"
	EventTransportRateLimited = "scm.transport.rate_limited"
)

const maxDiagnosticMessage = 300

type correlationKey struct{}

// Logger returns the supplied logger or slog.Default when nil.
func Logger(logger *slog.Logger) *slog.Logger {
	if logger != nil {
		return logger
	}
	return slog.Default()
}

// WithCorrelationID stores an already-safe correlation id in ctx.
func WithCorrelationID(ctx context.Context, id string) context.Context {
	id = strings.TrimSpace(id)
	if id == "" {
		return ctx
	}
	return context.WithValue(ctx, correlationKey{}, id)
}

// CorrelationID returns the SCM correlation id from ctx, if any.
func CorrelationID(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	id, _ := ctx.Value(correlationKey{}).(string)
	return id
}

// EnsureCorrelationID returns a context carrying a correlation id and that id.
func EnsureCorrelationID(ctx context.Context) (context.Context, string) {
	if ctx == nil {
		ctx = context.Background()
	}
	if id := CorrelationID(ctx); id != "" {
		return ctx, id
	}
	id := newCorrelationID()
	return WithCorrelationID(ctx, id), id
}

func newCorrelationID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err == nil {
		return hex.EncodeToString(b[:])
	}
	return hex.EncodeToString([]byte(time.Now().UTC().Format("20060102150405.000000000")))
}

func CorrelationAttr(ctx context.Context) slog.Attr {
	return slog.String(FieldCorrelationID, CorrelationID(ctx))
}

func SubjectAttrs(subj domain.SCMSubject) []slog.Attr {
	attrs := []slog.Attr{
		slog.String(FieldProvider, string(subj.Provider)),
		slog.String(FieldHost, subj.Host),
		slog.String(FieldRepo, subj.Repo),
		slog.String(FieldProjectID, string(subj.ProjectID)),
	}
	if subj.SessionID != "" {
		attrs = append(attrs, slog.String(FieldSessionID, string(subj.SessionID)))
	}
	if subj.PRNumber > 0 {
		attrs = append(attrs, slog.Int(FieldChangeRequestNumber, subj.PRNumber))
	}
	return attrs
}

func RepositoryAttrs(provider domain.SCMProvider, host, repo string, projectID domain.ProjectID) []slog.Attr {
	return []slog.Attr{
		slog.String(FieldProvider, string(provider)),
		slog.String(FieldHost, host),
		slog.String(FieldRepo, repo),
		slog.String(FieldProjectID, string(projectID)),
	}
}

func SnapshotAttrs(snap domain.SCMSnapshot) []slog.Attr {
	attrs := SubjectAttrs(snap.Subject)
	if snap.SessionID != "" && snap.Subject.SessionID == "" {
		attrs = append(attrs, slog.String(FieldSessionID, string(snap.SessionID)))
	}
	if snap.PR != nil && snap.PR.Number > 0 && snap.Subject.PRNumber == 0 {
		attrs = append(attrs, slog.Int(FieldChangeRequestNumber, snap.PR.Number))
	}
	if snap.Freshness != "" {
		attrs = append(attrs, slog.String(FieldFreshness, string(snap.Freshness)))
	}
	return attrs
}

func CommandAttrs(req ports.SCMCommandRequest) []slog.Attr {
	attrs := SubjectAttrs(req.Subject)
	attrs = append(attrs, slog.String(FieldCommand, string(req.Command)))
	if req.Actor != "" {
		attrs = append(attrs, slog.String(FieldActor, req.Actor))
	}
	if req.ChangeRequest.Number > 0 && req.Subject.PRNumber != req.ChangeRequest.Number {
		attrs = append(attrs, slog.Int(FieldChangeRequestNumber, req.ChangeRequest.Number))
	}
	return attrs
}

func RateLimitAttrs(rl *domain.SCMRateLimit) []slog.Attr {
	if rl == nil {
		return nil
	}
	attrs := []slog.Attr{
		slog.Int(FieldRateLimitLimit, rl.Limit),
		slog.Int(FieldRateLimitRemaining, rl.Remaining),
	}
	if !rl.ResetAt.IsZero() {
		attrs = append(attrs, slog.Time(FieldRateLimitReset, rl.ResetAt))
	}
	return attrs
}

func PollStateAttrs(states []domain.SCMPollState) []slog.Attr {
	var backoff time.Time
	var rateLimit time.Time
	for _, st := range states {
		if !st.BackoffUntil.IsZero() && (backoff.IsZero() || st.BackoffUntil.Before(backoff)) {
			backoff = st.BackoffUntil
		}
		if !st.RateLimitUntil.IsZero() && (rateLimit.IsZero() || st.RateLimitUntil.Before(rateLimit)) {
			rateLimit = st.RateLimitUntil
		}
	}
	attrs := []slog.Attr{}
	if !backoff.IsZero() {
		attrs = append(attrs, slog.Time(FieldBackoffUntil, backoff))
	}
	if !rateLimit.IsZero() {
		attrs = append(attrs, slog.Time(FieldRateLimitUntil, rateLimit))
	}
	return attrs
}

func ErrorAttrs(err error) []slog.Attr {
	kind := ErrorKind(err)
	attrs := []slog.Attr{}
	if kind != "" {
		attrs = append(attrs, slog.String(FieldErrorKind, string(kind)))
	}
	var se *domain.SCMError
	if errors.As(err, &se) {
		if se.StatusCode != 0 {
			attrs = append(attrs, slog.Int(FieldStatusCode, se.StatusCode))
		}
		if !se.RetryAfter.IsZero() {
			attrs = append(attrs, slog.Time(FieldRateLimitUntil, se.RetryAfter))
		}
	}
	return attrs
}

func ErrorKind(err error) domain.SCMErrorKind {
	if err == nil {
		return ""
	}
	var se *domain.SCMError
	if errors.As(err, &se) && se.Kind != "" {
		return se.Kind
	}
	return domain.SCMErrorUnavailable
}

func SCMError(err error) (*domain.SCMError, bool) {
	var se *domain.SCMError
	if errors.As(err, &se) {
		return se, true
	}
	return nil, false
}

func DurationMS(started time.Time) int64 {
	if started.IsZero() {
		return 0
	}
	return time.Since(started).Milliseconds()
}

func Freshness(snapshots []domain.SCMSnapshot, unavailable bool) domain.SCMFreshness {
	if unavailable {
		return domain.SCMFreshnessUnavailable
	}
	if len(snapshots) == 0 {
		return ""
	}
	freshness := snapshots[0].Freshness
	for _, snap := range snapshots[1:] {
		if snap.Freshness != freshness {
			return "mixed"
		}
	}
	return freshness
}

func DiagnosticFromError(operation string, err error) domain.SCMDiagnostic {
	d := domain.SCMDiagnostic{Operation: operation, ErrorKind: domain.SCMErrorUnavailable, Message: SafeDiagnosticMessage(err)}
	var se *domain.SCMError
	if errors.As(err, &se) {
		d.Operation = firstNonEmpty(se.Operation, operation)
		d.ErrorKind = se.Kind
		d.StatusCode = se.StatusCode
		d.Message = SafeDiagnosticMessage(se)
	}
	return d
}

func SafeDiagnosticMessage(err error) string {
	if err == nil {
		return ""
	}
	var se *domain.SCMError
	if errors.As(err, &se) {
		msg := se.Message
		if msg == "" && se.StatusCode != 0 {
			msg = http.StatusText(se.StatusCode)
		}
		return truncateOneLine(msg, maxDiagnosticMessage)
	}
	return "operation failed"
}

func StatusMessage(status int, _ []byte, jsonMessage string) string {
	if strings.TrimSpace(jsonMessage) != "" {
		return truncateOneLine(jsonMessage, maxDiagnosticMessage)
	}
	if text := http.StatusText(status); text != "" {
		return text
	}
	return "provider request failed"
}

func Add(attrs []slog.Attr, more ...slog.Attr) []slog.Attr {
	return append(attrs, more...)
}

func Args(attrs []slog.Attr) []any {
	args := make([]any, 0, len(attrs))
	for _, attr := range attrs {
		if attr.Key == "" {
			continue
		}
		args = append(args, attr)
	}
	return args
}

func truncateOneLine(s string, limit int) string {
	s = strings.TrimSpace(s)
	s = strings.Join(strings.Fields(s), " ")
	if limit <= 0 || len(s) <= limit {
		return s
	}
	if limit <= 1 {
		return s[:limit]
	}
	if limit <= 3 {
		return s[:limit]
	}
	return s[:limit-3] + "..."
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
