package gitlab

import (
	"context"
	"errors"
	"testing"
)

// TestEnvTokenSource_EnvVarWins pins that a configured env var, when present,
// short-circuits both GITLAB_TOKEN and the glab fallback. The glab fake is
// installed to prove it is NOT consulted on the happy path.
func TestEnvTokenSource_EnvVarWins(t *testing.T) {
	t.Setenv("AO_GITLAB_TOKEN", "from-env")
	t.Setenv("GITLAB_TOKEN", "from-default-env")
	src := EnvTokenSource{
		EnvVars: []string{"AO_GITLAB_TOKEN"},
		GLAB: func(context.Context) (string, error) {
			t.Fatalf("glab fallback called even though env var was set")
			return "", nil
		},
	}
	got, err := src.Token(context.Background())
	if err != nil {
		t.Fatalf("Token: %v", err)
	}
	if got != "from-env" {
		t.Fatalf("token = %q, want from-env", got)
	}
}

// TestEnvTokenSource_DefaultEnvWins pins that GITLAB_TOKEN is used when the
// configured EnvVars list is empty or yields nothing, before falling through
// to glab.
func TestEnvTokenSource_DefaultEnvWins(t *testing.T) {
	t.Setenv("GITLAB_TOKEN", "from-default-env")
	src := EnvTokenSource{
		GLAB: func(context.Context) (string, error) {
			t.Fatalf("glab fallback called even though GITLAB_TOKEN was set")
			return "", nil
		},
	}
	got, err := src.Token(context.Background())
	if err != nil {
		t.Fatalf("Token: %v", err)
	}
	if got != "from-default-env" {
		t.Fatalf("token = %q, want from-default-env", got)
	}
}

// TestEnvTokenSource_GlabFallback pins that when no env var yields a token,
// the glab shell-out is used.
func TestEnvTokenSource_GlabFallback(t *testing.T) {
	t.Setenv("GITLAB_TOKEN", "")
	var called bool
	src := EnvTokenSource{
		GLAB: func(context.Context) (string, error) {
			called = true
			return "glpat-from-glab", nil
		},
	}
	got, err := src.Token(context.Background())
	if err != nil {
		t.Fatalf("Token: %v", err)
	}
	if !called {
		t.Fatalf("glab fallback was not called")
	}
	if got != "glpat-from-glab" {
		t.Fatalf("token = %q, want glpat-from-glab", got)
	}
}

// TestEnvTokenSource_GlabFallbackTrimsWhitespace pins that the glab stdout is
// trimmed (the real CLI ends with a newline).
func TestEnvTokenSource_GlabFallbackTrimsWhitespace(t *testing.T) {
	t.Setenv("GITLAB_TOKEN", "")
	src := EnvTokenSource{
		GLAB: func(context.Context) (string, error) {
			return "  glpat-padded\n", nil
		},
	}
	got, err := src.Token(context.Background())
	if err != nil {
		t.Fatalf("Token: %v", err)
	}
	if got != "glpat-padded" {
		t.Fatalf("token = %q, want glpat-padded", got)
	}
}

// TestEnvTokenSource_GlabFailureFallsThrough pins the silent-fallthrough
// contract: if glab errors (not installed, not logged in, etc.) the source
// returns ErrNoToken instead of propagating the exec error. This keeps the
// caller's failure mode uniform — "no token configured" — regardless of
// whether the user has glab installed.
func TestEnvTokenSource_GlabFailureFallsThrough(t *testing.T) {
	t.Setenv("GITLAB_TOKEN", "")
	src := EnvTokenSource{
		GLAB: func(context.Context) (string, error) {
			return "", errors.New("exec: glab: executable file not found in $PATH")
		},
	}
	_, err := src.Token(context.Background())
	if !errors.Is(err, ErrNoToken) {
		t.Fatalf("err = %v, want ErrNoToken (glab error should fall through silently)", err)
	}
}

// TestEnvTokenSource_GlabEmptyFallsThrough pins that an empty (whitespace-only)
// glab output is treated as "no token" rather than as a valid token of zero
// length — otherwise StaticTokenSource-style downstream checks would yield
// "no token configured" inconsistently.
func TestEnvTokenSource_GlabEmptyFallsThrough(t *testing.T) {
	t.Setenv("GITLAB_TOKEN", "")
	src := EnvTokenSource{
		GLAB: func(context.Context) (string, error) {
			return "   \n", nil
		},
	}
	_, err := src.Token(context.Background())
	if !errors.Is(err, ErrNoToken) {
		t.Fatalf("err = %v, want ErrNoToken (empty glab output should fall through)", err)
	}
}

// TestEnvTokenSource_NilGlabUsesRealExec is a smoke test: with the GLAB field
// nil and no env vars, the source attempts a real exec. We don't depend on
// glab being installed on the test machine — we just assert that the
// fallthrough produces ErrNoToken (either because glab isn't installed OR
// because it has no token to give). Either way, the public contract holds:
// "no token configured" surfaces as ErrNoToken, never as a raw exec error.
func TestEnvTokenSource_NilGlabUsesRealExec(t *testing.T) {
	t.Setenv("GITLAB_TOKEN", "")
	src := EnvTokenSource{} // GLAB nil → defaults to real exec.
	// Either glab is installed and returns a real token (rare on CI), or it
	// fails / is missing. In both failure modes we must see ErrNoToken; in
	// the rare success case we get a non-empty token. Both are valid.
	tok, err := src.Token(context.Background())
	if err != nil && !errors.Is(err, ErrNoToken) {
		t.Fatalf("err = %v, want ErrNoToken or a real token", err)
	}
	if err == nil && tok == "" {
		t.Fatalf("got empty token with nil error")
	}
}

// TestEnvTokenSource_ContextThreadedToGlab pins that the ctx is passed through
// to the GLAB hook. The github adapter's parallel work uses the same shape,
// so any future caller wiring a deadline at startup gets honored.
func TestEnvTokenSource_ContextThreadedToGlab(t *testing.T) {
	t.Setenv("GITLAB_TOKEN", "")
	type ctxKey struct{}
	parent := context.WithValue(context.Background(), ctxKey{}, "sentinel")
	src := EnvTokenSource{
		GLAB: func(ctx context.Context) (string, error) {
			if v, _ := ctx.Value(ctxKey{}).(string); v != "sentinel" {
				t.Fatalf("ctx not threaded through to GLAB hook: got %v", v)
			}
			return "tok", nil
		},
	}
	if _, err := src.Token(parent); err != nil {
		t.Fatalf("Token: %v", err)
	}
}
