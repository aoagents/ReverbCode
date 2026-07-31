package gitlab

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"strings"
)

// TokenSource yields a GitLab personal-access (or project-access) token on
// demand. Mirrors the GitHub adapter's surface so daemon wiring is uniform
// across providers: the Tracker calls Token once at construction (fail-fast)
// and again per request (so rotated tokens are picked up without restart).
type TokenSource interface {
	Token(ctx context.Context) (string, error)
}

// ErrNoToken is returned when no token source could yield a non-empty token.
var ErrNoToken = errors.New("gitlab tracker: no token configured")

// StaticTokenSource is a literal token, typically used in tests.
type StaticTokenSource string

func (s StaticTokenSource) Token(context.Context) (string, error) {
	t := strings.TrimSpace(string(s))
	if t == "" {
		return "", ErrNoToken
	}
	return t, nil
}

// EnvTokenSource resolves a token from, in order:
//
//  1. The configured EnvVars (first non-empty wins).
//  2. GITLAB_TOKEN.
//  3. `glab auth token` (the GitLab CLI), if installed.
//
// The glab fallback is on by default — there is no opt-in flag. If glab is
// not installed, fails, or returns nothing, the source falls through silently
// to ErrNoToken; the exec error is NOT propagated, so the caller's failure
// mode is uniform ("no token configured") regardless of whether the user has
// glab installed. This matches the zero-friction directive AO-26: a user
// who has already done `glab auth login` gets working credentials with no
// extra setup, while CI environments using explicit env vars are unaffected.
//
// GLAB is the injection seam for tests. Production callers leave it nil and
// the source uses the real `glab auth token` exec; tests replace it with a
// fake to assert lookup order and contract behavior without touching $PATH.
// The signature mirrors the GitHub adapter's parallel `gh auth token` hook
// so both adapters' auth.go shapes stay parallel.
type EnvTokenSource struct {
	EnvVars []string
	GLAB    func(ctx context.Context) (string, error)
}

func (s EnvTokenSource) Token(ctx context.Context) (string, error) {
	for _, name := range s.EnvVars {
		if v := strings.TrimSpace(os.Getenv(name)); v != "" {
			return v, nil
		}
	}
	if v := strings.TrimSpace(os.Getenv("GITLAB_TOKEN")); v != "" {
		return v, nil
	}
	fn := s.GLAB
	if fn == nil {
		fn = defaultGlabToken
	}
	// Silent fallthrough: any error or empty output from glab maps to
	// ErrNoToken. We intentionally do NOT surface the exec error — a missing
	// CLI binary is the common case and shouldn't look like a configuration
	// problem to the caller.
	if tok, err := fn(ctx); err == nil {
		if trimmed := strings.TrimSpace(tok); trimmed != "" {
			return trimmed, nil
		}
	}
	return "", ErrNoToken
}

// defaultGlabToken shells out to `glab auth token`. The ctx is threaded
// through so a caller wiring a startup deadline gets honored. Any error
// (binary missing, not logged in, glab unhappy for any reason) is returned
// for the caller to discard — see EnvTokenSource.Token's silent-fallthrough
// rule. Stderr is discarded so an unauthenticated glab doesn't print noise
// during a fallthrough that the caller has chosen to handle silently.
func defaultGlabToken(ctx context.Context) (string, error) {
	cmd := exec.CommandContext(ctx, "glab", "auth", "token")
	cmd.Stderr = io.Discard
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return string(out), nil
}
