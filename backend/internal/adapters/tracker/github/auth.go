package github

import (
	"context"
	"errors"
	"fmt"
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
	v, err := gh(ctx)
	if err == nil {
		if v = strings.TrimSpace(v); v != "" {
			return v, nil
		}
		// gh succeeded with empty stdout — same outcome as no token, but
		// no underlying error to carry forward. Fall through to plain ErrNoToken.
		return "", ErrNoToken
	}
	// gh failed. "Binary missing" is the uninteresting case — the user
	// simply doesn't have gh installed, and we'd be noise to surface it.
	// Anything else (not logged in, network blip, hung subprocess) carries
	// operator-useful explanation we should preserve so a daemon log shows
	// WHY zero-config auth fell through, not just THAT it did.
	if errors.Is(err, exec.ErrNotFound) {
		return "", ErrNoToken
	}
	return "", fmt.Errorf("%w (gh fallback: %v)", ErrNoToken, err)
}

// ghAuthToken shells out to the `gh` CLI and asks it for the user's
// currently logged-in token. On non-zero exit, gh writes its explanation
// ("not logged in to any GitHub hosts. Try `gh auth login`") to stderr;
// we capture it and fold it into the returned error so callers can surface
// the cause.
func ghAuthToken(ctx context.Context) (string, error) {
	cmd := exec.CommandContext(ctx, "gh", "auth", "token")
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return "", fmt.Errorf("%w: %s", err, msg)
		}
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}
