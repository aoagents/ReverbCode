package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/gen"
)

// ProjectRow is one registered repo (the projects table). id is a short slug
// (mer, ao). ArchivedAt zero means active.
type ProjectRow struct {
	ID            string
	Path          string
	RepoOriginURL string
	DisplayName   string
	RegisteredAt  time.Time
	ArchivedAt    time.Time
}

// UpsertProject inserts or updates a registered project.
func (s *Store) UpsertProject(ctx context.Context, r ProjectRow) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return s.qw.UpsertProject(ctx, gen.UpsertProjectParams{
		ID:            r.ID,
		Path:          r.Path,
		RepoOriginUrl: r.RepoOriginURL,
		DisplayName:   r.DisplayName,
		RegisteredAt:  r.RegisteredAt,
		ArchivedAt:    nullTime(r.ArchivedAt),
	})
}

// GetProject returns a project by id (active or archived), or ok=false.
func (s *Store) GetProject(ctx context.Context, id string) (ProjectRow, bool, error) {
	p, err := s.qr.GetProject(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return ProjectRow{}, false, nil
	}
	if err != nil {
		return ProjectRow{}, false, fmt.Errorf("get project %s: %w", id, err)
	}
	return projectRowFromGen(p), true, nil
}

// ListProjects returns active (non-archived) projects, ordered by id.
func (s *Store) ListProjects(ctx context.Context) ([]ProjectRow, error) {
	rows, err := s.qr.ListProjects(ctx)
	if err != nil {
		return nil, fmt.Errorf("list projects: %w", err)
	}
	out := make([]ProjectRow, 0, len(rows))
	for _, p := range rows {
		out = append(out, projectRowFromGen(p))
	}
	return out, nil
}

// ArchiveProject soft-deletes a project (the row stays so session.project_id
// still resolves).
func (s *Store) ArchiveProject(ctx context.Context, id string, at time.Time) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return s.qw.ArchiveProject(ctx, gen.ArchiveProjectParams{
		ArchivedAt: nullTime(at),
		ID:         id,
	})
}

func projectRowFromGen(p gen.Project) ProjectRow {
	r := ProjectRow{
		ID:            p.ID,
		Path:          p.Path,
		RepoOriginURL: p.RepoOriginUrl,
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
