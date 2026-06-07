package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/gen"
)

// UpsertProject inserts or replaces a registered project row.
func (s *Store) UpsertProject(ctx context.Context, r domain.ProjectRecord) error {
	agentConfig, err := marshalAgentConfig(r.AgentConfig)
	if err != nil {
		return err
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return s.qw.UpsertProject(ctx, gen.UpsertProjectParams{
		ID:            domain.ProjectID(r.ID),
		Path:          r.Path,
		RepoOriginURL: r.RepoOriginURL,
		DisplayName:   r.DisplayName,
		RegisteredAt:  r.RegisteredAt,
		ArchivedAt:    nullTime(r.ArchivedAt),
		AgentConfig:   agentConfig,
	})
}

// GetProject returns a project by id, active or archived.
func (s *Store) GetProject(ctx context.Context, id string) (domain.ProjectRecord, bool, error) {
	p, err := s.qr.GetProject(ctx, domain.ProjectID(id))
	if errors.Is(err, sql.ErrNoRows) {
		return domain.ProjectRecord{}, false, nil
	}
	if err != nil {
		return domain.ProjectRecord{}, false, fmt.Errorf("get project %s: %w", id, err)
	}
	r, err := projectRowFromGen(p)
	if err != nil {
		return domain.ProjectRecord{}, false, fmt.Errorf("get project %s: %w", id, err)
	}
	return r, true, nil
}

// FindProjectByPath returns a project registered at path, active or archived.
func (s *Store) FindProjectByPath(ctx context.Context, path string) (domain.ProjectRecord, bool, error) {
	p, err := s.qr.FindProjectByPath(ctx, path)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.ProjectRecord{}, false, nil
	}
	if err != nil {
		return domain.ProjectRecord{}, false, fmt.Errorf("find project by path %s: %w", path, err)
	}
	r, err := projectRowFromGen(p)
	if err != nil {
		return domain.ProjectRecord{}, false, fmt.Errorf("find project by path %s: %w", path, err)
	}
	return r, true, nil
}

// ListProjects returns active projects ordered by id.
func (s *Store) ListProjects(ctx context.Context) ([]domain.ProjectRecord, error) {
	rows, err := s.qr.ListProjects(ctx)
	if err != nil {
		return nil, fmt.Errorf("list projects: %w", err)
	}
	out := make([]domain.ProjectRecord, 0, len(rows))
	for _, p := range rows {
		r, err := projectRowFromGen(p)
		if err != nil {
			return nil, fmt.Errorf("list projects: %w", err)
		}
		out = append(out, r)
	}
	return out, nil
}

// ArchiveProject soft-deletes a project and reports whether a row was affected.
func (s *Store) ArchiveProject(ctx context.Context, id string, at time.Time) (bool, error) {
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

func projectRowFromGen(p gen.Project) (domain.ProjectRecord, error) {
	agentConfig, err := unmarshalAgentConfig(p.AgentConfig)
	if err != nil {
		return domain.ProjectRecord{}, err
	}
	r := domain.ProjectRecord{
		ID:            string(p.ID),
		Path:          p.Path,
		RepoOriginURL: p.RepoOriginURL,
		DisplayName:   p.DisplayName,
		RegisteredAt:  p.RegisteredAt,
		AgentConfig:   agentConfig,
	}
	if p.ArchivedAt.Valid {
		r.ArchivedAt = p.ArchivedAt.Time
	}
	return r, nil
}

// marshalAgentConfig encodes the typed per-project agent config into the
// nullable JSON column. An IsZero config stores SQL NULL so an unset config
// round-trips back to a zero value rather than an empty object.
func marshalAgentConfig(cfg domain.AgentConfig) (sql.NullString, error) {
	if cfg.IsZero() {
		return sql.NullString{}, nil
	}
	data, err := json.Marshal(cfg)
	if err != nil {
		return sql.NullString{}, fmt.Errorf("marshal agent config: %w", err)
	}
	return sql.NullString{String: string(data), Valid: true}, nil
}

// unmarshalAgentConfig decodes the nullable JSON column back into the typed
// struct. SQL NULL (an unset config) decodes to a zero value.
func unmarshalAgentConfig(s sql.NullString) (domain.AgentConfig, error) {
	if !s.Valid || s.String == "" {
		return domain.AgentConfig{}, nil
	}
	var cfg domain.AgentConfig
	if err := json.Unmarshal([]byte(s.String), &cfg); err != nil {
		return domain.AgentConfig{}, fmt.Errorf("unmarshal agent config: %w", err)
	}
	return cfg, nil
}

func nullTime(t time.Time) sql.NullTime {
	if t.IsZero() {
		return sql.NullTime{}
	}
	return sql.NullTime{Time: t, Valid: true}
}
