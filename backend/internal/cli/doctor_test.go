package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDoctorChecksGitVersion(t *testing.T) {
	setConfigEnv(t)
	c := doctorContext(t, map[string]string{"git": "/bin/git"}, func(_ context.Context, name string, args ...string) ([]byte, error) {
		if name != "/bin/git" || len(args) != 1 || args[0] != "--version" {
			t.Fatalf("unexpected command: %s %v", name, args)
		}
		return []byte("git version 2.43.0\n"), nil
	})

	check := findDoctorCheck(t, c.runDoctor(context.Background()), "git")
	if check.Level != doctorPass || !strings.Contains(check.Message, "2.43.0") || !strings.Contains(check.Message, "supports worktrees") {
		t.Fatalf("git check = %+v, want PASS with version", check)
	}
}

func TestDoctorWarnsOnUnsupportedGitVersion(t *testing.T) {
	setConfigEnv(t)
	c := doctorContext(t, map[string]string{"git": "/bin/git"}, func(context.Context, string, ...string) ([]byte, error) {
		return []byte("git version 2.24.9\n"), nil
	})

	check := findDoctorCheck(t, c.runDoctor(context.Background()), "git")
	if check.Level != doctorWarn || !strings.Contains(check.Message, ">= 2.25.0") {
		t.Fatalf("git check = %+v, want WARN with minimum version", check)
	}
}

func TestDoctorFailsWhenGitMissing(t *testing.T) {
	setConfigEnv(t)
	c := doctorContext(t, map[string]string{}, nil)

	check := findDoctorCheck(t, c.runDoctor(context.Background()), "git")
	if check.Level != doctorFail {
		t.Fatalf("git check = %+v, want FAIL", check)
	}
}

func TestDoctorChecksZellijVersion(t *testing.T) {
	setConfigEnv(t)
	c := doctorContext(t, map[string]string{"git": "/bin/git", "zellij": "/bin/zellij"}, func(_ context.Context, name string, args ...string) ([]byte, error) {
		switch name {
		case "/bin/git":
			return []byte("git version 2.43.0\n"), nil
		case "/bin/zellij":
			if len(args) != 1 || args[0] != "--version" {
				t.Fatalf("unexpected zellij command: %s %v", name, args)
			}
			return []byte("zellij 0.44.3\n"), nil
		default:
			t.Fatalf("unexpected command: %s %v", name, args)
			return nil, nil
		}
	})

	check := findDoctorCheck(t, c.runDoctor(context.Background()), "zellij")
	if check.Level != doctorPass || !strings.Contains(check.Message, "0.44.3") {
		t.Fatalf("zellij check = %+v, want PASS with version", check)
	}
}

func TestDoctorFailsUnsupportedZellijVersion(t *testing.T) {
	setConfigEnv(t)
	c := doctorContext(t, map[string]string{"git": "/bin/git", "zellij": "/bin/zellij"}, func(_ context.Context, name string, _ ...string) ([]byte, error) {
		if name == "/bin/git" {
			return []byte("git version 2.43.0\n"), nil
		}
		return []byte("zellij 0.44.2\n"), nil
	})

	check := findDoctorCheck(t, c.runDoctor(context.Background()), "zellij")
	if check.Level != doctorFail || !strings.Contains(check.Message, "require >= 0.44.3") {
		t.Fatalf("zellij check = %+v, want FAIL with minimum version", check)
	}
}

func TestDoctorWarnsWhenZellijMissing(t *testing.T) {
	setConfigEnv(t)
	c := doctorContext(t, map[string]string{"git": "/bin/git"}, func(context.Context, string, ...string) ([]byte, error) {
		return []byte("git version 2.43.0\n"), nil
	})

	check := findDoctorCheck(t, c.runDoctor(context.Background()), "zellij")
	if check.Level != doctorWarn {
		t.Fatalf("zellij check = %+v, want WARN", check)
	}
}

func TestDoctorChecksHarnessVersions(t *testing.T) {
	setConfigEnv(t)
	cmdPath := map[string]string{
		"git":    "/bin/git",
		"claude": "/bin/claude",
		"codex":  "/bin/codex",
	}
	c := doctorContext(t, cmdPath, func(_ context.Context, name string, args ...string) ([]byte, error) {
		switch name {
		case "/bin/git":
			return []byte("git version 2.43.0\n"), nil
		case "/bin/claude", "/bin/codex":
			if len(args) != 1 || args[0] != "--version" {
				t.Fatalf("unexpected harness command: %s %v", name, args)
			}
			return []byte(strings.TrimPrefix(name, "/bin/") + " 1.2.3\n"), nil
		default:
			t.Fatalf("unexpected command: %s %v", name, args)
			return nil, nil
		}
	})

	checks := c.runDoctor(context.Background())
	for _, name := range []string{"claude-code", "codex"} {
		check := findDoctorCheck(t, checks, name)
		if check.Level != doctorPass || !strings.Contains(check.Message, "resolves to") {
			t.Fatalf("%s check = %+v, want PASS with path/version", name, check)
		}
	}
}

