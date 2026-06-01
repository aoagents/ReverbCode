package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/project"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/gen"
)

var _ project.Store = (*Store)(nil)

// Upsert inserts or replaces a registered project row.
func (s *Store) Upsert(ctx context.Context, r project.Row) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return s.qw.UpsertProject(ctx, gen.UpsertProjectParams{
		ID:            domain.ProjectID(r.ID),
		Path:          r.Path,
		RepoOriginURL: r.RepoOriginURL,
		DisplayName:   r.DisplayName,
		RegisteredAt:  r.RegisteredAt,
		ArchivedAt:    nullTime(r.ArchivedAt),
	})
}

// Get returns a project by id, active or archived.
func (s *Store) Get(ctx context.Context, id string) (project.Row, bool, error) {
	p, err := s.qr.GetProject(ctx, domain.ProjectID(id))
	if errors.Is(err, sql.ErrNoRows) {
		return project.Row{}, false, nil
	}
	if err != nil {
		return project.Row{}, false, fmt.Errorf("get project %s: %w", id, err)
	}
	return projectRowFromGen(p), true, nil
}

// FindByPath returns a project registered at path, active or archived.
func (s *Store) FindByPath(ctx context.Context, path string) (project.Row, bool, error) {
	p, err := s.qr.FindProjectByPath(ctx, path)
	if errors.Is(err, sql.ErrNoRows) {
		return project.Row{}, false, nil
	}
	if err != nil {
		return project.Row{}, false, fmt.Errorf("find project by path %s: %w", path, err)
	}
	return projectRowFromGen(p), true, nil
}

// List returns active projects ordered by id.
func (s *Store) List(ctx context.Context) ([]project.Row, error) {
	rows, err := s.qr.ListProjects(ctx)
	if err != nil {
		return nil, fmt.Errorf("list projects: %w", err)
	}
	out := make([]project.Row, 0, len(rows))
	for _, p := range rows {
		out = append(out, projectRowFromGen(p))
	}
	return out, nil
}

// Archive soft-deletes a project and reports whether a row was affected.
func (s *Store) Archive(ctx context.Context, id string, at time.Time) (bool, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	n, err := s.qw.ArchiveProject(ctx, gen.ArchiveProjectParams{
		ArchivedAt: nullTime(at),
		ID:         domain.ProjectID(id),
	})
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

func projectRowFromGen(p gen.Project) project.Row {
	r := project.Row{
		ID:            string(p.ID),
		Path:          p.Path,
		RepoOriginURL: p.RepoOriginURL,
		DisplayName:   p.DisplayName,
		RegisteredAt:  p.RegisteredAt,
	}
	if p.ArchivedAt.Valid {
		r.ArchivedAt = p.ArchivedAt.Time
	}
	return r
}

func nullTime(t time.Time) sql.NullTime {
	if t.IsZero() {
		return sql.NullTime{}
	}
	return sql.NullTime{Time: t, Valid: true}
}
