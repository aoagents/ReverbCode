package github

import (
	"context"
	"fmt"
	"net/http"
	"strconv"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// PostPRReview posts an AO code-review result to a PR as a GitHub pull-request
// review. The verdict maps to the review event so the result lands in the
// review-decision path the worker already consumes through the SCM observer.
func (p *Provider) PostPRReview(ctx context.Context, prURL string, verdict domain.ReviewVerdict, body string) error {
	owner, repo, number, err := parsePRURL(prURL)
	if err != nil {
		return err
	}
	event, err := reviewEvent(verdict)
	if err != nil {
		return err
	}
	payload := map[string]any{"event": event, "body": body}
	_, err = p.client.doREST(ctx, http.MethodPost, repoPath(owner, repo, "pulls", strconv.Itoa(number), "reviews"), nil, payload)
	if err != nil {
		return fmt.Errorf("github scm: post review on %s: %w", prURL, err)
	}
	return nil
}

// reviewEvent maps an AO verdict onto a GitHub review event.
func reviewEvent(verdict domain.ReviewVerdict) (string, error) {
	switch verdict {
	case domain.VerdictApproved:
		return "APPROVE", nil
	case domain.VerdictChangesRequested:
		return "REQUEST_CHANGES", nil
	default:
		return "", fmt.Errorf("github scm: unsupported review verdict %q", verdict)
	}
}
