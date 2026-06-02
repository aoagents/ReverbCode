package panep_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/messenger/inbox"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/messenger/panep"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestSatisfiesAgentMessenger(t *testing.T) {
	var _ ports.AgentMessenger = (*panep.Messenger)(nil)
}

type fakeLookup struct {
	handle    string
	workspace string
	err       error
}

func (f fakeLookup) SessionHandle(context.Context, domain.SessionID) (string, string, error) {
	return f.handle, f.workspace, f.err
}

type writeCall struct {
	handle ports.RuntimeHandle
	s      string
}

type fakeRuntime struct {
	err   error
	calls []writeCall
}

func (f *fakeRuntime) WriteChars(_ context.Context, handle ports.RuntimeHandle, s string) error {
	f.calls = append(f.calls, writeCall{handle: handle, s: s})
	return f.err
}

func TestSend_PingsPaneWithFilenameMatchingInbox(t *testing.T) {
	lookup := fakeLookup{handle: "sess-id/terminal_0", workspace: "/ws"}
	rt := &fakeRuntime{}
	fixed := time.Unix(1717171717, 42).UTC()
	m := panep.New(rt, lookup)

	// In production the composite injects the timestamp via inbox.WithTime so
	// the panep ping filename matches what inbox just wrote. Pin it here.
	ctx := inbox.WithTime(context.Background(), fixed)
	if err := m.Send(ctx, "s-1", "ε hello"); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if len(rt.calls) != 2 {
		t.Fatalf("want 2 WriteChars calls (body + newline), got %d", len(rt.calls))
	}
	// The exact filename the inbox messenger would write for the same
	// (clock, message); panep must point at it.
	wantFilename := inbox.FilenameFor(fixed, "ε hello")
	if !strings.Contains(rt.calls[0].s, wantFilename) {
		t.Errorf("ping body should reference inbox filename %q, got %q", wantFilename, rt.calls[0].s)
	}
	if !strings.Contains(rt.calls[0].s, ".ao/inbox/") {
		t.Errorf("ping body should mention .ao/inbox/, got %q", rt.calls[0].s)
	}
	if rt.calls[0].handle.ID != "sess-id/terminal_0" {
		t.Errorf("handle ID = %q, want sess-id/terminal_0", rt.calls[0].handle.ID)
	}
	if rt.calls[1].s != "\n" {
		t.Errorf("second WriteChars should be newline to submit, got %q", rt.calls[1].s)
	}
}

func TestSend_LookupErrorIsWrapped(t *testing.T) {
	lookup := fakeLookup{err: errors.New("db dead")}
	rt := &fakeRuntime{}
	m := panep.New(rt, lookup)

	err := m.Send(context.Background(), "s-1", "x")
	if err == nil {
		t.Fatal("expected error when SessionHandle fails")
	}
	if !strings.Contains(err.Error(), "db dead") {
		t.Errorf("error should wrap lookup error, got %v", err)
	}
	if len(rt.calls) != 0 {
		t.Errorf("runtime must not be called when lookup fails, got %d calls", len(rt.calls))
	}
}

func TestSend_EmptyHandleIsError(t *testing.T) {
	lookup := fakeLookup{handle: "", workspace: "/ws"}
	rt := &fakeRuntime{}
	m := panep.New(rt, lookup)

	if err := m.Send(context.Background(), "s-1", "x"); err == nil {
		t.Fatal("expected error when runtime handle is empty")
	}
	if len(rt.calls) != 0 {
		t.Errorf("runtime must not be called for empty handle, got %d calls", len(rt.calls))
	}
}

func TestSend_EmptyWorkspacePathIsError(t *testing.T) {
	// The ping body references .ao/inbox/<filename>; with no workspace path we
	// cannot trust the inbox messenger wrote a real file there.
	lookup := fakeLookup{handle: "sess-id/terminal_0", workspace: ""}
	rt := &fakeRuntime{}
	m := panep.New(rt, lookup)

	if err := m.Send(context.Background(), "s-1", "x"); err == nil {
		t.Fatal("expected error when workspace path is empty")
	}
	if len(rt.calls) != 0 {
		t.Errorf("runtime must not be called for empty workspace, got %d calls", len(rt.calls))
	}
}

func TestSend_RuntimeErrorIsWrapped(t *testing.T) {
	lookup := fakeLookup{handle: "sess-id/terminal_0", workspace: "/ws"}
	rt := &fakeRuntime{err: errors.New("zellij: pipe broken")}
	m := panep.New(rt, lookup)

	err := m.Send(context.Background(), "s-1", "x")
	if err == nil {
		t.Fatal("expected error when WriteChars fails")
	}
	if !strings.Contains(err.Error(), "zellij: pipe broken") {
		t.Errorf("error should wrap runtime error, got %v", err)
	}
}
