package cli

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestDoctorChecksZellijVersion(t *testing.T) {
	setConfigEnv(t)
	cmdPath := map[string]string{"git": "/bin/git", "zellij": "/bin/zellij"}
	c := &commandContext{deps: Deps{
		LookPath: func(name string) (string, error) { return cmdPath[name], nil },
		CommandOutput: func(_ context.Context, name string, args ...string) ([]byte, error) {
			if name != "/bin/zellij" || len(args) != 1 || args[0] != "--version" {
				t.Fatalf("unexpected command: %s %v", name, args)
			}
			return []byte("zellij 0.44.3\n"), nil
		},
	}.withDefaults()}

	check := findDoctorCheck(t, c.runDoctor(context.Background()), "zellij")
	if check.Level != doctorPass || !strings.Contains(check.Message, "0.44.3") {
		t.Fatalf("zellij check = %+v, want PASS with version", check)
	}
}

func TestDoctorFailsUnsupportedZellijVersion(t *testing.T) {
	setConfigEnv(t)
	cmdPath := map[string]string{"git": "/bin/git", "zellij": "/bin/zellij"}
	c := &commandContext{deps: Deps{
		LookPath: func(name string) (string, error) { return cmdPath[name], nil },
		CommandOutput: func(context.Context, string, ...string) ([]byte, error) {
			return []byte("zellij 0.44.2\n"), nil
		},
	}.withDefaults()}

	check := findDoctorCheck(t, c.runDoctor(context.Background()), "zellij")
	if check.Level != doctorFail || !strings.Contains(check.Message, "require >= 0.44.3") {
		t.Fatalf("zellij check = %+v, want FAIL with minimum version", check)
	}
}

func TestDoctorWarnsWhenZellijMissing(t *testing.T) {
	setConfigEnv(t)
	c := &commandContext{deps: Deps{
		LookPath: func(name string) (string, error) {
			if name == "git" {
				return "/bin/git", nil
			}
			return "", errors.New("missing")
		},
	}.withDefaults()}

	check := findDoctorCheck(t, c.runDoctor(context.Background()), "zellij")
	if check.Level != doctorWarn {
		t.Fatalf("zellij check = %+v, want WARN", check)
	}
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
