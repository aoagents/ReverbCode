package ports

import "context"

// PRService is the HTTP-layer contract for PR action operations (SCM lane).
// Implementations back the two /prs/{id}/... action routes.
type PRService interface {
	// Merge squash-merges the PR identified by prID (its number as a string).
	// Returns ErrPRNotFound (→ 404), ErrPRNotMergeable (→ 409), or
	// ErrPRPreconditions (→ 422) on expected failure modes.
	Merge(ctx context.Context, prID string) (MergeResult, error)

	// ResolveComments resolves the review threads identified by commentIDs
	// (GitHub review thread node IDs). If commentIDs is empty, all unresolved
	// threads on the PR are resolved. Returns ErrPRNotFound (→ 404) or
	// ErrNothingToResolve (→ 422) on expected failure modes.
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
