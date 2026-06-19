package telemetrymeta

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
)

func ErrorKindAndCode(err error) (kind string, code string) {
	kind = "internal"
	var apiErr *apierr.Error
	if errors.As(err, &apiErr) {
		return ErrorKind(apiErr.Kind), apiErr.Code
	}
	return kind, ""
}

func ErrorKind(kind apierr.Kind) string {
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

func PanicKind(rec any) string {
	switch rec.(type) {
	case error:
		return "error"
	case string:
		return "string"
	default:
		return "other"
	}
}

func StatusFamily(status int) string {
	if status < 100 || status > 999 {
		return "unknown"
	}
	return fmt.Sprintf("%dxx", status/100)
}

func RoutePattern(r *http.Request) string {
	if r == nil {
		return ""
	}
	if rc := chi.RouteContext(r.Context()); rc != nil {
		if pattern := strings.TrimSpace(rc.RoutePattern()); pattern != "" {
			return pattern
		}
	}
	if r.URL == nil {
		return ""
	}
	return r.URL.Path
}

func Fingerprint(parts ...string) string {
	h := sha256.New()
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		_, _ = h.Write([]byte(part))
		_, _ = h.Write([]byte{0})
	}
	sum := hex.EncodeToString(h.Sum(nil))
	if len(sum) > 16 {
		return sum[:16]
	}
	return sum
}