func TestDoctorWarnsWhenHarnessMissing(t *testing.T) {
	setConfigEnv(t)
	c := doctorContext(t, map[string]string{"git": "/bin/git"}, func(context.Context, string, ...string) ([]byte, error) {
		return []byte("git version 2.43.0\n"), nil
	})

	check := findDoctorCheck(t, c.runDoctor(context.Background()), "codex")
	if check.Level != doctorWarn || !strings.Contains(check.Message, "not found in PATH") {
		t.Fatalf("codex check = %+v, want WARN missing binary", check)
	}
}

func TestDoctorWarnsWhenHarnessVersionFails(t *testing.T) {
	setConfigEnv(t)
	c := doctorContext(t, map[string]string{"git": "/bin/git", "codex": "/bin/codex"}, func(_ context.Context, name string, _ ...string) ([]byte, error) {
		if name == "/bin/git" {
			return []byte("git version 2.43.0\n"), nil
		}
		return nil, errors.New("boom")
	})

	check := findDoctorCheck(t, c.runDoctor(context.Background()), "codex")
	if check.Level != doctorWarn || !strings.Contains(check.Message, "failed") {
		t.Fatalf("codex check = %+v, want WARN version failure", check)
	}
}

func TestDoctorChecksGitHubTokenFromEnv(t *testing.T) {
	setConfigEnv(t)
	srv := githubDoctorServer(t, http.StatusOK, `{"login":"octocat"}`, "repo, read:org")
	c := doctorContext(t, map[string]string{"git": "/bin/git"}, func(context.Context, string, ...string) ([]byte, error) {
		return []byte("git version 2.43.0\n"), nil
	})
	t.Setenv("AO_GITHUB_TOKEN", "env-token")
	c.deps.HTTPClient = srv.Client()
	c.deps.DoctorGitHubRESTBase = srv.URL

	check := findDoctorCheck(t, c.runDoctor(context.Background()), "github-token")
	if check.Level != doctorPass || !strings.Contains(check.Message, "AO_GITHUB_TOKEN") || !strings.Contains(check.Message, "repo, read:org") {
		t.Fatalf("github-token check = %+v, want PASS with source and scopes", check)
	}
}

func TestDoctorChecksGitHubTokenFromGHCLI(t *testing.T) {
	setConfigEnv(t)
	srv := githubDoctorServer(t, http.StatusOK, `{"login":"octocat"}`, "")
	c := doctorContext(t, map[string]string{"git": "/bin/git", "gh": "/bin/gh"}, func(_ context.Context, name string, args ...string) ([]byte, error) {
		if name == "/bin/gh" {
			if len(args) != 2 || args[0] != "auth" || args[1] != "token" {
				t.Fatalf("unexpected gh command: %s %v", name, args)
			}
			return []byte("gh-token\n"), nil
		}
		return []byte("git version 2.43.0\n"), nil
	})
	c.deps.HTTPClient = srv.Client()
	c.deps.DoctorGitHubRESTBase = srv.URL

	check := findDoctorCheck(t, c.runDoctor(context.Background()), "github-token")
	if check.Level != doctorPass || !strings.Contains(check.Message, "gh token valid") {
		t.Fatalf("github-token check = %+v, want PASS from gh", check)
	}
}

func TestDoctorWarnsWhenGitHubTokenMissing(t *testing.T) {
	setConfigEnv(t)
	c := doctorContext(t, map[string]string{"git": "/bin/git"}, func(context.Context, string, ...string) ([]byte, error) {
		return []byte("git version 2.43.0\n"), nil
	})

	check := findDoctorCheck(t, c.runDoctor(context.Background()), "github-token")
	if check.Level != doctorWarn || !strings.Contains(check.Message, "no GitHub token found") {
		t.Fatalf("github-token check = %+v, want WARN missing token", check)
	}
}

