// Package scm provides the PR action service, sitting between the HTTP handler
// and the SCM provider (GitHub). It owns domain-error translation so handlers
// see clean sentinel errors, not raw HTTP status codes.
package scm

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	scmgithub "github.com/aoagents/agent-orchestrator/backend/internal/adapters/scm/github"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// Sentinel errors the PRService returns; controllers map these to HTTP statuses.
var (
	ErrPRNotFound       = errors.New("scm: PR not found")
	ErrPRNotMergeable   = errors.New("scm: PR is not mergeable")
	ErrPRPreconditions  = errors.New("scm: PR merge preconditions unmet")
	ErrNothingToResolve = errors.New("scm: nothing to resolve")
)

// PRProvider is the set of SCM provider operations the PRService needs.
// *scmgithub.Provider satisfies this interface.
type PRProvider interface {
	MergePR(ctx context.Context, owner, repo string, prNumber int) error
	ListUnresolvedThreadIDs(ctx context.Context, owner, repo string, prNumber int) ([]string, error)
	ResolveThread(ctx context.Context, threadID string) error
}

// PRService implements ports.PRService over a single GitHub repository.
// owner and repo are resolved once at construction from project config.
type PRService struct {
	owner    string
	repo     string
	provider PRProvider
}

// NewPRService returns a PRService configured for the given GitHub owner/repo.
func NewPRService(owner, repo string, provider PRProvider) *PRService {
	return &PRService{owner: owner, repo: repo, provider: provider}
}

// Compile-time proof that *PRService satisfies the port.
var _ ports.PRService = (*PRService)(nil)

// Merge squash-merges the PR identified by prID (PR number as a decimal string).
func (s *PRService) Merge(ctx context.Context, prID string) (ports.MergeResult, error) {
	num, err := parsePRNumber(prID)
	if err != nil {
		return ports.MergeResult{}, ErrPRNotFound
	}
	if err := s.provider.MergePR(ctx, s.owner, s.repo, num); err != nil {
		return ports.MergeResult{}, mapMergeError(err)
	}
	return ports.MergeResult{PRNumber: num, Method: "squash"}, nil
}

// ResolveComments resolves the review threads identified by commentIDs.
// If commentIDs is empty, all unresolved threads on the PR are resolved.
func (s *PRService) ResolveComments(ctx context.Context, prID string, commentIDs []string) (ports.ResolveResult, error) {
	num, err := parsePRNumber(prID)
	if err != nil {
		return ports.ResolveResult{}, ErrPRNotFound
	}

	threadIDs := commentIDs
	if len(threadIDs) == 0 {
		ids, err := s.provider.ListUnresolvedThreadIDs(ctx, s.owner, s.repo, num)
		if err != nil {
			return ports.ResolveResult{}, mapResolveError(err)
		}
		if len(ids) == 0 {
			return ports.ResolveResult{}, ErrNothingToResolve
		}
		threadIDs = ids
	}

	var resolved int
	for _, id := range threadIDs {
		if err := s.provider.ResolveThread(ctx, id); err != nil {
			return ports.ResolveResult{}, mapResolveError(err)
		}
		resolved++
	}
	if resolved == 0 {
		return ports.ResolveResult{}, ErrNothingToResolve
	}
	return ports.ResolveResult{Resolved: resolved}, nil
}

func parsePRNumber(prID string) (int, error) {
	n, err := strconv.Atoi(prID)
	if err != nil || n <= 0 {
		return 0, fmt.Errorf("invalid PR id %q", prID)
	}
	return n, nil
}

func mapMergeError(err error) error {
	switch {
	case errors.Is(err, scmgithub.ErrNotFound):
		return ErrPRNotFound
	case errors.Is(err, scmgithub.ErrNotMergeable):
		return ErrPRNotMergeable
	case errors.Is(err, scmgithub.ErrUnprocessable):
		return ErrPRPreconditions
	default:
		return err
	}
}

func mapResolveError(err error) error {
	if errors.Is(err, scmgithub.ErrNotFound) {
		return ErrPRNotFound
	}
	return err
}
