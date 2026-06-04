package terminal

import (
	"context"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// SessionSource supplies pre-computed session snapshots for the live
// session-state feed. The implementation is responsible for resolving the
// derived display Status (record + PR facts) before returning, so the terminal
// package does not depend on the service layer.
type SessionSource interface {
	AllSessions(ctx context.Context) ([]domain.Session, error)
	Session(ctx context.Context, id domain.SessionID) (domain.Session, bool, error)
}

// attentionLevel maps a derived session status and activity state to one of the
// six attention-zone labels the dashboard uses. The logic mirrors
// getDetailedAttentionLevel in packages/web/src/lib/types.ts, simplified to the
// facts available server-side (the PR-derived statuses are already baked into
// SessionStatus by DeriveStatus).
func attentionLevel(status domain.SessionStatus, activity domain.ActivityState) string {
	switch status {
	case domain.StatusMerged, domain.StatusTerminated:
		return "done"
	case domain.StatusMergeable, domain.StatusApproved:
		return "merge"
	case domain.StatusNeedsInput:
		return "respond"
	case domain.StatusCIFailed, domain.StatusChangesRequested:
		return "review"
	case domain.StatusReviewPending:
		return "pending"
	}
	switch activity {
	case domain.ActivityWaitingInput, domain.ActivityExited:
		return "respond"
	}
	return "working"
}

func toSessionPatch(s domain.Session) sessionPatch {
	return sessionPatch{
		ID:             string(s.ID),
		Status:         string(s.Status),
		Activity:       string(s.Activity.State),
		AttentionLevel: attentionLevel(s.Status, s.Activity.State),
		LastActivityAt: s.Activity.LastActivityAt.UTC().Format(time.RFC3339),
	}
}

func toSessionPatches(sessions []domain.Session) []sessionPatch {
	patches := make([]sessionPatch, len(sessions))
	for i, s := range sessions {
		patches[i] = toSessionPatch(s)
	}
	return patches
}