func TestDoctorFailsExpiredGitHubToken(t *testing.T) {
	setConfigEnv(t)
	srv := githubDoctorServer(t, http.StatusUnauthorized, `{"message":"Bad credentials"}`, "")
	c := doctorContext(t, map[string]string{"git": "/bin/git"}, func(context.Context, string, ...string) ([]byte, error) {
		return []byte("git version 2.43.0\n"), nil
	})
	t.Setenv("GITHUB_TOKEN", "expired-token")
	c.deps.HTTPClient = srv.Client()
	c.deps.DoctorGitHubRESTBase = srv.URL

	check := findDoctorCheck(t, c.runDoctor(context.Background()), "github-token")
	if check.Level != doctorFail || !strings.Contains(check.Message, "HTTP 401") {
		t.Fatalf("github-token check = %+v, want FAIL rejected token", check)
	}
}

func TestDoctorJSONOutputIsDecodable(t *testing.T) {
	setConfigEnv(t)
	clearDoctorGitHubEnv(t)
	out, errOut, err := executeCLI(t, Deps{
		LookPath: func(name string) (string, error) {
			if name == "git" {
				return "/bin/git", nil
			}
			return "", errors.New("missing")
		},
		CommandOutput: func(context.Context, string, ...string) ([]byte, error) {
			return []byte("git version 2.43.0\n"), nil
		},
		ProcessAlive: func(int) bool { return false },
	}, "doctor", "--json")
	if err != nil {
		t.Fatalf("doctor --json failed: %v\nstderr=%s\nstdout=%s", err, errOut, out)
	}
	var got doctorReport
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("decode doctor json: %v\nout=%s", err, out)
	}
	if !got.OK || len(got.Checks) == 0 {
		t.Fatalf("doctor json = %#v, want ok with checks", got)
	}
	if findDoctorCheck(t, got.Checks, "git").Section != doctorSectionTools {
		t.Fatalf("git json check missing section: %#v", findDoctorCheck(t, got.Checks, "git"))
	}
}

func TestDoctorTextOutputIsGrouped(t *testing.T) {
	setConfigEnv(t)
	clearDoctorGitHubEnv(t)
	out, errOut, err := executeCLI(t, Deps{
		LookPath: func(name string) (string, error) {
			if name == "git" {
				return "/bin/git", nil
			}
			return "", errors.New("missing")
		},
		CommandOutput: func(context.Context, string, ...string) ([]byte, error) {
			return []byte("git version 2.43.0\n"), nil
		},
		ProcessAlive: func(int) bool { return false },
	}, "doctor")
	if err != nil {
		t.Fatalf("doctor failed: %v\nstderr=%s\nstdout=%s", err, errOut, out)
	}
	for _, want := range []string{"Core:\nPASS config:", "Tools:\nPASS git:", "Agent harnesses:\nWARN claude-code:", "WARN codex:", "GitHub:\nWARN github-token:"} {
		if !strings.Contains(out, want) {
			t.Fatalf("doctor output missing %q:\n%s", want, out)
		}
	}
}

func clearDoctorGitHubEnv(t *testing.T) {
	t.Helper()
	t.Setenv("AO_GITHUB_TOKEN", "")
	t.Setenv("GITHUB_TOKEN", "")
	t.Setenv("GH_TOKEN", "")
}

func doctorContext(t *testing.T, paths map[string]string, commandOutput func(context.Context, string, ...string) ([]byte, error)) *commandContext {
	t.Helper()
	clearDoctorGitHubEnv(t)
	deps := Deps{
		LookPath: func(name string) (string, error) {
			path, ok := paths[name]
			if !ok || path == "" {
				return "", fmt.Errorf("%s missing", name)
			}
			return path, nil
		},
		ProcessAlive: func(int) bool { return false },
	}
	if commandOutput != nil {
		deps.CommandOutput = commandOutput
	}
	return &commandContext{deps: deps.withDefaults()}
}

func githubDoctorServer(t *testing.T, status int, body, scopes string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/user" {
			t.Fatalf("unexpected github probe: %s %s", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); !strings.HasPrefix(got, "Bearer ") {
			t.Fatalf("missing bearer auth header: %q", got)
		}
		if scopes != "" {
			w.Header().Set("X-OAuth-Scopes", scopes)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}))
}

func findDoctorCheck(t *testing.T, checks []doctorCheck, name string) doctorCheck {
	t.Helper()
	for _, check := range checks {
		if check.Name == name {
			return check
		}
	}
	t.Fatalf("doctor check %q not found in %+v", name, checks)
	return doctorCheck{}
}
