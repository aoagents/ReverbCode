package composite_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/messenger/composite"
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

func TestSend_SecondaryFailureIsLoggedAndSwallowed(t *testing.T) {
	var calls []string
	primary := &recordingMessenger{name: "primary", calls: &calls}
	secondary := &recordingMessenger{name: "secondary", err: errors.New("pipe broken"), calls: &calls}

	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}))

	c := composite.New([]ports.AgentMessenger{primary, secondary}, logger)
	if err := c.Send(context.Background(), "s-1", "hi"); err != nil {
		t.Fatalf("Send must succeed when only the secondary fails, got %v", err)
	}
	if len(calls) != 2 {
		t.Fatalf("calls = %v, want both invoked", calls)
	}
	if !strings.Contains(buf.String(), "pipe broken") {
		t.Errorf("expected secondary failure logged, got %q", buf.String())
	}
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
