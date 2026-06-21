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
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
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
	UpdateReviewRunResult(ctx context.Context, id string, status domain.ReviewRunStatus, verdict domain.ReviewVerdict, body, githubReviewID string) (bool, error)
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
	Store     Store
	Sessions  Sessions
	PRs       PRs
	Projects  Projects
	Launcher  Launcher
	Messenger ports.AgentMessenger

	// Clock and NewID are injectable for deterministic tests.
	Clock func() time.Time
	NewID func() string
}

// Engine is the core code-review engine.
type Engine struct {
	store     Store
	sessions  Sessions
	prs       PRs
	projects  Projects
	launcher  Launcher
	messenger ports.AgentMessenger
	clock     func() time.Time
	newID     func() string

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
		messenger:    d.Messenger,
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
//   - if a non-failed run already exists for this commit, it is returned unchanged;
//   - otherwise, if a live reviewer pane exists, it is messaged to review the
//     new commit; if not, a fresh reviewer is spawned;
//   - the run is recorded before launch so startup failures leave a visible
//     failed pass instead of an empty gap.
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

	// Idempotency: return a non-failed pass as-is. Failed passes stay visible
	// but can be retried after the user fixes the underlying issue.
	if existing, ok, err := e.store.GetReviewRunBySessionAndSHA(ctx, workerID, targetSHA); err != nil {
		return TriggerResult{}, err
	} else if ok && existing.Status != domain.ReviewRunFailed {
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

	review, err = e.upsertReview(ctx, worker, harness, pr.URL, review.ReviewerHandleID, now)
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
		if errors.Is(err, domain.ErrDuplicateReviewRun) {
			if existing, ok, getErr := e.store.GetReviewRunBySessionAndSHA(ctx, workerID, targetSHA); getErr != nil {
				return TriggerResult{}, getErr
			} else if ok {
				return TriggerResult{Run: existing, ReviewerHandleID: review.ReviewerHandleID, Created: false}, nil
			}
		}
		return TriggerResult{}, err
	}

	failRun := func(err error) error {
		if _, updateErr := e.store.UpdateReviewRunResult(ctx, run.ID, domain.ReviewRunFailed, domain.VerdictNone, err.Error(), ""); updateErr != nil {
			return updateErr
		}
		return err
	}

	// Reuse a live reviewer pane if there is one; otherwise spawn a fresh one.
	handleID := ""
	if hasReview && review.ReviewerHandleID != "" {
		alive, err := e.launcher.Alive(ctx, review.ReviewerHandleID)
		if err != nil {
			return TriggerResult{}, failRun(err)
		}
		if alive {
			if err := e.launcher.Notify(ctx, review.ReviewerHandleID, spec); err != nil {
				return TriggerResult{}, failRun(fmt.Errorf("notify reviewer: %w", err))
			}
			handleID = review.ReviewerHandleID
		}
	}
	if handleID == "" {
		h, err := e.launcher.Spawn(ctx, spec)
		if err != nil {
			return TriggerResult{}, failRun(fmt.Errorf("launch reviewer: %w", err))
		}
		handleID = h
	}

	// The reviewer is running; now record the pass.
	review, err = e.upsertReview(ctx, worker, harness, pr.URL, handleID, now)
	if err != nil {
		return TriggerResult{}, err
	}
	run.ReviewID = review.ID
	return TriggerResult{Run: run, ReviewerHandleID: handleID, Created: true}, nil
}

// Submit records the reviewer's result for a specific worker review pass: it
// marks the run complete and stores the verdict, body, and the GitHub review id
// the reviewer posted. AO does not post the review — the reviewer agent posts it
// to the PR itself.
//
// On a changes_requested verdict, Submit also messages the worker session with
// the review feedback directly, so the worker learns about it event-driven
// rather than via the SCM poll loop (which never observes CHANGES_REQUESTED for
// self-reviews or COMMENT-state reviews; issue #337). When a GitHub review id is
// known, it is included so the worker knows exactly which review to address and
// reply to.
func (e *Engine) Submit(ctx context.Context, workerID domain.SessionID, runID string, verdict domain.ReviewVerdict, body, githubReviewID string) (domain.ReviewRun, error) {
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

	// Notify the worker before marking the run complete. If the message fails,
	// the run stays 'running' so a retried `ao review submit` runs again instead
	// of tripping the status='running' guard above on an already-completed run. A
	// message that lands but a DB write that then fails degrades to one extra
	// nudge on retry — the same trade lifecycle's sendOnce makes.
	if verdict == domain.VerdictChangesRequested {
		if err := e.notifyWorkerChangesRequested(ctx, workerID, body, githubReviewID); err != nil {
			return domain.ReviewRun{}, err
		}
	}

	updated, err := e.store.UpdateReviewRunResult(ctx, run.ID, domain.ReviewRunComplete, verdict, body, githubReviewID)
	if err != nil {
		return domain.ReviewRun{}, err
	}
	if !updated {
		return domain.ReviewRun{}, fmt.Errorf("%w: review run %q is not running", ErrInvalid, runID)
	}
	run.Status = domain.ReviewRunComplete
	run.Verdict = verdict
	run.Body = body
	run.GithubReviewID = githubReviewID
	return run, nil
}

// notifyWorkerChangesRequested injects the AO reviewer's feedback into the
// worker's live agent pane via the same messenger lifecycle uses for SCM nudges.
//
// When the GitHub review id is known, the worker is asked to reply on that
// review referencing its id with how it addressed the feedback and to resolve
// the review comment threads it addressed. The reviewer posts inline comments
// (per its prompt), so the per-finding threads are resolvable via `gh api`
// GraphQL resolveReviewThread; the top-level review object itself is not
// resolvable, hence the reply. The body is reviewer-authored text pasted into a
// PTY, so it is sanitized first (matching the lifecycle reaction path).
func (e *Engine) notifyWorkerChangesRequested(ctx context.Context, workerID domain.SessionID, body, githubReviewID string) error {
	if e.messenger == nil {
		return nil
	}
	msg := "An AO code reviewer requested changes on your PR. Review the feedback below and address it."
	if githubReviewID != "" {
		safeReviewID := domain.SanitizeControlChars(githubReviewID)
		msg += fmt.Sprintf(" This feedback is GitHub review %s. Once you have addressed it, reply on that review referencing id %s with how you addressed it, then resolve the review comment threads you addressed.", safeReviewID, safeReviewID)
	}
	if body != "" {
		msg += "\n\n" + domain.SanitizeControlChars(body)
	}
	return e.messenger.Send(ctx, workerID, msg)
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
