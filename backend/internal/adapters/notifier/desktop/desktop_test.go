package desktop

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

type runCall struct {
	name string
	args []string
}

type fakeExecer struct {
	lookPathArg string
	lookPathErr error
	runOutput   []byte
	runErr      error
	runCalls    []runCall
}

func (f *fakeExecer) LookPath(name string) (string, error) {
	f.lookPathArg = name
	if f.lookPathErr != nil {
		return "", f.lookPathErr
	}
	return "/usr/bin/" + name, nil
}

func (f *fakeExecer) Run(_ context.Context, name string, args ...string) ([]byte, error) {
	f.runCalls = append(f.runCalls, runCall{name: name, args: append([]string(nil), args...)})
	return f.runOutput, f.runErr
}

func TestNew_RequiresOsascriptOnPath(t *testing.T) {
	f := &fakeExecer{lookPathErr: errors.New("not found")}
	if _, err := newWithExecer(f); err == nil {
		t.Fatal("expected error when osascript missing, got nil")
	}
	if f.lookPathArg != "osascript" {
		t.Errorf("LookPath called with %q, want %q", f.lookPathArg, "osascript")
	}
}

func TestNew_OK(t *testing.T) {
	f := &fakeExecer{}
	n, err := newWithExecer(f)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n == nil {
		t.Fatal("nil notifier")
	}
}

func TestNotify_InvokesOsascriptWithPayloadAfterSeparator(t *testing.T) {
	f := &fakeExecer{}
	n, _ := newWithExecer(f)
	err := n.Notify(context.Background(), ports.OrchestratorEvent{
		Type:    "AO",
		Message: "hello",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(f.runCalls) != 1 {
		t.Fatalf("expected 1 run call, got %d", len(f.runCalls))
	}
	rc := f.runCalls[0]
	if rc.name != "osascript" {
		t.Errorf("ran %q, want osascript", rc.name)
	}
	sepIdx := indexOf(rc.args, "--")
	if sepIdx < 0 {
		t.Fatalf("no -- separator in args: %v", rc.args)
	}
	if len(rc.args)-sepIdx-1 != 2 {
		t.Fatalf("expected exactly 2 args after --, got %d (args=%v)", len(rc.args)-sepIdx-1, rc.args)
	}
	if rc.args[sepIdx+1] != "AO" || rc.args[sepIdx+2] != "hello" {
		t.Errorf("payload after --: got %q %q, want AO hello", rc.args[sepIdx+1], rc.args[sepIdx+2])
	}
}

func TestNotify_PayloadPassesVerbatim_NoEscapingNeeded(t *testing.T) {
	cases := []struct {
		name  string
		title string
		body  string
	}{
		{"quotes", `He said "hi"`, `back"to"you`},
		{"backslashes", `path\to\file`, `C:\Windows\System32`},
		{"newlines", "line1\nline2", "alpha\nbeta\ngamma"},
		{"backticks_and_dollar", "back`tick`", "$(rm -rf /)"},
		{"mixed_nasties", "\"\\$(`x`)\n", "foo\n\\\"bar"},
		{"empty", "", ""},
		{"double_dash", "--", "--"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			f := &fakeExecer{}
			n, _ := newWithExecer(f)
			if err := n.Notify(context.Background(), ports.OrchestratorEvent{Type: tc.title, Message: tc.body}); err != nil {
				t.Fatalf("notify: %v", err)
			}
			if len(f.runCalls) != 1 {
				t.Fatalf("expected 1 call, got %d", len(f.runCalls))
			}
			args := f.runCalls[0].args
			sepIdx := indexOf(args, "--")
			if sepIdx < 0 {
				t.Fatalf("no -- separator in args: %v", args)
			}

			if args[sepIdx+1] != tc.title {
				t.Errorf("title not verbatim: got %q want %q", args[sepIdx+1], tc.title)
			}
			if args[sepIdx+2] != tc.body {
				t.Errorf("body not verbatim: got %q want %q", args[sepIdx+2], tc.body)
			}

			for i := 0; i < sepIdx; i++ {
				if tc.title != "" && strings.Contains(args[i], tc.title) {
					t.Errorf("title leaked into script line [%d]: %q", i, args[i])
				}
				if tc.body != "" && strings.Contains(args[i], tc.body) {
					t.Errorf("body leaked into script line [%d]: %q", i, args[i])
				}
			}
		})
	}
}

func TestNotify_SurfacesOsascriptError(t *testing.T) {
	cases := []struct {
		name   string
		output []byte
	}{
		{"with_output", []byte("syntax error: bad thing")},
		{"no_output", nil},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			f := &fakeExecer{runErr: errors.New("exit status 1"), runOutput: tc.output}
			n, _ := newWithExecer(f)
			err := n.Notify(context.Background(), ports.OrchestratorEvent{Type: "T", Message: "M"})
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if !strings.Contains(err.Error(), "exit status 1") {
				t.Errorf("error %q does not contain underlying cause", err.Error())
			}
		})
	}
}

func indexOf(s []string, v string) int {
	for i, x := range s {
		if x == v {
			return i
		}
	}
	return -1
}
