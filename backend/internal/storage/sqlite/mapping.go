package sqlite

import (
	"database/sql"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/gen"
)

func boolToInt(b bool) int64 {
	if b {
		return 1
	}
	return 0
}

// rowToRecord maps a stored session row to a domain record. The folded-in
// operational columns become Metadata; the canonical lifecycle is reassembled
// from the typed columns. Display status is never reconstructed here.
func rowToRecord(row gen.Session) domain.SessionRecord {
	return domain.SessionRecord{
		ID:        domain.SessionID(row.ID),
		ProjectID: domain.ProjectID(row.ProjectID),
		IssueID:   domain.IssueID(row.IssueID),
		Kind:      domain.SessionKind(row.Kind),
		Lifecycle: domain.CanonicalSessionLifecycle{
			Version:           domain.LifecycleVersion,
			Harness:           domain.AgentHarness(row.Harness),
			IsAlive:           row.IsAlive != 0,
			Session:           domain.SessionSubstate{State: domain.SessionState(row.SessionState)},
			TerminationReason: domain.TerminationReason(row.TerminationReason),
			Activity: domain.ActivitySubstate{
				State:          domain.ActivityState(row.ActivityState),
				LastActivityAt: row.ActivityLastAt,
				Source:         domain.ActivitySource(row.ActivitySource),
			},
			Detecting: nullToDetecting(row),
		},
		Metadata: domain.SessionMetadata{
			Branch:          row.Branch,
			WorkspacePath:   row.WorkspacePath,
			RuntimeHandleID: row.RuntimeHandleID,
			RuntimeName:     row.RuntimeName,
			AgentSessionID:  row.AgentSessionID,
			Prompt:          row.Prompt,
		},
		CreatedAt: row.CreatedAt,
		UpdatedAt: row.UpdatedAt,
	}
}

func recordToInsert(rec domain.SessionRecord, num int64) gen.InsertSessionParams {
	da, ds, dh := detectingToNull(rec.Lifecycle.Detecting)
	return gen.InsertSessionParams{
		ID:                    string(rec.ID),
		ProjectID:             string(rec.ProjectID),
		Num:                   num,
		IssueID:               string(rec.IssueID),
		Kind:                  string(rec.Kind),
		Harness:               string(rec.Lifecycle.Harness),
		SessionState:          string(rec.Lifecycle.Session.State),
		TerminationReason:     string(rec.Lifecycle.TerminationReason),
		IsAlive:               boolToInt(rec.Lifecycle.IsAlive),
		ActivityState:         string(rec.Lifecycle.Activity.State),
		ActivityLastAt:        rec.Lifecycle.Activity.LastActivityAt,
		ActivitySource:        string(rec.Lifecycle.Activity.Source),
		DetectingAttempts:     da,
		DetectingStartedAt:    ds,
		DetectingEvidenceHash: dh,
		Branch:                rec.Metadata.Branch,
		WorkspacePath:         rec.Metadata.WorkspacePath,
		RuntimeHandleID:       rec.Metadata.RuntimeHandleID,
		RuntimeName:           rec.Metadata.RuntimeName,
		AgentSessionID:        rec.Metadata.AgentSessionID,
		Prompt:                rec.Metadata.Prompt,
		CreatedAt:             rec.CreatedAt,
		UpdatedAt:             rec.UpdatedAt,
	}
}

func recordToUpdate(rec domain.SessionRecord) gen.UpdateSessionParams {
	da, ds, dh := detectingToNull(rec.Lifecycle.Detecting)
	return gen.UpdateSessionParams{
		IssueID:               string(rec.IssueID),
		Kind:                  string(rec.Kind),
		Harness:               string(rec.Lifecycle.Harness),
		SessionState:          string(rec.Lifecycle.Session.State),
		TerminationReason:     string(rec.Lifecycle.TerminationReason),
		IsAlive:               boolToInt(rec.Lifecycle.IsAlive),
		ActivityState:         string(rec.Lifecycle.Activity.State),
		ActivityLastAt:        rec.Lifecycle.Activity.LastActivityAt,
		ActivitySource:        string(rec.Lifecycle.Activity.Source),
		DetectingAttempts:     da,
		DetectingStartedAt:    ds,
		DetectingEvidenceHash: dh,
		Branch:                rec.Metadata.Branch,
		WorkspacePath:         rec.Metadata.WorkspacePath,
		RuntimeHandleID:       rec.Metadata.RuntimeHandleID,
		RuntimeName:           rec.Metadata.RuntimeName,
		AgentSessionID:        rec.Metadata.AgentSessionID,
		Prompt:                rec.Metadata.Prompt,
		UpdatedAt:             rec.UpdatedAt,
		ID:                    string(rec.ID),
	}
}

func detectingToNull(d *domain.DetectingState) (sql.NullInt64, sql.NullTime, sql.NullString) {
	if d == nil {
		return sql.NullInt64{}, sql.NullTime{}, sql.NullString{}
	}
	return sql.NullInt64{Int64: int64(d.Attempts), Valid: true},
		sql.NullTime{Time: d.StartedAt, Valid: true},
		sql.NullString{String: d.EvidenceHash, Valid: true}
}

func nullToDetecting(row gen.Session) *domain.DetectingState {
	if !row.DetectingAttempts.Valid {
		return nil
	}
	return &domain.DetectingState{
		Attempts:     int(row.DetectingAttempts.Int64),
		StartedAt:    row.DetectingStartedAt.Time,
		EvidenceHash: row.DetectingEvidenceHash.String,
	}
}
