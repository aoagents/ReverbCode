package service

import (
	"context"
	"fmt"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	sessionmanager "github.com/aoagents/agent-orchestrator/backend/internal/session_manager"
)

// SessionStore is the read-only persistence surface needed to assemble controller-facing session read models.
type SessionStore interface {
	GetSession(ctx context.Context, id domain.SessionID) (domain.SessionRecord, bool, error)
	ListSessions(ctx context.Context, project domain.ProjectID) ([]domain.SessionRecord, error)
	ListAllSessions(ctx context.Context) ([]domain.SessionRecord, error)
	GetDisplayPRFactsForSession(ctx context.Context, id domain.SessionID) (domain.PRFacts, bool, error)
}

// SessionListFilter captures API-facing session list query filters.
type SessionListFilter struct {
	ProjectID        domain.ProjectID
	Active           *bool
	OrchestratorOnly bool
	Fresh            bool
}

// Session is the controller-facing session service. It delegates command-side
// session operations to the internal sessionmanager.Manager and owns read-model
// assembly, including user-facing display status derivation.
type Session struct {
	manager *sessionmanager.Manager
	store   SessionStore
}

// NewSession wires a controller-facing session service over an internal session Manager.
func NewSession(manager *sessionmanager.Manager, store SessionStore) *Session {
	return &Session{manager: manager, store: store}
}

// Spawn creates a session and returns the API-facing read model.
func (s *Session) Spawn(ctx context.Context, cfg ports.SpawnConfig) (domain.Session, error) {
	rec, err := s.manager.Spawn(ctx, cfg)
	if err != nil {
		return domain.Session{}, err
	}
	return s.toSession(ctx, rec)
}

// Restore relaunches a terminated session and returns the API-facing read model.
func (s *Session) Restore(ctx context.Context, id domain.SessionID) (domain.Session, error) {
	rec, err := s.manager.Restore(ctx, id)
	if err != nil {
		return domain.Session{}, err
	}
	return s.toSession(ctx, rec)
}

// Kill delegates terminal intent and teardown to the internal manager.
func (s *Session) Kill(ctx context.Context, id domain.SessionID) (bool, error) {
	return s.manager.Kill(ctx, id)
}

// Send delegates agent messaging to the internal manager.
func (s *Session) Send(ctx context.Context, id domain.SessionID, message string) error {
	return s.manager.Send(ctx, id, message)
}

// Cleanup delegates terminal workspace cleanup to the internal manager.
func (s *Session) Cleanup(ctx context.Context, project domain.ProjectID) ([]domain.SessionID, error) {
	return s.manager.Cleanup(ctx, project)
}

// List returns sessions as enriched display models after applying API filters.
func (s *Session) List(ctx context.Context, filter SessionListFilter) ([]domain.Session, error) {
	recs, err := s.listRecords(ctx, filter.ProjectID)
	if err != nil {
		return nil, err
	}
	out := make([]domain.Session, 0, len(recs))
	for _, rec := range recs {
		if !matchesSessionFilter(rec, filter) {
			continue
		}
		sess, err := s.toSession(ctx, rec)
		if err != nil {
			return nil, err
		}
		out = append(out, sess)
	}
	return out, nil
}

func (s *Session) listRecords(ctx context.Context, project domain.ProjectID) ([]domain.SessionRecord, error) {
	if project == "" {
		recs, err := s.store.ListAllSessions(ctx)
		if err != nil {
			return nil, fmt.Errorf("list all sessions: %w", err)
		}
		return recs, nil
	}
	recs, err := s.store.ListSessions(ctx, project)
	if err != nil {
		return nil, fmt.Errorf("list %s: %w", project, err)
	}
	return recs, nil
}

func matchesSessionFilter(rec domain.SessionRecord, filter SessionListFilter) bool {
	if filter.Active != nil && rec.IsTerminated == *filter.Active {
		return false
	}
	if filter.OrchestratorOnly && rec.Kind != domain.KindOrchestrator {
		return false
	}
	if filter.Fresh && rec.IsTerminated {
		return false
	}
	return true
}

// Get returns one session as an enriched display model, or sessionmanager.ErrNotFound if it is absent.
func (s *Session) Get(ctx context.Context, id domain.SessionID) (domain.Session, error) {
	rec, ok, err := s.store.GetSession(ctx, id)
	if err != nil {
		return domain.Session{}, fmt.Errorf("get %s: %w", id, err)
	}
	if !ok {
		return domain.Session{}, fmt.Errorf("get %s: %w", id, sessionmanager.ErrNotFound)
	}
	return s.toSession(ctx, rec)
}

func (s *Session) toSession(ctx context.Context, rec domain.SessionRecord) (domain.Session, error) {
	pr, ok, err := s.store.GetDisplayPRFactsForSession(ctx, rec.ID)
	if err != nil {
		return domain.Session{}, fmt.Errorf("pr facts %s: %w", rec.ID, err)
	}
	if !ok {
		return domain.Session{SessionRecord: rec, Status: deriveStatus(rec, nil)}, nil
	}
	return domain.Session{SessionRecord: rec, Status: deriveStatus(rec, &pr)}, nil
}
