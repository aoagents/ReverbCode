// Package review is the daemon's code-review surface: triggering a review spawns
// a configured reviewer agent over the worker's worktree with its own review
// prompt. The reviewer agent posts its review to the PR itself; the worker picks
// the feedback up through the existing SCM observer → review-nudge path.
//
// V1 is manual and one-shot: a review runs only when triggered. The reviewer is
// tracked by the review (one per worker) and review_run (one per pass) tables.
package review

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// ErrInvalid and ErrNotFound let the HTTP layer map service failures to 422/404.
var (
	ErrInvalid  = errors.New("review: invalid input")
	ErrNotFound = errors.New("review: not found")
)

// Store is the persistence surface the review service needs. *sqlite.Store
// satisfies it in production; tests use a fake.
type Store interface {
	UpsertReview(ctx context.Context, r domain.Review) error
	GetReviewBySession(ctx context.Context, id domain.SessionID) (domain.Review, bool, error)
	InsertReviewRun(ctx context.Context, r domain.ReviewRun) error
	UpdateReviewRunResult(ctx context.Context, id string, status domain.ReviewRunStatus, verdict domain.ReviewVerdict, updatedAt time.Time) error
	GetLatestReviewRunBySession(ctx context.Context, id domain.SessionID) (domain.ReviewRun, bool, error)
	ListReviewRunsBySession(ctx context.Context, id domain.SessionID) ([]domain.ReviewRun, error)
}

// Sessions resolves the worker session under review.
type Sessions interface {
	GetSession(ctx context.Context, id domain.SessionID) (domain.SessionRecord, bool, error)
}

// PRs resolves the PR a worker owns.
type PRs interface {
	ListPRsBySession(ctx context.Context, id domain.SessionID) ([]domain.PullRequest, error)
}

// Projects resolves the per-project reviewer config.
type Projects interface {
	GetProject(ctx context.Context, id string) (domain.ProjectRecord, bool, error)
}

// Runner launches the reviewer agent one-shot over the worker's worktree.
type Runner interface {
	Run(ctx context.Context, spec RunSpec) error
}

// RunSpec describes one reviewer launch.
type RunSpec struct {
	WorkerID      domain.SessionID
	Harness       domain.AgentHarness
	WorkspacePath string
	PRURL         string
}

// Manager is the reviews surface the HTTP controller depends on.
type Manager interface {
	Trigger(ctx context.Context, workerID domain.SessionID) (domain.ReviewRun, error)
	List(ctx context.Context, workerID domain.SessionID) ([]domain.ReviewRun, error)
}

// Deps wires the review service.
type Deps struct {
	Store    Store
	Sessions Sessions
	PRs      PRs
	Projects Projects
	Runner   Runner

	// Clock and NewID are injectable for deterministic tests.
	Clock func() time.Time
	NewID func() string
}

// Service is the daemon's code-review service.
type Service struct {
	store    Store
	sessions Sessions
	prs      PRs
	projects Projects
	runner   Runner
	clock    func() time.Time
	newID    func() string
}

var _ Manager = (*Service)(nil)

// New wires a Service from its dependencies, defaulting the clock and id source.
func New(d Deps) *Service {
	clock := d.Clock
	if clock == nil {
		clock = func() time.Time { return time.Now().UTC() }
	}
	newID := d.NewID
	if newID == nil {
		newID = uuid.NewString
	}
	return &Service{
		store:    d.Store,
		sessions: d.Sessions,
		prs:      d.PRs,
		projects: d.Projects,
		runner:   d.Runner,
		clock:    clock,
		newID:    newID,
	}
}

