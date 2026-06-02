package composite_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/messenger/composite"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/messenger/inbox"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/messenger/panep"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestSatisfiesAgentMessenger(t *testing.T) {
	var _ ports.AgentMessenger = (*composite.Messenger)(nil)
}

type recordingMessenger struct {
	name  string
	err   error
	calls *[]string
}

func (r *recordingMessenger) Send(_ context.Context, _ domain.SessionID, _ string) error {
	*r.calls = append(*r.calls, r.name)
	return r.err
}

func nopLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestSend_FansOutInOrder(t *testing.T) {
	var calls []string
	primary := &recordingMessenger{name: "primary", calls: &calls}
	secondary := &recordingMessenger{name: "secondary", calls: &calls}

	c := composite.New([]ports.AgentMessenger{primary, secondary}, nopLogger())
	if err := c.Send(context.Background(), "s-1", "hi"); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if len(calls) != 2 {
		t.Fatalf("calls = %v, want 2", calls)
	}
	if calls[0] != "primary" || calls[1] != "secondary" {
		t.Fatalf("call order = %v, want [primary secondary]", calls)
	}
}

func TestSend_PrimaryFailureSkipsSecondaries(t *testing.T) {
	var calls []string
	primary := &recordingMessenger{name: "primary", err: errors.New("disk full"), calls: &calls}
	secondary := &recordingMessenger{name: "secondary", calls: &calls}

	c := composite.New([]ports.AgentMessenger{primary, secondary}, nopLogger())
	err := c.Send(context.Background(), "s-1", "hi")
	if err == nil {
		t.Fatal("expected error when primary fails")
	}
	if !strings.Contains(err.Error(), "disk full") {
		t.Errorf("error should surface primary failure, got %v", err)
	}
	if len(calls) != 1 || calls[0] != "primary" {
		t.Fatalf("calls = %v, want only [primary]", calls)
	}
}

func TestSend_SecondaryFailureIsLoggedAtWarnAndSwallowed(t *testing.T) {
	var calls []string
	primary := &recordingMessenger{name: "primary", calls: &calls}
	secondary := &recordingMessenger{name: "secondary", err: errors.New("pipe broken"), calls: &calls}

	rec := &levelRecorder{}
	logger := slog.New(rec)

	c := composite.New([]ports.AgentMessenger{primary, secondary}, logger)
	if err := c.Send(context.Background(), "s-1", "hi"); err != nil {
		t.Fatalf("Send must succeed when only the secondary fails, got %v", err)
	}
	if len(calls) != 2 {
		t.Fatalf("calls = %v, want both invoked", calls)
	}
	if len(rec.records) != 1 {
		t.Fatalf("want 1 log record for secondary failure, got %d", len(rec.records))
	}
	if rec.records[0].Level != slog.LevelWarn {
		t.Errorf("secondary failure logged at %v, want WARN", rec.records[0].Level)
	}
	if !strings.Contains(rec.records[0].Message+" "+rec.attrText(0), "pipe broken") {
		t.Errorf("expected secondary failure surfaced in log, got message=%q attrs=%q",
			rec.records[0].Message, rec.attrText(0))
	}
}

// levelRecorder is a slog.Handler that captures full Records so tests can
// assert on level + message + attrs, not just a serialized substring.
type levelRecorder struct {
	records []slog.Record
	attrs   [][]slog.Attr
}

func (r *levelRecorder) Enabled(context.Context, slog.Level) bool { return true }

func (r *levelRecorder) Handle(_ context.Context, rec slog.Record) error {
	var collected []slog.Attr
	rec.Attrs(func(a slog.Attr) bool {
		collected = append(collected, a)
		return true
	})
	r.records = append(r.records, rec)
	r.attrs = append(r.attrs, collected)
	return nil
}

func (r *levelRecorder) WithAttrs([]slog.Attr) slog.Handler { return r }
func (r *levelRecorder) WithGroup(string) slog.Handler      { return r }

