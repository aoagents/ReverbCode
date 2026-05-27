package tmux

import (
	"context"
	"errors"
	"os/exec"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestNewDefaultsToPortableShell(t *testing.T) {
	t.Setenv("SHELL", "")
	r := New(Options{})
	if got, want := r.shell, "/bin/sh"; got != want {
		t.Fatalf("default shell = %q, want %q", got, want)
	}
}

func TestCommandBuilders(t *testing.T) {
	if got, want := newSessionArgs("sess-1", "/tmp/ws", "/bin/zsh", "echo hi"), []string{"new-session", "-d", "-s", "sess-1", "-c", "/tmp/ws", "/bin/zsh", "-lc", "echo hi"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("newSessionArgs = %#v, want %#v", got, want)
	}
	if got, want := setStatusOffArgs("sess-1"), []string{"set-option", "-t", "=sess-1:", "status", "off"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("setStatusOffArgs = %#v, want %#v", got, want)
	}
	if got, want := capturePaneArgs("sess-1", 42), []string{"capture-pane", "-p", "-t", "=sess-1:0.0", "-S", "-42"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("capturePaneArgs = %#v, want %#v", got, want)
	}
}

func TestExactTargets(t *testing.T) {
	if got, want := exactSessionTarget("abc"), "=abc:"; got != want {
		t.Fatalf("exactSessionTarget = %q, want %q", got, want)
	}
	if got, want := exactPaneTarget("abc"), "=abc:0.0"; got != want {
		t.Fatalf("exactPaneTarget = %q, want %q", got, want)
	}
}

func TestTmuxSessionNameSanitizesIssueRefs(t *testing.T) {
	got, err := tmuxSessionName("repo/issue#42.1")
	if err != nil {
		t.Fatalf("tmuxSessionName: %v", err)
	}
	if err := validateSessionID(got); err != nil {
		t.Fatalf("sanitized id %q is invalid: %v", got, err)
	}
	if !strings.HasPrefix(got, "repo-issue-42-1-") {
		t.Fatalf("sanitized id = %q, want readable prefix", got)
	}
	if got == "repo/issue#42.1" {
		t.Fatal("sanitized id still contains raw unsafe characters")
	}
}

func TestValidateSessionID(t *testing.T) {
	valid := []string{"sess-1", "S_2", "abc123"}
	for _, id := range valid {
		if err := validateSessionID(id); err != nil {
			t.Fatalf("validateSessionID(%q): %v", id, err)
		}
	}
	invalid := []string{"", "sess.1", "sess/1", "$(boom)", "with space"}
	for _, id := range invalid {
		if err := validateSessionID(id); err == nil {
			t.Fatalf("validateSessionID(%q): got nil, want error", id)
		}
	}
}

func TestWrapLaunchCommandExportsEnvAndKeepsPaneAlive(t *testing.T) {
	oldGetenv := getenv
	getenv = func(key string) string {
		if key == "PATH" {
			return "/usr/bin:/bin"
		}
		return ""
	}
	defer func() { getenv = oldGetenv }()

	got := wrapLaunchCommand(ports.RuntimeConfig{LaunchCommand: "ao run", Env: map[string]string{
		"AO_SESSION_ID": "sess-1",
		"ODD":           "can't",
		"PATH":          "/custom/bin:/usr/bin",
	}}, "/bin/zsh")

	for _, want := range []string{
		"export AO_SESSION_ID='sess-1';",
		"export ODD='can'\\''t';",
		"export PATH='/custom/bin:/usr/bin';",
		"ao run; exec '/bin/zsh' -i",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("wrapped command missing %q in %q", want, got)
		}
	}
}