// Trigger starts a review pass for a worker's PR: it reuses (or creates) the
// worker's review row, records a pending review_run, and launches the configured
// reviewer agent over the worker's worktree.
func (s *Service) Trigger(ctx context.Context, workerID domain.SessionID) (domain.ReviewRun, error) {
	if workerID == "" {
		return domain.ReviewRun{}, fmt.Errorf("%w: worker session id is required", ErrInvalid)
	}
	worker, ok, err := s.sessions.GetSession(ctx, workerID)
	if err != nil {
		return domain.ReviewRun{}, err
	}
	if !ok {
		return domain.ReviewRun{}, fmt.Errorf("%w: worker session %q", ErrNotFound, workerID)
	}
	if worker.IsTerminated {
		return domain.ReviewRun{}, fmt.Errorf("%w: worker session %q is terminated", ErrInvalid, workerID)
	}
	if worker.Metadata.WorkspacePath == "" {
		return domain.ReviewRun{}, fmt.Errorf("%w: worker session %q has no workspace to review", ErrInvalid, workerID)
	}

	prURL, err := s.workerPRURL(ctx, workerID)
	if err != nil {
		return domain.ReviewRun{}, err
	}

	harness, err := s.reviewerHarness(ctx, worker)
	if err != nil {
		return domain.ReviewRun{}, err
	}

	now := s.clock()
	review, err := s.upsertReview(ctx, worker, harness, prURL, now)
	if err != nil {
		return domain.ReviewRun{}, err
	}

	run := domain.ReviewRun{
		ID:        s.newID(),
		ReviewID:  review.ID,
		SessionID: workerID,
		Harness:   harness,
		PRURL:     prURL,
		Status:    domain.ReviewRunPending,
		Verdict:   domain.VerdictNone,
		Iteration: s.nextIteration(ctx, workerID),
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := s.store.InsertReviewRun(ctx, run); err != nil {
		return domain.ReviewRun{}, err
	}

	if err := s.runner.Run(ctx, RunSpec{
		WorkerID:      workerID,
		Harness:       harness,
		WorkspacePath: worker.Metadata.WorkspacePath,
		PRURL:         prURL,
	}); err != nil {
		// The pass never launched; record it as failed so a stale pending row
		// does not look like an in-flight review forever.
		_ = s.store.UpdateReviewRunResult(ctx, run.ID, domain.ReviewRunFailed, domain.VerdictNone, s.clock())
		return domain.ReviewRun{}, fmt.Errorf("launch reviewer: %w", err)
	}
	return run, nil
}

// List returns the review passes recorded for a worker, newest first.
func (s *Service) List(ctx context.Context, workerID domain.SessionID) ([]domain.ReviewRun, error) {
	if workerID == "" {
		return nil, fmt.Errorf("%w: worker session id is required", ErrInvalid)
	}
	return s.store.ListReviewRunsBySession(ctx, workerID)
}

func (s *Service) workerPRURL(ctx context.Context, workerID domain.SessionID) (string, error) {
	prs, err := s.prs.ListPRsBySession(ctx, workerID)
	if err != nil {
		return "", err
	}
	if len(prs) == 0 {
		return "", fmt.Errorf("%w: worker %q has no PR to review", ErrInvalid, workerID)
	}
	return prs[0].URL, nil
}

// reviewerHarness resolves which harness reviews the worker's PR: a configured
// reviewer wins, otherwise the worker's own harness is reused (falling back to
// claude-code), per domain.ResolveReviewerHarness.
func (s *Service) reviewerHarness(ctx context.Context, worker domain.SessionRecord) (domain.AgentHarness, error) {
	var cfg domain.ProjectConfig
	if s.projects != nil {
		if proj, ok, err := s.projects.GetProject(ctx, string(worker.ProjectID)); err != nil {
			return "", err
		} else if ok {
			cfg = proj.Config
		}
	}
	return cfg.ResolveReviewerHarness(worker.Harness), nil
}

func (s *Service) upsertReview(ctx context.Context, worker domain.SessionRecord, harness domain.AgentHarness, prURL string, now time.Time) (domain.Review, error) {
	existing, ok, err := s.store.GetReviewBySession(ctx, worker.ID)
	if err != nil {
		return domain.Review{}, err
	}
	review := domain.Review{
		ID:        s.newID(),
		SessionID: worker.ID,
		ProjectID: worker.ProjectID,
		Harness:   harness,
		PRURL:     prURL,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if ok {
		// Reuse the existing row's identity and creation time; UpsertReview
		// refreshes harness/pr_url/updated_at.
		review.ID = existing.ID
		review.CreatedAt = existing.CreatedAt
	}
	if err := s.store.UpsertReview(ctx, review); err != nil {
		return domain.Review{}, err
	}
	return review, nil
}

func (s *Service) nextIteration(ctx context.Context, workerID domain.SessionID) int {
	if latest, ok, err := s.store.GetLatestReviewRunBySession(ctx, workerID); err == nil && ok {
		return latest.Iteration + 1
	}
	return 1
}
