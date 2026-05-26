package lifecycle

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// fakeStore is an in-memory LifecycleStore that faithfully applies merge-patch
// semantics (sparse field writes, the three-way Detecting/ClearDetecting rule,
// ExpectedRevision optimistic-concurrency check, monotonic Revision bump) so
// tests assert against the real persisted canonical.
type fakeStore struct {
	mu       sync.Mutex
	records  map[domain.SessionID]*domain.SessionRecord
	metadata map[domain.SessionID]map[string]string
}

var _ ports.LifecycleStore = (*fakeStore)(nil)

func newFakeStore() *fakeStore {
	return &fakeStore{
		records:  map[domain.SessionID]*domain.SessionRecord{},
		metadata: map[domain.SessionID]map[string]string{},
	}
}

// seed installs a starting lifecycle for a session id (bypassing the patch path).
func (s *fakeStore) seed(id domain.SessionID, l domain.CanonicalSessionLifecycle) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if l.Version == 0 {
		l.Version = domain.LifecycleVersion
	}
	s.records[id] = &domain.SessionRecord{ID: id, Lifecycle: l}
}

func (s *fakeStore) Load(_ context.Context, id domain.SessionID) (domain.CanonicalSessionLifecycle, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.records[id]
	if !ok {
		return domain.CanonicalSessionLifecycle{}, false, nil
	}
	return rec.Lifecycle, true, nil
}

func (s *fakeStore) PatchLifecycle(_ context.Context, id domain.SessionID, p ports.LifecyclePatch) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	rec, ok := s.records[id]
	if !ok {
		rec = &domain.SessionRecord{ID: id, Lifecycle: domain.CanonicalSessionLifecycle{Version: domain.LifecycleVersion}}
		s.records[id] = rec
	}
	l := &rec.Lifecycle

	if p.ExpectedRevision != nil && *p.ExpectedRevision != l.Revision {
		return fmt.Errorf("revision mismatch for %s: have %d, expected %d", id, l.Revision, *p.ExpectedRevision)
	}

	if p.Session != nil {
		l.Session = *p.Session
	}
	if p.PR != nil {
		l.PR = *p.PR
	}
	if p.Runtime != nil {
		l.Runtime = *p.Runtime
	}
	if p.Activity != nil {
		l.Activity = *p.Activity
	}
	switch {
	case p.ClearDetecting:
		l.Detecting = nil
	case p.Detecting != nil:
		d := *p.Detecting
		l.Detecting = &d
	}

	l.Version = domain.LifecycleVersion
	l.Revision++
	rec.UpdatedAt = time.Now()
	return nil
}

func (s *fakeStore) List(_ context.Context, project domain.ProjectID) ([]domain.SessionRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []domain.SessionRecord
	for _, rec := range s.records {
		if rec.ProjectID == project {
			out = append(out, *rec)
		}
	}
	return out, nil
}

func (s *fakeStore) GetMetadata(_ context.Context, id domain.SessionID) (map[string]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := map[string]string{}
	for k, v := range s.metadata[id] {
		out[k] = v
	}
	return out, nil
}

func (s *fakeStore) PatchMetadata(_ context.Context, id domain.SessionID, kv map[string]string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.metadata[id] == nil {
		s.metadata[id] = map[string]string{}
	}
	for k, v := range kv {
		s.metadata[id][k] = v
	}
	return nil
}

// recordingNotifier captures emitted events for assertions.
type recordingNotifier struct {
	mu     sync.Mutex
	events []ports.OrchestratorEvent
}

var _ ports.Notifier = (*recordingNotifier)(nil)

func (n *recordingNotifier) Notify(_ context.Context, e ports.OrchestratorEvent) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.events = append(n.events, e)
	return nil
}

// recordingMessenger captures messages injected into agents.
type recordingMessenger struct {
	mu   sync.Mutex
	sent []struct {
		ID      domain.SessionID
		Message string
	}
}

var _ ports.AgentMessenger = (*recordingMessenger)(nil)

func (a *recordingMessenger) Send(_ context.Context, id domain.SessionID, message string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.sent = append(a.sent, struct {
		ID      domain.SessionID
		Message string
	}{id, message})
	return nil
}
