// Package review holds the core code-review logic: triggering a reviewer over a
// worker's worktree, recording review runs, and accepting submitted results.
//
// It is independent of any transport. The daemon's HTTP service
// (internal/service/review) is a thin boundary over this engine today, and the
// same engine can back an in-process CLI trigger later without going through the
// API. Transport-specific concerns (DTOs, error→status mapping) stay in the
// service/controller layers; the orchestration and run-id generation live here.
package review

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// ErrInvalid and ErrNotFound let the transport layer map failures to 422/404.
var (
	ErrInvalid  = errors.New("review: invalid input")
	ErrNotFound = errors.New("review: not found")
)

// Store is the persistence surface the engine needs. *sqlite.Store satisfies it
// in production; tests use a fake.
type Store interface {
	UpsertReview(ctx context.Context, r domain.Review) error
	GetReviewBySession(ctx context.Context, id domain.SessionID) (domain.Review, bool, error)
	InsertReviewRun(ctx context.Context, r domain.ReviewRun) error
	UpdateReviewRunResult(ctx context.Context, id string, status domain.ReviewRunStatus, verdict domain.ReviewVerdict, body string) (bool, error)
	GetReviewRun(ctx context.Context, id string) (domain.ReviewRun, bool, error)
	GetReviewRunBySessionAndSHA(ctx context.Context, id domain.SessionID, targetSHA string) (domain.ReviewRun, bool, error)
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

// Deps wires the engine.
type Deps struct {
	Store    Store
	Sessions Sessions
	PRs      PRs
	Projects Projects
	Launcher Launcher

	// Clock and NewID are injectable for deterministic tests.
	Clock func() time.Time
	NewID func() string
}

// Engine is the core code-review engine.
type Engine struct {
	store    Store
	sessions Sessions
	prs      PRs
	projects Projects
	launcher Launcher
	clock    func() time.Time
	newID    func() string

	// triggerMu guards triggerLocks; triggerLocks holds one mutex per worker
	// session so concurrent Trigger calls for the same worker serialise (see
	// lockWorker). Distinct workers never contend.
	triggerMu    sync.Mutex
	triggerLocks map[domain.SessionID]*sync.Mutex
}

// New wires an Engine from its dependencies, defaulting the clock and id source.
func New(d Deps) *Engine {
	clock := d.Clock
	if clock == nil {
		clock = func() time.Time { return time.Now().UTC() }
	}
	newID := d.NewID
	if newID == nil {
		newID = uuid.NewString
	}
	return &Engine{
		store:        d.Store,
		sessions:     d.Sessions,
		prs:          d.PRs,
		projects:     d.Projects,
		launcher:     d.Launcher,
		clock:        clock,
		newID:        newID,
		triggerLocks: make(map[domain.SessionID]*sync.Mutex),
	}
}

// lockWorker serialises Trigger calls for a single worker session and returns
// the unlock func. Without it, two concurrent triggers for the same worker can
// both pass the per-commit idempotency check and each spawn a reviewer against
// the same deterministic handle, leaving two running runs for one commit (#242).
//
// The per-worker mutex is created on first use and kept for the lifetime of the
// engine; the entry is a single pointer, so the unbounded-by-session-count map
// is a negligible, bounded-in-practice cost.
func (e *Engine) lockWorker(id domain.SessionID) func() {
	e.triggerMu.Lock()
	mu, ok := e.triggerLocks[id]
	if !ok {
		mu = &sync.Mutex{}
		e.triggerLocks[id] = mu
	}
	e.triggerMu.Unlock()
	mu.Lock()
	return mu.Unlock
}

// TriggerResult is the outcome of a trigger: the (new or existing) run, the live
// reviewer pane's handle so the UI can attach its terminal, and whether a new
// pass was started (false when an existing run for the same commit was reused).
type TriggerResult struct {
	Run              domain.ReviewRun
	ReviewerHandleID string
	Created          bool
}

// SessionReviews is a worker's review state: the live reviewer handle plus its
// recorded passes, newest first.
type SessionReviews struct {
	ReviewerHandleID string
	Runs             []domain.ReviewRun
}

// Trigger starts (or reuses) a review of a worker's PR at its current head:
//   - if a run already exists for this commit, it is returned unchanged;
//   - otherwise, if a live reviewer pane exists, it is messaged to review the
//     new commit; if not, a fresh reviewer is spawned;
//   - only after the reviewer is launched is the run recorded.
func (e *Engine) Trigger(ctx context.Context, workerID domain.SessionID) (TriggerResult, error) {
	if workerID == "" {
		return TriggerResult{}, fmt.Errorf("%w: worker session id is required", ErrInvalid)
	}

	// Serialise concurrent triggers for this worker so the idempotency check
	// below (and the reviewer spawn that follows it) can't be raced into a
	// double-spawn. Held across the spawn deliberately: the loser then re-reads
	// the freshly-recorded run and short-circuits to Created:false.
	unlock := e.lockWorker(workerID)
	defer unlock()

	worker, ok, err := e.sessions.GetSession(ctx, workerID)
	if err != nil {
		return TriggerResult{}, err
	}
	if !ok {
		return TriggerResult{}, fmt.Errorf("%w: worker session %q", ErrNotFound, workerID)
	}
	if worker.IsTerminated {
		return TriggerResult{}, fmt.Errorf("%w: worker session %q is terminated", ErrInvalid, workerID)
	}
	if worker.Metadata.WorkspacePath == "" {
		return TriggerResult{}, fmt.Errorf("%w: worker session %q has no workspace to review", ErrInvalid, workerID)
	}

	pr, err := e.workerPR(ctx, workerID)
	if err != nil {
		return TriggerResult{}, err
	}
	targetSHA := pr.HeadSHA

	review, hasReview, err := e.store.GetReviewBySession(ctx, workerID)
	if err != nil {
		return TriggerResult{}, err
	}

	// Idempotency: a pass already exists for this commit — return it as-is.
	if existing, ok, err := e.store.GetReviewRunBySessionAndSHA(ctx, workerID, targetSHA); err != nil {
		return TriggerResult{}, err
	} else if ok {
		return TriggerResult{Run: existing, ReviewerHandleID: review.ReviewerHandleID, Created: false}, nil
	}

	harness, err := e.reviewerHarness(ctx, worker)
	if err != nil {
		return TriggerResult{}, err
	}

	now := e.clock()
	runID := e.newID()
	spec := LaunchSpec{
		RunID:         runID,
		WorkerID:      workerID,
		Harness:       harness,
		WorkspacePath: worker.Metadata.WorkspacePath,
		PRURL:         pr.URL,
		TargetSHA:     targetSHA,
	}

	// Reuse a live reviewer pane if there is one; otherwise spawn a fresh one.
	handleID := ""
	if hasReview && review.ReviewerHandleID != "" {
		alive, err := e.launcher.Alive(ctx, review.ReviewerHandleID)
		if err != nil {
			return TriggerResult{}, err
		}
		if alive {
			if err := e.launcher.Notify(ctx, review.ReviewerHandleID, spec); err != nil {
				return TriggerResult{}, fmt.Errorf("notify reviewer: %w", err)
			}
			handleID = review.ReviewerHandleID
		}
	}
	if handleID == "" {
		h, err := e.launcher.Spawn(ctx, spec)
		if err != nil {
			return TriggerResult{}, fmt.Errorf("launch reviewer: %w", err)
		}
		handleID = h
	}

	// The reviewer is running; now record the pass.
	review, err = e.upsertReview(ctx, worker, harness, pr.URL, handleID, now)
	if err != nil {
		return TriggerResult{}, err
	}
	run := domain.ReviewRun{
		ID:        runID,
		ReviewID:  review.ID,
		SessionID: workerID,
		Harness:   harness,
		PRURL:     pr.URL,
		TargetSHA: targetSHA,
		Status:    domain.ReviewRunRunning,
		Verdict:   domain.VerdictNone,
		CreatedAt: now,
	}
	if err := e.store.InsertReviewRun(ctx, run); err != nil {
		// The per-worker lock serialises in-process triggers, but the unique
		// index (migration 0013) can still reject a run a concurrent daemon (or
		// a pre-lock restart) recorded for this commit. The reviewer is already
		// launched, so don't surface a raw error: re-read the recorded run and
		// return it as the existing, not-newly-created pass.
		if errors.Is(err, domain.ErrDuplicateReviewRun) {
			if existing, ok, getErr := e.store.GetReviewRunBySessionAndSHA(ctx, workerID, targetSHA); getErr != nil {
				return TriggerResult{}, getErr
			} else if ok {
				return TriggerResult{Run: existing, ReviewerHandleID: handleID, Created: false}, nil
			}
		}
		return TriggerResult{}, err
	}
	return TriggerResult{Run: run, ReviewerHandleID: handleID, Created: true}, nil
}

// Submit records the reviewer's result for a specific worker review pass: it
// marks the run complete and stores the verdict and body. AO does not post the
// review — the reviewer agent posts it to the PR itself.
func (e *Engine) Submit(ctx context.Context, workerID domain.SessionID, runID string, verdict domain.ReviewVerdict, body string) (domain.ReviewRun, error) {
	if workerID == "" {
		return domain.ReviewRun{}, fmt.Errorf("%w: worker session id is required", ErrInvalid)
	}
	if runID == "" {
		return domain.ReviewRun{}, fmt.Errorf("%w: review run id is required", ErrInvalid)
	}
	if !verdict.Valid() {
		return domain.ReviewRun{}, fmt.Errorf("%w: verdict must be %q or %q", ErrInvalid, domain.VerdictApproved, domain.VerdictChangesRequested)
	}
	if verdict == domain.VerdictChangesRequested && body == "" {
		return domain.ReviewRun{}, fmt.Errorf("%w: a changes_requested review requires a body", ErrInvalid)
	}

	run, ok, err := e.store.GetReviewRun(ctx, runID)
	if err != nil {
		return domain.ReviewRun{}, err
	}
	if !ok {
		return domain.ReviewRun{}, fmt.Errorf("%w: review run %q", ErrNotFound, runID)
	}
	if run.SessionID != workerID {
		return domain.ReviewRun{}, fmt.Errorf("%w: review run %q does not belong to worker %q", ErrInvalid, runID, workerID)
	}
	if run.Status != domain.ReviewRunRunning {
		return domain.ReviewRun{}, fmt.Errorf("%w: review run %q is not running", ErrInvalid, runID)
	}

	updated, err := e.store.UpdateReviewRunResult(ctx, run.ID, domain.ReviewRunComplete, verdict, body)
	if err != nil {
		return domain.ReviewRun{}, err
	}
	if !updated {
		return domain.ReviewRun{}, fmt.Errorf("%w: review run %q is not running", ErrInvalid, runID)
	}
	run.Status = domain.ReviewRunComplete
	run.Verdict = verdict
	run.Body = body
	return run, nil
}

// List returns a worker's review state: the live reviewer handle and its passes.
func (e *Engine) List(ctx context.Context, workerID domain.SessionID) (SessionReviews, error) {
	if workerID == "" {
		return SessionReviews{}, fmt.Errorf("%w: worker session id is required", ErrInvalid)
	}
	runs, err := e.store.ListReviewRunsBySession(ctx, workerID)
	if err != nil {
		return SessionReviews{}, err
	}
	var handle string
	if review, ok, err := e.store.GetReviewBySession(ctx, workerID); err != nil {
		return SessionReviews{}, err
	} else if ok {
		handle = review.ReviewerHandleID
	}
	return SessionReviews{ReviewerHandleID: handle, Runs: runs}, nil
}

func (e *Engine) workerPR(ctx context.Context, workerID domain.SessionID) (domain.PullRequest, error) {
	prs, err := e.prs.ListPRsBySession(ctx, workerID)
	if err != nil {
		return domain.PullRequest{}, err
	}
	if len(prs) == 0 {
		return domain.PullRequest{}, fmt.Errorf("%w: worker %q has no PR to review", ErrInvalid, workerID)
	}
	return prs[0], nil
}

// reviewerHarness resolves which harness reviews the worker's PR: a configured
// reviewer wins, otherwise the worker's own harness is reused (falling back to
// claude-code), per domain.ResolveReviewerHarness.
func (e *Engine) reviewerHarness(ctx context.Context, worker domain.SessionRecord) (domain.ReviewerHarness, error) {
	var cfg domain.ProjectConfig
	if e.projects != nil {
		if proj, ok, err := e.projects.GetProject(ctx, string(worker.ProjectID)); err != nil {
			return "", err
		} else if ok {
			cfg = proj.Config
		}
	}
	return cfg.ResolveReviewerHarness(worker.Harness), nil
}

func (e *Engine) upsertReview(ctx context.Context, worker domain.SessionRecord, harness domain.ReviewerHarness, prURL, handleID string, now time.Time) (domain.Review, error) {
	existing, ok, err := e.store.GetReviewBySession(ctx, worker.ID)
	if err != nil {
		return domain.Review{}, err
	}
	review := domain.Review{
		ID:               e.newID(),
		SessionID:        worker.ID,
		ProjectID:        worker.ProjectID,
		Harness:          harness,
		PRURL:            prURL,
		ReviewerHandleID: handleID,
		CreatedAt:        now,
		UpdatedAt:        now,
	}
	if ok {
		// Reuse the existing row's identity and creation time; UpsertReview
		// refreshes harness/pr_url/reviewer_handle_id/updated_at.
		review.ID = existing.ID
		review.CreatedAt = existing.CreatedAt
	}
	if err := e.store.UpsertReview(ctx, review); err != nil {
		return domain.Review{}, err
	}
	return review, nil
}
