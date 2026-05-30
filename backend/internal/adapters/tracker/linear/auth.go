package linear

import (
	"context"
	"errors"
	"os"
	"strings"
)

// TokenSource yields a Linear personal API key on demand. Mirrors the
// GitHub adapter's TokenSource so the Session Manager only needs to know
// one shape across providers. The Tracker calls Token once at construction
// (fail-fast) and again per request so rotated tokens are picked up
// without restart.
type TokenSource interface {
	Token(ctx context.Context) (string, error)
}

// ErrNoToken is returned when no token source could yield a non-empty token.
var ErrNoToken = errors.New("linear tracker: no token configured")

// StaticTokenSource is a literal token, typically used in tests.
type StaticTokenSource string

func (s StaticTokenSource) Token(context.Context) (string, error) {
	t := strings.TrimSpace(string(s))
	if t == "" {
		return "", ErrNoToken
	}
	return t, nil
}

// EnvTokenSource reads the first non-empty value from the listed env vars,
// falling back to LINEAR_API_KEY. The order matters: a project-configured
// token (e.g. AO_LINEAR_TOKEN) should be preferred over the global default.
type EnvTokenSource struct {
	EnvVars []string
}

func (s EnvTokenSource) Token(context.Context) (string, error) {
	for _, name := range s.EnvVars {
		if v := strings.TrimSpace(os.Getenv(name)); v != "" {
			return v, nil
		}
	}
	if v := strings.TrimSpace(os.Getenv("LINEAR_API_KEY")); v != "" {
		return v, nil
	}
	return "", ErrNoToken
}
