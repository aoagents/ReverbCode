package service

import "github.com/aoagents/agent-orchestrator/backend/internal/domain"

func deriveStatus(rec domain.SessionRecord, pr *domain.PRFacts) domain.SessionStatus {
	if rec.IsTerminated {
		if pr != nil && pr.Merged {
			return domain.StatusMerged
		}
		return domain.StatusTerminated
	}

	if rec.Activity.State == domain.ActivityWaitingInput {
		return domain.StatusNeedsInput
	}

	if pr != nil {
		if pr.Merged {
			return domain.StatusMerged
		}
		if !pr.Closed {
			return prPipelineStatus(*pr)
		}
	}

	if rec.Activity.State == domain.ActivityActive {
		return domain.StatusWorking
	}
	return domain.StatusIdle
}

func prPipelineStatus(pr domain.PRFacts) domain.SessionStatus {
	switch {
	case pr.CI == domain.CIFailing:
		return domain.StatusCIFailed
	case pr.Draft:
		return domain.StatusDraft
	case pr.Review == domain.ReviewChangesRequest || pr.ReviewComments:
		return domain.StatusChangesRequested
	case pr.Mergeability == domain.MergeMergeable:
		return domain.StatusMergeable
	case pr.Review == domain.ReviewApproved:
		return domain.StatusApproved
	case pr.Review == domain.ReviewRequired:
		return domain.StatusReviewPending
	default:
		return domain.StatusPROpen
	}
}
