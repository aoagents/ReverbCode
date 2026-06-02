package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/gen"
)

// ---- sessions ----

// CreateSession assigns the per-project identity ("{project}-{num}") and inserts
// the record, returning it with ID populated. The next-num read and the insert
// run on the writer connection under writeMu, so two concurrent creates in the
// same project can't collide on num.
func (s *Store) CreateSession(ctx context.Context, rec domain.SessionRecord) (domain.SessionRecord, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	num, err := s.qw.NextSessionNum(ctx, rec.ProjectID)
	if err != nil {
		return domain.SessionRecord{}, fmt.Errorf("next session num for %s: %w", rec.ProjectID, err)
	}
	rec.ID = domain.SessionID(fmt.Sprintf("%s-%d", rec.ProjectID, num))
	if err := s.qw.InsertSession(ctx, recordToInsert(rec, num)); err != nil {
		return domain.SessionRecord{}, fmt.Errorf("insert session %s: %w", rec.ID, err)
	}
	return rec, nil
}

// UpdateSession writes the full mutable state of an existing session. The
// id/project/num/created_at are immutable and not touched here.
func (s *Store) UpdateSession(ctx context.Context, rec domain.SessionRecord) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return s.qw.UpdateSession(ctx, recordToUpdate(rec))
}

// GetSession returns the full record for a session, or ok=false if absent.
func (s *Store) GetSession(ctx context.Context, id domain.SessionID) (domain.SessionRecord, bool, error) {
	row, err := s.qr.GetSession(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.SessionRecord{}, false, nil
	}
	if err != nil {
		return domain.SessionRecord{}, false, fmt.Errorf("get session %s: %w", id, err)
	}
	return rowToRecord(row), true, nil
}

// ListSessions returns every session in a project, ordered by num.
func (s *Store) ListSessions(ctx context.Context, project domain.ProjectID) ([]domain.SessionRecord, error) {
	rows, err := s.qr.ListSessionsByProject(ctx, project)
	if err != nil {
		return nil, fmt.Errorf("list sessions for %s: %w", project, err)
	}
	return mapSessionRows(rows), nil
}

// ListAllSessions returns every session across all projects.
func (s *Store) ListAllSessions(ctx context.Context) ([]domain.SessionRecord, error) {
	rows, err := s.qr.ListAllSessions(ctx)
	if err != nil {
		return nil, fmt.Errorf("list all sessions: %w", err)
	}
	return mapSessionRows(rows), nil
}

func mapSessionRows(rows []gen.Session) []domain.SessionRecord {
	out := make([]domain.SessionRecord, 0, len(rows))
	for _, r := range rows {
		out = append(out, rowToRecord(r))
	}
	return out
}

func rowToRecord(row gen.Session) domain.SessionRecord {
	return domain.SessionRecord{
		ID:        row.ID,
		ProjectID: row.ProjectID,
		IssueID:   row.IssueID,
		Kind:      row.Kind,
		Harness:   row.Harness,
		Activity: domain.Activity{
			State:          row.ActivityState,
			LastActivityAt: row.ActivityLastAt,
		},
		IsTerminated: row.IsTerminated,
		Metadata: domain.SessionMetadata{
			Branch:          row.Branch,
			WorkspacePath:   row.WorkspacePath,
			RuntimeHandleID: row.RuntimeHandleID,
			AgentSessionID:  row.AgentSessionID,
			Prompt:          row.Prompt,
		},
		CreatedAt: row.CreatedAt,
		UpdatedAt: row.UpdatedAt,
	}
}

func recordToInsert(rec domain.SessionRecord, num int64) gen.InsertSessionParams {
	activity := normalActivity(rec.Activity, rec.CreatedAt)
	return gen.InsertSessionParams{
		ID:              rec.ID,
		ProjectID:       rec.ProjectID,
		Num:             num,
		IssueID:         rec.IssueID,
		Kind:            rec.Kind,
		Harness:         rec.Harness,
		ActivityState:   activity.State,
		ActivityLastAt:  activity.LastActivityAt,
		IsTerminated:    rec.IsTerminated,
		Branch:          rec.Metadata.Branch,
		WorkspacePath:   rec.Metadata.WorkspacePath,
		RuntimeHandleID: rec.Metadata.RuntimeHandleID,
		AgentSessionID:  rec.Metadata.AgentSessionID,
		Prompt:          rec.Metadata.Prompt,
		CreatedAt:       rec.CreatedAt,
		UpdatedAt:       rec.UpdatedAt,
	}
}

func recordToUpdate(rec domain.SessionRecord) gen.UpdateSessionParams {
	activity := normalActivity(rec.Activity, rec.UpdatedAt)
	return gen.UpdateSessionParams{
		ID:              rec.ID,
		IssueID:         rec.IssueID,
		Kind:            rec.Kind,
		Harness:         rec.Harness,
		ActivityState:   activity.State,
		ActivityLastAt:  activity.LastActivityAt,
		IsTerminated:    rec.IsTerminated,
		Branch:          rec.Metadata.Branch,
		WorkspacePath:   rec.Metadata.WorkspacePath,
		RuntimeHandleID: rec.Metadata.RuntimeHandleID,
		AgentSessionID:  rec.Metadata.AgentSessionID,
		Prompt:          rec.Metadata.Prompt,
		UpdatedAt:       rec.UpdatedAt,
	}
}

func normalActivity(a domain.Activity, fallback time.Time) domain.Activity {
	if a.State == "" {
		a.State = domain.ActivityIdle
	}
	if a.LastActivityAt.IsZero() {
		a.LastActivityAt = fallback
	}
	if a.LastActivityAt.IsZero() {
		a.LastActivityAt = time.Now().UTC()
	}
	return a
}
