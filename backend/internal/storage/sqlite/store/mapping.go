package store

import (
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/gen"
)

func rowToRecord(row gen.Session) domain.SessionRecord {
	return domain.SessionRecord{
		ID:        domain.SessionID(row.ID),
		ProjectID: domain.ProjectID(row.ProjectID),
		IssueID:   domain.IssueID(row.IssueID),
		Kind:      row.Kind,
		Harness:   row.Harness,
		Activity: domain.ActivitySubstate{
			State:          row.ActivityState,
			LastActivityAt: row.ActivityLastAt,
			Source:         row.ActivitySource,
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
		ID:              string(rec.ID),
		ProjectID:       string(rec.ProjectID),
		Num:             num,
		IssueID:         string(rec.IssueID),
		Kind:            rec.Kind,
		Harness:         rec.Harness,
		ActivityState:   activity.State,
		ActivityLastAt:  activity.LastActivityAt,
		ActivitySource:  activity.Source,
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
		ID:              string(rec.ID),
		IssueID:         string(rec.IssueID),
		Kind:            rec.Kind,
		Harness:         rec.Harness,
		ActivityState:   activity.State,
		ActivityLastAt:  activity.LastActivityAt,
		ActivitySource:  activity.Source,
		IsTerminated:    rec.IsTerminated,
		Branch:          rec.Metadata.Branch,
		WorkspacePath:   rec.Metadata.WorkspacePath,
		RuntimeHandleID: rec.Metadata.RuntimeHandleID,
		AgentSessionID:  rec.Metadata.AgentSessionID,
		Prompt:          rec.Metadata.Prompt,
		UpdatedAt:       rec.UpdatedAt,
	}
}

func normalActivity(a domain.ActivitySubstate, fallback time.Time) domain.ActivitySubstate {
	if a.State == "" {
		a.State = domain.ActivityIdle
	}
	if a.Source == "" {
		a.Source = domain.SourceNone
	}
	if a.LastActivityAt.IsZero() {
		a.LastActivityAt = fallback
	}
	if a.LastActivityAt.IsZero() {
		a.LastActivityAt = time.Now().UTC()
	}
	return a
}

func boolToInt(v bool) int64 {
	if v {
		return 1
	}
	return 0
}
