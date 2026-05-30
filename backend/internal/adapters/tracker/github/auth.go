package github

import (
	"context"
	"errors"
	"os"
	"os/exec"
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

func (s StaticTokenSource) Token(context.Context) (string, error) {
	t := strings.TrimSpace(string(s))
	if t == "" {
		return "", ErrNoToken
	}
	return t, nil
}

// EnvTokenSource resolves a token from the user's environment with zero
// configuration on a stock developer machine. Lookup order:
//
//  1. Each name in EnvVars (project-configured first, e.g. AO_GITHUB_TOKEN).
//  2. The well-known GITHUB_TOKEN env var.
//  3. The `gh` CLI's auth state, via `gh auth token`. If the user has
//     already run `gh auth login`, this just works — no env var required.
//
// If step 3 errors (gh not installed, not authenticated, exec failure) the
// error is swallowed and we fall through to ErrNoToken so the caller sees
// the same "configure a token" signal regardless of why no token was found.
//
// GH is the function invoked in step 3. Production code leaves it nil and
// the default `gh auth token` exec is used. Tests inject a fake to avoid
// shelling out to a real gh binary.
type EnvTokenSource struct {
	EnvVars []string
	GH      func(ctx context.Context) (string, error)
}

func (s EnvTokenSource) Token(ctx context.Context) (string, error) {
	for _, name := range s.EnvVars {
		if v := strings.TrimSpace(os.Getenv(name)); v != "" {
			return v, nil
		}
	}
	if v := strings.TrimSpace(os.Getenv("GITHUB_TOKEN")); v != "" {
		return v, nil
	}
	gh := s.GH
	if gh == nil {
		gh = ghAuthToken
	}
	if v, err := gh(ctx); err == nil {
		if v = strings.TrimSpace(v); v != "" {
			return v, nil
		}
	}
	return "", ErrNoToken
}

// ghAuthToken shells out to the `gh` CLI and asks it for the user's
// currently logged-in token. Returns an error if gh is not installed or
// the user is not authenticated; the EnvTokenSource fallback chain
// swallows that error and reports ErrNoToken instead.
func ghAuthToken(ctx context.Context) (string, error) {
	out, err := exec.CommandContext(ctx, "gh", "auth", "token").Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}
