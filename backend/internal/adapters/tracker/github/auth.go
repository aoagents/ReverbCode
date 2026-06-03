package github

import (
	"context"
	"errors"
	"os"
	"strings"
)

// TokenSource yields a GitHub bearer token on demand. It is intentionally
// tiny so tests can inject a static token and production can layer env-var or
// gh-CLI fallbacks behind the same surface. The Tracker calls Token once at
// construction (fail-fast) and again per request (so rotated tokens are
// picked up without restart).
type TokenSource interface {
	Token(ctx context.Context) (string, error)
}

// ErrNoToken is returned when no token source could yield a non-empty token.
var ErrNoToken = errors.New("github tracker: no token configured")

// StaticTokenSource is a literal token, typically used in tests.
type StaticTokenSource string

// Token returns the literal token, or ErrNoToken if it is blank.
func (s StaticTokenSource) Token(context.Context) (string, error) {
	t := strings.TrimSpace(string(s))
	if t == "" {
		return "", ErrNoToken
	}
	return t, nil
}

// EnvTokenSource reads the first non-empty value from the listed env vars,
// falling back to GITHUB_TOKEN. The order matters: a project-configured
// token (e.g. AO_GITHUB_TOKEN) should be preferred over the global default,
// matching the pattern PR #28 uses on the SCM side so both adapters honor
// the same precedence.
type EnvTokenSource struct {
	EnvVars []string
}

// Token returns the first non-empty configured env var (falling back to
// GITHUB_TOKEN), or ErrNoToken if none is set.
func (s EnvTokenSource) Token(context.Context) (string, error) {
	for _, name := range s.EnvVars {
		if v := strings.TrimSpace(os.Getenv(name)); v != "" {
			return v, nil
		}
	}
	if v := strings.TrimSpace(os.Getenv("GITHUB_TOKEN")); v != "" {
		return v, nil
	}
	return "", ErrNoToken
}