func (r *levelRecorder) attrText(i int) string {
	var b strings.Builder
	for _, a := range r.attrs[i] {
		b.WriteString(a.Key)
		b.WriteString("=")
		b.WriteString(a.Value.String())
		b.WriteString(" ")
	}
	return b.String()
}

func TestSend_AllSecondariesAttemptedEvenIfOneFails(t *testing.T) {
	var calls []string
	primary := &recordingMessenger{name: "primary", calls: &calls}
	sec1 := &recordingMessenger{name: "sec1", err: errors.New("transient"), calls: &calls}
	sec2 := &recordingMessenger{name: "sec2", calls: &calls}

	c := composite.New([]ports.AgentMessenger{primary, sec1, sec2}, nopLogger())
	if err := c.Send(context.Background(), "s-1", "hi"); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if len(calls) != 3 || calls[0] != "primary" || calls[1] != "sec1" || calls[2] != "sec2" {
		t.Fatalf("call order = %v, want [primary sec1 sec2]", calls)
	}
}

func TestSend_EmptyInnerListIsNoOp(t *testing.T) {
	c := composite.New(nil, nopLogger())
	if err := c.Send(context.Background(), "s-1", "hi"); err != nil {
		t.Fatalf("empty composite Send should be no-op, got %v", err)
	}
}

// inboxLookup adapts a tempdir into inbox.SessionWorkspace.
type fixedWorkspace struct{ path string }

func (f fixedWorkspace) WorkspacePath(context.Context, domain.SessionID) (string, error) {
	return f.path, nil
}

// fixedSession adapts to panep.SessionLookup.
type fixedSession struct{ handle, workspace string }

func (f fixedSession) SessionHandle(context.Context, domain.SessionID) (string, string, error) {
	return f.handle, f.workspace, nil
}

type recordingRuntime struct {
	calls []string
}

func (r *recordingRuntime) WriteChars(_ context.Context, _ ports.RuntimeHandle, s string) error {
	r.calls = append(r.calls, s)
	return nil
}

// TestSend_PingFilenameMatchesOnDiskFilename is the end-to-end consistency
// proof the brief implicitly requires: a single composite.Send invocation must
// produce one inbox file on disk AND a ping body that names exactly that file.
// Without shared time, the inbox clock and panep clock fire at different
// nanoseconds and the filenames diverge.
func TestSend_PingFilenameMatchesOnDiskFilename(t *testing.T) {
	workspace := t.TempDir()
	inboxMsg := inbox.New(fixedWorkspace{path: workspace})
	rt := &recordingRuntime{}
	panepMsg := panep.New(rt, fixedSession{handle: "sess-id/terminal_0", workspace: workspace})

	c := composite.New([]ports.AgentMessenger{inboxMsg, panepMsg}, nopLogger())
	if err := c.Send(context.Background(), "s-1", "hello agent"); err != nil {
		t.Fatalf("Send: %v", err)
	}

	entries, err := os.ReadDir(filepath.Join(workspace, ".ao", "inbox"))
	if err != nil {
		t.Fatalf("read inbox dir: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("want 1 inbox file, got %d", len(entries))
	}
	onDisk := entries[0].Name()
	if len(rt.calls) == 0 {
		t.Fatal("panep did not ping the pane")
	}
	if !strings.Contains(rt.calls[0], onDisk) {
		t.Fatalf("ping body must reference the on-disk file %q, got %q", onDisk, rt.calls[0])
	}
}

func TestNew_NilLoggerStillWorks(t *testing.T) {
	// A nil logger should not panic — composite must default to a discard
	// logger so misconfigured callers don't crash on the first secondary error.
	var calls []string
	primary := &recordingMessenger{name: "primary", calls: &calls}
	secondary := &recordingMessenger{name: "secondary", err: errors.New("x"), calls: &calls}

	c := composite.New([]ports.AgentMessenger{primary, secondary}, nil)
	if err := c.Send(context.Background(), "s-1", "hi"); err != nil {
		t.Fatalf("Send with nil logger and secondary error must not return error, got %v", err)
	}
}