func TestCreateRunsNewSessionAndDisablesStatus(t *testing.T) {
	fr := &fakeRunner{}
	r := New(Options{Binary: "tmux-test", Timeout: time.Second, Shell: "/bin/zsh"})
	r.runner = fr

	handle, err := r.Create(context.Background(), ports.RuntimeConfig{
		SessionID:     "sess-1",
		WorkspacePath: "/tmp/ws",
		LaunchCommand: "echo ready",
		Env:           map[string]string{"AO_SESSION_ID": "sess-1"},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if handle != (ports.RuntimeHandle{ID: "sess-1", RuntimeName: runtimeName}) {
		t.Fatalf("handle = %+v, want tmux handle", handle)
	}
	if len(fr.calls) != 2 {
		t.Fatalf("calls = %d, want 2", len(fr.calls))
	}
	if got, want := fr.calls[0].args[:6], []string{"new-session", "-d", "-s", "sess-1", "-c", "/tmp/ws"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("create args prefix = %#v, want %#v", got, want)
	}
	if got, want := fr.calls[1].args, setStatusOffArgs("sess-1"); !reflect.DeepEqual(got, want) {
		t.Fatalf("status args = %#v, want %#v", got, want)
	}
}

func TestCreateNormalizesUnsafeSessionID(t *testing.T) {
	fr := &fakeRunner{}
	r := New(Options{Binary: "tmux-test", Timeout: time.Second, Shell: "/bin/sh"})
	r.runner = fr

	handle, err := r.Create(context.Background(), ports.RuntimeConfig{
		SessionID:     "repo/issue#42",
		WorkspacePath: "/tmp/ws",
		LaunchCommand: "echo ready",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := validateSessionID(handle.ID); err != nil {
		t.Fatalf("handle id %q invalid: %v", handle.ID, err)
	}
	if handle.ID == "repo/issue#42" {
		t.Fatal("handle kept unsafe raw session id")
	}
	if got := fr.calls[0].args[3]; got != handle.ID {
		t.Fatalf("tmux session arg = %q, want handle id %q", got, handle.ID)
	}
}

func TestSendMessageUsesLiteralForShortInput(t *testing.T) {
	fr := &fakeRunner{}
	r := New(Options{Timeout: time.Second})
	r.runner = fr

	if err := r.SendMessage(context.Background(), ports.RuntimeHandle{ID: "sess-1", RuntimeName: runtimeName}, "hello"); err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	if got, want := fr.calls[0].args, sendLiteralArgs("sess-1", "hello"); !reflect.DeepEqual(got, want) {
		t.Fatalf("literal args = %#v, want %#v", got, want)
	}
	if got, want := fr.calls[1].args, sendEnterArgs("sess-1"); !reflect.DeepEqual(got, want) {
		t.Fatalf("enter args = %#v, want %#v", got, want)
	}
}

func TestSendMessageUsesBufferForMultilineInput(t *testing.T) {
	fr := &fakeRunner{}
	r := New(Options{Timeout: time.Second})
	r.runner = fr

	if err := r.SendMessage(context.Background(), ports.RuntimeHandle{ID: "sess-1", RuntimeName: runtimeName}, "hello\nworld"); err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	if len(fr.calls) != 3 {
		t.Fatalf("calls = %d, want 3", len(fr.calls))
	}
	if fr.calls[0].args[0] != "load-buffer" {
		t.Fatalf("first command = %#v, want load-buffer", fr.calls[0].args)
	}
	if got := fr.calls[1].args; !reflect.DeepEqual(got[:4], []string{"paste-buffer", "-d", "-t", "=sess-1:0.0"}) {
		t.Fatalf("paste args = %#v", got)
	}
	if got, want := fr.calls[2].args, sendEnterArgs("sess-1"); !reflect.DeepEqual(got, want) {
		t.Fatalf("enter args = %#v, want %#v", got, want)
	}
}

func TestIsAliveTreatsExitStatusAsNotAlive(t *testing.T) {
	fr := &fakeRunner{err: &exec.ExitError{}}
	r := New(Options{Timeout: time.Second})
	r.runner = fr

	alive, err := r.IsAlive(context.Background(), ports.RuntimeHandle{ID: "sess-1", RuntimeName: runtimeName})
	if err != nil {
		t.Fatalf("IsAlive: %v", err)
	}
	if alive {
		t.Fatal("alive = true, want false")
	}
}

func TestDestroyIsIdempotentWhenSessionMissing(t *testing.T) {
	fr := &fakeRunner{err: &exec.ExitError{}}
	r := New(Options{Timeout: time.Second})
	r.runner = fr

	if err := r.Destroy(context.Background(), ports.RuntimeHandle{ID: "sess-1", RuntimeName: runtimeName}); err != nil {
		t.Fatalf("Destroy: %v", err)
	}
	if len(fr.calls) != 1 || fr.calls[0].args[0] != "kill-session" {
		t.Fatalf("calls = %#v, want only kill-session", fr.calls)
	}
}

func TestGetOutputValidatesLines(t *testing.T) {
	r := New(Options{Timeout: time.Second})
	_, err := r.GetOutput(context.Background(), ports.RuntimeHandle{ID: "sess-1", RuntimeName: runtimeName}, 0)
	if err == nil {
		t.Fatal("GetOutput lines=0: got nil, want error")
	}
}

type fakeRunner struct {
	calls []runnerCall
	out   []byte
	err   error
}

type runnerCall struct {
	name string
	args []string
}

func (f *fakeRunner) Run(_ context.Context, name string, args ...string) ([]byte, error) {
	f.calls = append(f.calls, runnerCall{name: name, args: append([]string(nil), args...)})
	if f.err != nil {
		return f.out, f.err
	}
	return f.out, nil
}

func TestCommandErrorUnwraps(t *testing.T) {
	base := errors.New("base")
	err := commandError{err: base, output: "details"}
	if !errors.Is(err, base) {
		t.Fatal("commandError should unwrap base error")
	}
	if !strings.Contains(err.Error(), "details") {
		t.Fatalf("error = %q, want output details", err.Error())
	}
}
