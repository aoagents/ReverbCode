package pr

import (
	"context"
	"strconv"
)

// ActionManager is the controller-facing contract for /prs/{id} action routes.
type ActionManager interface {
	Merge(ctx context.Context, prID string) (MergeResult, error)
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

// ActionService implements ActionManager. Business logic is not yet implemented.
type ActionService struct{}

var _ ActionManager = (*ActionService)(nil)

// NewActionService returns a stub ActionService.
func NewActionService() *ActionService {
	return &ActionService{}
}

// Merge squash-merges the PR identified by prID.
// TODO: implement — squash-merge the PR via the SCM provider.
func (s *ActionService) Merge(_ context.Context, prID string) (MergeResult, error) {
	n, _ := strconv.Atoi(prID)
	return MergeResult{PRNumber: n, Method: "squash"}, nil
}

// ResolveComments resolves review threads on the PR identified by prID.
// TODO: implement — resolve review threads via the SCM provider.
func (s *ActionService) ResolveComments(_ context.Context, _ string, _ []string) (ResolveResult, error) {
	return ResolveResult{Resolved: 0}, nil
}
