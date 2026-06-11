// Package review is the daemon's code-review surface: review runs against a
// session's PR and the findings they produce.
//
// This is an in-memory implementation. Execution is not yet wired to a real
// review agent — Execute records a pending run so the HTTP surface is live and
// the dashboard renders against real endpoints; agent-backed findings and
// persistence are a follow-up. Mirrors agent-orchestrator's reviews feature
// (packages/web/src/app/api/reviews) on reverbcode's daemon.
package review

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

// ErrInvalid and ErrNotFound let the HTTP layer map service failures to 422/404.
var (
	ErrInvalid  = errors.New("review: invalid input")
	ErrNotFound = errors.New("review: not found")
)

// Severity ranks a finding by how much it should block the human; one of
// "info" | "warning" | "error". Kept as a plain string on the wire.
const (
	SeverityInfo    = "info"
	SeverityWarning = "warning"
	SeverityError   = "error"
)

// Finding is one review comment produced for a run.
type Finding struct {
	ID       string `json:"id"`
	Path     string `json:"path"`
	Line     int    `json:"line"`
	Severity string `json:"severity"`
	Body     string `json:"body"`
}

// Run is one code-review execution against a session's PR.
type Run struct {
	ID        string `json:"id"`
	SessionID string `json:"sessionId"`
	// Status is one of: pending | complete | sent.
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"createdAt"`
	Findings  []Finding `json:"findings"`
}

// Manager is the reviews surface the HTTP controller depends on.
type Manager interface {
	List(ctx context.Context) ([]Run, error)
	Execute(ctx context.Context, sessionID string) (Run, error)
	Send(ctx context.Context, id string) (Run, error)
}

type memStore struct {
	mu   sync.Mutex
	runs map[string]*Run
	seq  int
}

// NewInMemory returns an in-memory Manager. Runs do not survive a daemon
// restart.
func NewInMemory() Manager {
	return &memStore{runs: map[string]*Run{}}
}

func (s *memStore) List(_ context.Context) ([]Run, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Run, 0, len(s.runs))
	for _, run := range s.runs {
		out = append(out, *run)
	}
	return out, nil
}

func (s *memStore) Execute(_ context.Context, sessionID string) (Run, error) {
	if sessionID == "" {
		return Run{}, fmt.Errorf("%w: sessionId is required", ErrInvalid)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.seq++
	run := &Run{
		ID:        fmt.Sprintf("rev-%d", s.seq),
		SessionID: sessionID,
		Status:    "pending",
		CreatedAt: time.Now().UTC(),
		Findings:  []Finding{},
	}
	s.runs[run.ID] = run
	return *run, nil
}

func (s *memStore) Send(_ context.Context, id string) (Run, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	run, ok := s.runs[id]
	if !ok {
		return Run{}, fmt.Errorf("%w: review %q", ErrNotFound, id)
	}
	run.Status = "sent"
	return *run, nil
}
