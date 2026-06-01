package project

import (
	"context"
	"sync"
	"time"
)

// Row mirrors the project table shape from the sqlite storage PR. The
// memory store is intentionally row-based so the API layer does not depend on a
// richer mock model than the real DB will provide.
type Row struct {
	ID            string
	Path          string
	RepoOriginURL string
	DisplayName   string
	RegisteredAt  time.Time
	ArchivedAt    time.Time
}

// Store is the project persistence the manager depends on. MemoryStore is the
// current in-process implementation; the sqlite adapter uses the same row shape.
type Store interface {
	List(ctx context.Context) ([]Row, error)
	Get(ctx context.Context, id string) (Row, bool, error)
	FindByPath(ctx context.Context, path string) (Row, bool, error)
	Upsert(ctx context.Context, row Row) error
	Archive(ctx context.Context, id string, at time.Time) (bool, error)
}

// MemoryStore is the mocked DB layer for the project API implementation. It is
// process-local and intentionally small, but concurrency-safe for HTTP tests.
type MemoryStore struct {
	mu       sync.Mutex
	projects map[string]Row
	paths    map[string]string
}

var _ Store = (*MemoryStore)(nil)

// NewMemoryStore returns an empty, ready-to-use in-memory project store.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		projects: map[string]Row{},
		paths:    map[string]string{},
	}
}

// List returns all non-archived projects, in unspecified order.
func (s *MemoryStore) List(context.Context) ([]Row, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	out := make([]Row, 0, len(s.projects))
	for _, row := range s.projects {
		if row.ArchivedAt.IsZero() {
			out = append(out, row)
		}
	}
	return out, nil
}

// Get returns the project with the given id, or ok=false if absent.
func (s *MemoryStore) Get(_ context.Context, id string) (Row, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	row, ok := s.projects[id]
	if !ok {
		return Row{}, false, nil
	}
	return row, true, nil
}

// FindByPath returns the project registered at a filesystem path, or ok=false.
func (s *MemoryStore) FindByPath(_ context.Context, path string) (Row, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	id, ok := s.paths[path]
	if !ok {
		return Row{}, false, nil
	}
	row, ok := s.projects[id]
	if !ok {
		return Row{}, false, nil
	}
	return row, true, nil
}

// Upsert inserts or replaces a project, keeping the path→id index in sync.
func (s *MemoryStore) Upsert(_ context.Context, row Row) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if existing, ok := s.projects[row.ID]; ok && existing.Path != row.Path {
		delete(s.paths, existing.Path)
	}
	s.projects[row.ID] = row
	s.paths[row.Path] = row.ID
	return nil
}

// Archive soft-deletes a project by stamping ArchivedAt; returns ok=false if
// the project doesn't exist.
func (s *MemoryStore) Archive(_ context.Context, id string, at time.Time) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	row, ok := s.projects[id]
	if !ok {
		return false, nil
	}
	row.ArchivedAt = at
	s.projects[id] = row
	return true, nil
}
