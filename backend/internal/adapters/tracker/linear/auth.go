package linear

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
)

// DefaultEnvVar is the env var EnvTokenSource falls back to when nothing
// project-specific is configured. Exported so callers (CLI help text,
// onboarding docs) can reference the same constant the adapter actually
// reads.
const DefaultEnvVar = "LINEAR_API_KEY"

// APIKeySettingsURL is where a user goes in the Linear web UI to mint a
// personal API key. Surfaced in error messages so a fresh dev hits a
// failed Preflight, copies the URL, and is unblocked in seconds — no
// docs hunt required.
const APIKeySettingsURL = "https://linear.app/settings/api"

// TokenSource yields a Linear personal API key on demand. Mirrors the
// GitHub adapter's TokenSource so the Session Manager only needs to know
// one shape across providers. The Tracker calls Token once at construction
// (fail-fast) and again per request so rotated tokens are picked up
// without restart.
type TokenSource interface {
	Token(ctx context.Context) (string, error)
}

// ErrNoToken is returned when no token source could yield a non-empty
// token. The message is intentionally actionable — Linear has no
// CLI-stored-token surface like gh's keyring, so the only path is a
// personal API key in an env var. Pointing the user at the settings URL
// and the env var name turns a generic "no token" failure into a
// one-step fix without grepping our docs.
var ErrNoToken = errors.New("linear tracker: no token configured — create a personal API key at " +
	APIKeySettingsURL + " and export it as " + DefaultEnvVar)

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
	if v := strings.TrimSpace(os.Getenv(DefaultEnvVar)); v != "" {
		return v, nil
	}
	// Wrap ErrNoToken so errors.Is still matches, but enumerate the env
	// vars we actually checked so the user sees what to set instead of
	// guessing. The deduped list mirrors lookup order, with the default
	// appended when it wasn't already listed.
	tried := dedup(append(append([]string{}, s.EnvVars...), DefaultEnvVar))
	return "", fmt.Errorf("%w (checked: %s)", ErrNoToken, strings.Join(tried, ", "))
}

func dedup(in []string) []string {
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, v := range in {
		if v == "" {
			continue
		}
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	return out
}
