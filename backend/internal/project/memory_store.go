package project

import (
	"context"
	"sync"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

type Store interface {
	List(ctx context.Context) ([]Project, error)
	Get(ctx context.Context, id domain.ProjectID) (Project, bool, error)
	FindByPath(ctx context.Context, path string) (Project, bool, error)
	Create(ctx context.Context, p Project) error
	Update(ctx context.Context, p Project) error
	Delete(ctx context.Context, id domain.ProjectID) (bool, error)
}

// MemoryStore is the mocked DB layer for the project API implementation. It is
// process-local and intentionally small, but concurrency-safe for HTTP tests.
type MemoryStore struct {
	mu       sync.Mutex
	projects map[domain.ProjectID]Project
	paths    map[string]domain.ProjectID
}

var _ Store = (*MemoryStore)(nil)

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		projects: map[domain.ProjectID]Project{},
		paths:    map[string]domain.ProjectID{},
	}
}

func (s *MemoryStore) List(context.Context) ([]Project, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	out := make([]Project, 0, len(s.projects))
	for _, p := range s.projects {
		out = append(out, cloneProject(p))
	}
	return out, nil
}

func (s *MemoryStore) Get(_ context.Context, id domain.ProjectID) (Project, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	p, ok := s.projects[id]
	if !ok {
		return Project{}, false, nil
	}
	return cloneProject(p), true, nil
}

func (s *MemoryStore) FindByPath(_ context.Context, path string) (Project, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	id, ok := s.paths[path]
	if !ok {
		return Project{}, false, nil
	}
	p, ok := s.projects[id]
	if !ok {
		return Project{}, false, nil
	}
	return cloneProject(p), true, nil
}

func (s *MemoryStore) Create(_ context.Context, p Project) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.projects[p.ID]; ok {
		return conflict("ID_ALREADY_REGISTERED", "A project with this id is already registered for a different path", nil)
	}
	if existingID, ok := s.paths[p.Path]; ok {
		return conflict("PATH_ALREADY_REGISTERED", "A project at this path is already registered", map[string]any{
			"existingProjectId": string(existingID),
		})
	}
	s.projects[p.ID] = cloneProject(p)
	s.paths[p.Path] = p.ID
	return nil
}

func (s *MemoryStore) Update(_ context.Context, p Project) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.projects[p.ID]; !ok {
		return notFound("PROJECT_NOT_FOUND", "Unknown project")
	}
	s.projects[p.ID] = cloneProject(p)
	s.paths[p.Path] = p.ID
	return nil
}

func (s *MemoryStore) Delete(_ context.Context, id domain.ProjectID) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	p, ok := s.projects[id]
	if !ok {
		return false, nil
	}
	delete(s.projects, id)
	delete(s.paths, p.Path)
	return true, nil
}

func cloneProject(p Project) Project {
	if p.Tracker != nil {
		tracker := *p.Tracker
		p.Tracker = &tracker
	}
	if p.SCM != nil {
		scm := *p.SCM
		if p.SCM.Webhook != nil {
			webhook := *p.SCM.Webhook
			scm.Webhook = &webhook
		}
		p.SCM = &scm
	}
	if p.Reactions != nil {
		reactions := make(map[string]*ReactionConfig, len(p.Reactions))
		for k, v := range p.Reactions {
			if v == nil {
				reactions[k] = nil
				continue
			}
			reaction := *v
			reactions[k] = &reaction
		}
		p.Reactions = reactions
	}
	return p
}
