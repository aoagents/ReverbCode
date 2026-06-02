package pr

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	scmgithub "github.com/aoagents/agent-orchestrator/backend/internal/adapters/scm/github"
)

var (
	ErrPRNotFound       = errors.New("pr: not found")
	ErrPRNotMergeable   = errors.New("pr: not mergeable")
	ErrPRPreconditions  = errors.New("pr: merge preconditions unmet")
	ErrNothingToResolve = errors.New("pr: nothing to resolve")
)

// ActionManager is the controller-facing contract for /prs/{id} action routes.
type ActionManager interface {
	// Merge squash-merges the PR identified by prID (its number as a decimal string).
	Merge(ctx context.Context, prID string) (MergeResult, error)

	// ResolveComments resolves the review threads identified by commentIDs
	// (GitHub review thread node IDs). If commentIDs is empty, all unresolved
	// threads on the PR are resolved.
	ResolveComments(ctx context.Context, prID string, commentIDs []string) (ResolveResult, error)
}

// MergeResult is the successful outcome of a PR merge.
type MergeResult struct {
	PRNumber int
	Method   string // always "squash"
}

// ResolveResult is the successful outcome of a resolve-comments operation.
type ResolveResult struct {
	Resolved int
}

// PRProvider is the set of SCM adapter operations ActionService needs.
// *scmgithub.Provider satisfies this interface.
type PRProvider interface {
	MergePR(ctx context.Context, owner, repo string, prNumber int) error
	ListUnresolvedThreadIDs(ctx context.Context, owner, repo string, prNumber int) ([]string, error)
	ResolveThread(ctx context.Context, threadID string) error
}

// ActionService implements ActionManager over a single GitHub repository.
// owner and repo are resolved once at construction from the project config.
type ActionService struct {
	owner    string
	repo     string
	provider PRProvider
}

var _ ActionManager = (*ActionService)(nil)

// NewActionService returns an ActionService configured for the given GitHub owner/repo.
func NewActionService(owner, repo string, provider PRProvider) *ActionService {
	return &ActionService{owner: owner, repo: repo, provider: provider}
}

// Merge squash-merges the PR identified by prID (PR number as a decimal string).
func (s *ActionService) Merge(ctx context.Context, prID string) (MergeResult, error) {
	num, err := parsePRNumber(prID)
	if err != nil {
		return MergeResult{}, ErrPRNotFound
	}
	if err := s.provider.MergePR(ctx, s.owner, s.repo, num); err != nil {
		return MergeResult{}, mapMergeError(err)
	}
	return MergeResult{PRNumber: num, Method: "squash"}, nil
}

// ResolveComments resolves the review threads identified by commentIDs.
// If commentIDs is empty, all unresolved threads on the PR are resolved.
func (s *ActionService) ResolveComments(ctx context.Context, prID string, commentIDs []string) (ResolveResult, error) {
	num, err := parsePRNumber(prID)
	if err != nil {
		return ResolveResult{}, ErrPRNotFound
	}

	threadIDs := commentIDs
	if len(threadIDs) == 0 {
		ids, err := s.provider.ListUnresolvedThreadIDs(ctx, s.owner, s.repo, num)
		if err != nil {
			return ResolveResult{}, mapResolveError(err)
		}
		if len(ids) == 0 {
			return ResolveResult{}, ErrNothingToResolve
		}
		threadIDs = ids
	} else {
		// Verify the PR exists so the {id} path parameter is not silently
		// ignored when explicit thread IDs are supplied.
		if _, err := s.provider.ListUnresolvedThreadIDs(ctx, s.owner, s.repo, num); err != nil {
			return ResolveResult{}, mapResolveError(err)
		}
	}

	var resolved int
	for _, id := range threadIDs {
		if err := s.provider.ResolveThread(ctx, id); err != nil {
			return ResolveResult{}, mapResolveError(err)
		}
		resolved++
	}
	if resolved == 0 {
		return ResolveResult{}, ErrNothingToResolve
	}
	return ResolveResult{Resolved: resolved}, nil
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
