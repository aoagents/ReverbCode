package session

import "github.com/aoagents/agent-orchestrator/backend/internal/domain"

func deriveStatus(rec domain.SessionRecord, pr *domain.PRFacts) domain.SessionStatus {
	return domain.DeriveStatus(rec, pr)
}
