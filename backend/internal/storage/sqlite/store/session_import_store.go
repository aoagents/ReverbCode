package store

import (
	"context"
	"fmt"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// ImportSession inserts a session with a caller-supplied id and num, bypassing
// CreateSession's per-project num generation so the legacy importer can preserve
// a verbatim id (e.g. "{prefix}-orchestrator", num 0). It is idempotent: an id
// that already exists is left untouched and inserted=false is returned, so a
// re-run of the importer never clobbers a row the daemon may since have evolved.
//
// Like CreateSession this is a single INSERT under writeMu; the ON CONFLICT
// guard makes the existence check and the insert atomic on the writer
// connection. It uses raw ExecContext to attach the ON CONFLICT clause the
// generated InsertSession query does not carry (the same raw-exec approach
// DeleteSession uses to work around sqlc's DELETE handling).
func (s *Store) ImportSession(ctx context.Context, rec domain.SessionRecord, num int64) (bool, error) {
	activity := normalActivity(rec.Activity, rec.CreatedAt)
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	res, err := s.writeDB.ExecContext(ctx, `
INSERT INTO sessions (
    id, project_id, num, issue_id, kind, harness, display_name,
    activity_state, activity_last_at, first_signal_at, is_terminated,
    branch, workspace_path, runtime_handle_id, agent_session_id, prompt,
    created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(id) DO NOTHING`,
		rec.ID,
		rec.ProjectID,
		num,
		rec.IssueID,
		rec.Kind,
		rec.Harness,
		rec.DisplayName,
		activity.State,
		activity.LastActivityAt,
		timeToNullTime(rec.FirstSignalAt),
		rec.IsTerminated,
		rec.Metadata.Branch,
		rec.Metadata.WorkspacePath,
		rec.Metadata.RuntimeHandleID,
		rec.Metadata.AgentSessionID,
		rec.Metadata.Prompt,
		rec.CreatedAt,
		rec.UpdatedAt,
	)
	if err != nil {
		return false, fmt.Errorf("import session %s: %w", rec.ID, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("import session %s: rows affected: %w", rec.ID, err)
	}
	return n > 0, nil
}
