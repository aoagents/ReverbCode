package review

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// --- fakes ---

type fakeStore struct {
	reviews []domain.Review
	runs    []domain.ReviewRun
	// insertErr, when set, makes the next InsertReviewRun model a concurrent
	// writer that already recorded a run for this commit: it records that
	// winner (so a follow-up GetReviewRunBySessionPRAndSHA finds it) and returns
	// insertErr instead of recording the caller's run.
	insertErr error
}

func (f *fakeStore) UpsertReview(_ context.Context, r domain.Review) error {
	for i := range f.reviews {
		if f.reviews[i].SessionID == r.SessionID && f.reviews[i].PRURL == r.PRURL {
			f.reviews[i] = r
			return nil
		}
	}
	f.reviews = append(f.reviews, r)
	return nil
}
func (f *fakeStore) GetReviewBySessionAndPR(_ context.Context, id domain.SessionID, prURL string) (domain.Review, bool, error) {
	for _, review := range f.reviews {
		if review.SessionID == id && review.PRURL == prURL {
			return review, true, nil
		}
	}
	return domain.Review{}, false, nil
}
func (f *fakeStore) ListReviewsBySession(_ context.Context, id domain.SessionID) ([]domain.Review, error) {
	var out []domain.Review
	for _, review := range f.reviews {
		if review.SessionID == id {
			out = append(out, review)
		}
	}
	return out, nil
}
func (f *fakeStore) InsertReviewRun(_ context.Context, r domain.ReviewRun) error {
	if f.insertErr != nil {
		winner := r
		winner.ID = "winner-" + r.ID
		f.runs = append(f.runs, winner)
		return f.insertErr
	}
	f.runs = append(f.runs, r)
	return nil
}
func (f *fakeStore) UpdateReviewRunResult(_ context.Context, id string, status domain.ReviewRunStatus, verdict domain.ReviewVerdict, body, githubReviewID string) (bool, error) {
	for i := range f.runs {
		if f.runs[i].ID == id {
			if f.runs[i].Status != domain.ReviewRunRunning {
				return false, nil
			}
			f.runs[i].Status = status
			f.runs[i].Verdict = verdict
			f.runs[i].Body = body
			f.runs[i].GithubReviewID = githubReviewID
			return true, nil
		}
	}
	return false, nil
}
func (f *fakeStore) GetReviewRun(_ context.Context, id string) (domain.ReviewRun, bool, error) {
	for _, r := range f.runs {
		if r.ID == id {
			return r, true, nil
		}
	}
	return domain.ReviewRun{}, false, nil
}
func (f *fakeStore) GetReviewRunBySessionPRAndSHA(_ context.Context, id domain.SessionID, prURL, sha string) (domain.ReviewRun, bool, error) {
	for i := len(f.runs) - 1; i >= 0; i-- {
		if f.runs[i].SessionID == id && f.runs[i].PRURL == prURL && f.runs[i].TargetSHA == sha {
			return f.runs[i], true, nil
		}
	}
	return domain.ReviewRun{}, false, nil
}
func (f *fakeStore) ListReviewRunsBySession(_ context.Context, id domain.SessionID) ([]domain.ReviewRun, error) {
	var out []domain.ReviewRun
	for _, run := range f.runs {
		if run.SessionID == id {
			out = append(out, run)
		}
	}
	return out, nil
}
func (f *fakeStore) ListReviewRunsBySessionAndPR(_ context.Context, id domain.SessionID, prURL string) ([]domain.ReviewRun, error) {
	var out []domain.ReviewRun
	for _, run := range f.runs {
		if run.SessionID == id && run.PRURL == prURL {
			out = append(out, run)
		}
	}
	return out, nil
}

type fakeSessions struct {
	rec domain.SessionRecord
	ok  bool
}

func (f fakeSessions) GetSession(_ context.Context, _ domain.SessionID) (domain.SessionRecord, bool, error) {
	return f.rec, f.ok, nil
}

type fakePRs struct{ prs []domain.PullRequest }

func (f fakePRs) ListPRsBySession(_ context.Context, _ domain.SessionID) ([]domain.PullRequest, error) {
	return f.prs, nil
}

type fakeProjects struct{ cfg domain.ProjectConfig }

func (f fakeProjects) GetProject(_ context.Context, id string) (domain.ProjectRecord, bool, error) {
	return domain.ProjectRecord{ID: id, Config: f.cfg}, true, nil
}

type fakeLauncher struct {
	handle     string
	alive      bool
	spawnErr   error
	notifyErr  error
	spawned    bool
	spawnCount int
	notified   bool
	gotSpec    LaunchSpec
	gotHandle  string
}

func (f *fakeLauncher) Spawn(_ context.Context, spec LaunchSpec) (string, error) {
	f.spawned = true
	f.spawnCount++
	f.gotSpec = spec
	if f.spawnErr != nil {
		return "", f.spawnErr
	}
	return f.handle, nil
}
func (f *fakeLauncher) Notify(_ context.Context, handleID string, spec LaunchSpec) error {
	f.notified = true
	f.gotHandle = handleID
	f.gotSpec = spec
	return f.notifyErr
}
func (f *fakeLauncher) Alive(_ context.Context, _ string) (bool, error) { return f.alive, nil }

type fakeMessenger struct {
	sends   int
	gotID   domain.SessionID
	gotMsg  string
	sendErr error
}

func (f *fakeMessenger) Send(_ context.Context, id domain.SessionID, msg string) error {
	f.sends++
	f.gotID = id
	f.gotMsg = msg
	return f.sendErr
}

func liveWorker() domain.SessionRecord {
	return domain.SessionRecord{
		ID:        "mer-1",
		ProjectID: "mer",
		Harness:   domain.HarnessClaudeCode,
		Metadata:  domain.SessionMetadata{WorkspacePath: "/ws/mer-1"},
	}
}

func newEngineForTest(store Store, sessions Sessions, prs PRs, projects Projects, launcher Launcher) *Engine {
	ids := 0
	return New(Deps{
		Store: store, Sessions: sessions, PRs: prs, Projects: projects, Launcher: launcher,
		Clock: func() time.Time { return time.Unix(0, 0).UTC() },
		NewID: func() string { ids++; return "id-" + string(rune('0'+ids)) },
	})
}

func prAt(sha string) fakePRs {
	return prAtURL("https://github.com/o/r/pull/1", sha)
}

func prAtURL(url, sha string) fakePRs {
	return fakePRs{prs: []domain.PullRequest{{URL: url, HeadSHA: sha}}}
}

// --- tests ---

func TestTriggerSpawnsNewReviewerAndRecordsRunAfterLaunch(t *testing.T) {
	store := &fakeStore{}
	launcher := &fakeLauncher{handle: "review-mer-1"}
	eng := newEngineForTest(store, fakeSessions{rec: liveWorker(), ok: true}, prAt("sha1"), fakeProjects{}, launcher)

	res, err := eng.Trigger(context.Background(), "mer-1", "")
	if err != nil {
		t.Fatalf("Trigger: %v", err)
	}
	if !res.Created || res.ReviewerHandleID != "review-mer-1" {
		t.Fatalf("result = %+v", res)
	}
	if !launcher.spawned || launcher.notified {
		t.Fatalf("expected spawn (no live reviewer): %+v", launcher)
	}
	if res.Run.TargetSHA != "sha1" || res.Run.Status != domain.ReviewRunRunning || res.Run.Harness != domain.ReviewerClaudeCode {
		t.Fatalf("run = %+v", res.Run)
	}
	if launcher.gotSpec.RunID != res.Run.ID {
		t.Fatalf("launch spec run id %q != run id %q", launcher.gotSpec.RunID, res.Run.ID)
	}
	if len(store.runs) != 1 || len(store.reviews) != 1 || store.reviews[0].ReviewerHandleID != "review-mer-1" {
		t.Fatalf("persisted reviews=%+v runs=%+v", store.reviews, store.runs)
	}
}

func TestTriggerConcurrentSameWorkerSpawnsOnce(t *testing.T) {
	store := &fakeStore{}
	launcher := &fakeLauncher{handle: "review-mer-1"}
	eng := newEngineForTest(store, fakeSessions{rec: liveWorker(), ok: true}, prAt("sha1"), fakeProjects{}, launcher)

	const n = 8
	var wg sync.WaitGroup
	results := make([]TriggerResult, n)
	errs := make([]error, n)
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func(i int) {
			defer wg.Done()
			results[i], errs[i] = eng.Trigger(context.Background(), "mer-1", "")
		}(i)
	}
	wg.Wait()

	created := 0
	for i := 0; i < n; i++ {
		if errs[i] != nil {
			t.Fatalf("Trigger[%d]: %v", i, errs[i])
		}
		if results[i].Created {
			created++
		}
	}
	// Exactly one trigger does the work; the rest reuse its run.
	if created != 1 {
		t.Errorf("Created=true count = %d, want exactly 1", created)
	}
	if launcher.spawnCount != 1 {
		t.Errorf("reviewer spawn count = %d, want 1", launcher.spawnCount)
	}
	if len(store.runs) != 1 {
		t.Errorf("recorded review runs = %d, want 1", len(store.runs))
	}
}

func TestTriggerFallsBackToExistingRunOnUniqueConflict(t *testing.T) {
	// The idempotency check passes (no run yet), but the insert loses to a
	// concurrent writer the unique index already accepted.
	store := &fakeStore{insertErr: domain.ErrDuplicateReviewRun}
	launcher := &fakeLauncher{handle: "review-mer-1"}
	eng := newEngineForTest(store, fakeSessions{rec: liveWorker(), ok: true}, prAt("sha1"), fakeProjects{}, launcher)

	res, err := eng.Trigger(context.Background(), "mer-1", "")
	if err != nil {
		t.Fatalf("Trigger: %v", err)
	}
	if res.Created {
		t.Fatalf("expected Created=false on unique conflict: %+v", res)
	}
	if res.Run.TargetSHA != "sha1" || res.Run.ID != "winner-id-1" {
		t.Fatalf("expected the recorded winner run, got %+v", res.Run)
	}
	if launcher.spawnCount != 0 {
		t.Fatalf("reviewer should not launch after unique conflict: %+v", launcher)
	}
}

func TestTriggerIsIdempotentForSameCommit(t *testing.T) {
	store := &fakeStore{
		reviews: []domain.Review{{ID: "rev-1", SessionID: "mer-1", PRURL: "https://github.com/o/r/pull/1", ReviewerHandleID: "review-mer-1"}},
		runs:    []domain.ReviewRun{{ID: "run-1", SessionID: "mer-1", PRURL: "https://github.com/o/r/pull/1", TargetSHA: "sha1", Status: domain.ReviewRunRunning}},
	}
	launcher := &fakeLauncher{}
	eng := newEngineForTest(store, fakeSessions{rec: liveWorker(), ok: true}, prAt("sha1"), fakeProjects{}, launcher)

	res, err := eng.Trigger(context.Background(), "mer-1", "")
	if err != nil {
		t.Fatalf("Trigger: %v", err)
	}
	if res.Created || res.Run.ID != "run-1" || res.ReviewerHandleID != "review-mer-1" {
		t.Fatalf("expected reuse of existing run: %+v", res)
	}
	if launcher.spawned || launcher.notified {
		t.Fatalf("should not launch for an already-reviewed commit: %+v", launcher)
	}
	if len(store.runs) != 1 {
		t.Fatalf("should not insert another run: %+v", store.runs)
	}
}

func TestTriggerNotifiesLiveReviewerOnNewCommit(t *testing.T) {
	store := &fakeStore{
		reviews: []domain.Review{{ID: "rev-1", SessionID: "mer-1", PRURL: "https://github.com/o/r/pull/1", ReviewerHandleID: "review-mer-1"}},
		runs:    []domain.ReviewRun{{ID: "run-0", SessionID: "mer-1", PRURL: "https://github.com/o/r/pull/1", TargetSHA: "sha0", Status: domain.ReviewRunComplete}},
	}
	launcher := &fakeLauncher{alive: true}
	eng := newEngineForTest(store, fakeSessions{rec: liveWorker(), ok: true}, prAt("sha1"), fakeProjects{}, launcher)

	res, err := eng.Trigger(context.Background(), "mer-1", "")
	if err != nil {
		t.Fatalf("Trigger: %v", err)
	}
	if !launcher.notified || launcher.spawned {
		t.Fatalf("expected notify on live reviewer: %+v", launcher)
	}
	if launcher.gotHandle != "review-mer-1" {
		t.Fatalf("notify handle = %q", launcher.gotHandle)
	}
	if !res.Created || res.Run.TargetSHA != "sha1" || len(store.runs) != 2 {
		t.Fatalf("expected a new run for sha1: res=%+v runs=%+v", res, store.runs)
	}
}

func TestTriggerSpawnsWhenReviewerDead(t *testing.T) {
	store := &fakeStore{
		reviews: []domain.Review{{ID: "rev-1", SessionID: "mer-1", PRURL: "https://github.com/o/r/pull/1", ReviewerHandleID: "review-mer-1"}},
		runs:    []domain.ReviewRun{{ID: "run-0", SessionID: "mer-1", PRURL: "https://github.com/o/r/pull/1", TargetSHA: "sha0", Status: domain.ReviewRunComplete}},
	}
	launcher := &fakeLauncher{alive: false, handle: "review-mer-1"}
	eng := newEngineForTest(store, fakeSessions{rec: liveWorker(), ok: true}, prAt("sha1"), fakeProjects{}, launcher)

	if _, err := eng.Trigger(context.Background(), "mer-1", ""); err != nil {
		t.Fatalf("Trigger: %v", err)
	}
	if !launcher.spawned || launcher.notified {
		t.Fatalf("expected spawn when reviewer dead: %+v", launcher)
	}
}

func TestTriggerLaunchFailureRecordsFailedRun(t *testing.T) {
	store := &fakeStore{}
	launcher := &fakeLauncher{spawnErr: fmt.Errorf("claude: %w", ports.ErrAgentBinaryNotFound)}
	eng := newEngineForTest(store, fakeSessions{rec: liveWorker(), ok: true}, prAt("sha1"), fakeProjects{}, launcher)

	if _, err := eng.Trigger(context.Background(), "mer-1", ""); !errors.Is(err, ports.ErrAgentBinaryNotFound) {
		t.Fatalf("err = %v, want ports.ErrAgentBinaryNotFound", err)
	}
	if len(store.reviews) != 1 || len(store.runs) != 1 {
		t.Fatalf("expected persisted failed review/run: reviews=%+v runs=%+v", store.reviews, store.runs)
	}
	run := store.runs[0]
	if run.Status != domain.ReviewRunFailed || run.Verdict != domain.VerdictNone {
		t.Fatalf("run = %+v, want failed with no verdict", run)
	}
	if !strings.Contains(run.Body, "claude") || !strings.Contains(run.Body, ports.ErrAgentBinaryNotFound.Error()) {
		t.Fatalf("run body = %q, want launch cause", run.Body)
	}
}

func TestTriggerRetriesAfterFailedRunForSameCommit(t *testing.T) {
	store := &fakeStore{
		reviews: []domain.Review{{ID: "rev-1", SessionID: "mer-1", ReviewerHandleID: "review-mer-1"}},
		runs:    []domain.ReviewRun{{ID: "run-failed", ReviewID: "rev-1", SessionID: "mer-1", TargetSHA: "sha1", Status: domain.ReviewRunFailed}},
	}
	launcher := &fakeLauncher{handle: "review-mer-1"}
	eng := newEngineForTest(store, fakeSessions{rec: liveWorker(), ok: true}, prAt("sha1"), fakeProjects{}, launcher)

	res, err := eng.Trigger(context.Background(), "mer-1", "")
	if err != nil {
		t.Fatalf("Trigger: %v", err)
	}
	if !res.Created || res.Run.ID == "run-failed" {
		t.Fatalf("expected retry to create a new run, got %+v", res)
	}
	if len(store.runs) != 2 || !launcher.spawned {
		t.Fatalf("expected new launch/run after failed pass: launched=%v runs=%+v", launcher.spawned, store.runs)
	}
}

func TestTriggerUsesConfiguredReviewerHarness(t *testing.T) {
	store := &fakeStore{}
	projects := fakeProjects{cfg: domain.ProjectConfig{Reviewers: []domain.ReviewerConfig{{Harness: domain.ReviewerHarness("greptile")}}}}
	launcher := &fakeLauncher{handle: "review-mer-1"}
	eng := newEngineForTest(store, fakeSessions{rec: liveWorker(), ok: true}, prAt("sha1"), projects, launcher)

	res, err := eng.Trigger(context.Background(), "mer-1", "")
	if err != nil {
		t.Fatalf("Trigger: %v", err)
	}
	if res.Run.Harness != domain.ReviewerHarness("greptile") || launcher.gotSpec.Harness != domain.ReviewerHarness("greptile") {
		t.Fatalf("harness not used: run=%+v spec=%+v", res.Run, launcher.gotSpec)
	}
}

func TestTriggerRejectsBadWorkerState(t *testing.T) {
	t.Run("unknown worker", func(t *testing.T) {
		eng := newEngineForTest(&fakeStore{}, fakeSessions{ok: false}, prAt("sha1"), fakeProjects{}, &fakeLauncher{})
		if _, err := eng.Trigger(context.Background(), "mer-1", ""); !errors.Is(err, ErrNotFound) {
			t.Fatalf("err = %v, want ErrNotFound", err)
		}
	})
	t.Run("no pr", func(t *testing.T) {
		eng := newEngineForTest(&fakeStore{}, fakeSessions{rec: liveWorker(), ok: true}, fakePRs{}, fakeProjects{}, &fakeLauncher{})
		if _, err := eng.Trigger(context.Background(), "mer-1", ""); !errors.Is(err, ErrInvalid) {
			t.Fatalf("err = %v, want ErrInvalid", err)
		}
	})
	t.Run("multiple prs require selector", func(t *testing.T) {
		prs := fakePRs{prs: []domain.PullRequest{
			{URL: "https://github.com/o/r/pull/1", HeadSHA: "sha1"},
			{URL: "https://github.com/o/r2/pull/2", HeadSHA: "sha2"},
		}}
		eng := newEngineForTest(&fakeStore{}, fakeSessions{rec: liveWorker(), ok: true}, prs, fakeProjects{}, &fakeLauncher{})
		if _, err := eng.Trigger(context.Background(), "mer-1", ""); !errors.Is(err, ErrInvalid) {
			t.Fatalf("err = %v, want ErrInvalid", err)
		}
	})
	t.Run("unknown selected pr", func(t *testing.T) {
		eng := newEngineForTest(&fakeStore{}, fakeSessions{rec: liveWorker(), ok: true}, prAt("sha1"), fakeProjects{}, &fakeLauncher{})
		if _, err := eng.Trigger(context.Background(), "mer-1", "https://github.com/o/r/pull/404"); !errors.Is(err, ErrInvalid) {
			t.Fatalf("err = %v, want ErrInvalid", err)
		}
	})
}

func TestTriggerUsesSelectedPRAndScopesIdempotencyByPR(t *testing.T) {
	store := &fakeStore{
		reviews: []domain.Review{{ID: "rev-1", SessionID: "mer-1", PRURL: "https://github.com/o/api/pull/1", ReviewerHandleID: "review-api"}},
		runs:    []domain.ReviewRun{{ID: "run-api", ReviewID: "rev-1", SessionID: "mer-1", PRURL: "https://github.com/o/api/pull/1", TargetSHA: "same-sha", Status: domain.ReviewRunRunning}},
	}
	prs := fakePRs{prs: []domain.PullRequest{
		{URL: "https://github.com/o/api/pull/1", HeadSHA: "same-sha"},
		{URL: "https://github.com/o/web/pull/2", HeadSHA: "same-sha"},
	}}
	launcher := &fakeLauncher{handle: "review-web"}
	eng := newEngineForTest(store, fakeSessions{rec: liveWorker(), ok: true}, prs, fakeProjects{}, launcher)

	res, err := eng.Trigger(context.Background(), "mer-1", "https://github.com/o/web/pull/2")
	if err != nil {
		t.Fatalf("Trigger: %v", err)
	}
	if !res.Created || res.Run.PRURL != "https://github.com/o/web/pull/2" || res.Run.ID == "run-api" {
		t.Fatalf("selected PR did not get its own run: %+v", res)
	}
	if !launcher.spawned || launcher.gotSpec.PRURL != "https://github.com/o/web/pull/2" {
		t.Fatalf("launcher spec = %+v", launcher.gotSpec)
	}
}

func TestSubmitRecordsVerdictAndBody(t *testing.T) {
	store := &fakeStore{runs: []domain.ReviewRun{{ID: "run-1", SessionID: "mer-1", Status: domain.ReviewRunRunning}}}
	eng := newEngineForTest(store, fakeSessions{rec: liveWorker(), ok: true}, prAt("sha1"), fakeProjects{}, &fakeLauncher{})

	run, err := eng.Submit(context.Background(), "mer-1", "run-1", domain.VerdictChangesRequested, "please fix", "")
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if run.Status != domain.ReviewRunComplete || run.Verdict != domain.VerdictChangesRequested || run.Body != "please fix" {
		t.Fatalf("run = %+v", run)
	}
}

func newEngineWithMessenger(store Store, messenger ports.AgentMessenger) *Engine {
	ids := 0
	return New(Deps{
		Store: store, Sessions: fakeSessions{rec: liveWorker(), ok: true}, PRs: prAt("sha1"),
		Projects: fakeProjects{}, Launcher: &fakeLauncher{}, Messenger: messenger,
		Clock: func() time.Time { return time.Unix(0, 0).UTC() },
		NewID: func() string { ids++; return "id-" + string(rune('0'+ids)) },
	})
}

func TestSubmitChangesRequestedMessagesWorker(t *testing.T) {
	store := &fakeStore{runs: []domain.ReviewRun{{ID: "run-1", SessionID: "mer-1", Status: domain.ReviewRunRunning}}}
	msgr := &fakeMessenger{}
	eng := newEngineWithMessenger(store, msgr)

	run, err := eng.Submit(context.Background(), "mer-1", "run-1", domain.VerdictChangesRequested, "fix the bug", "98\x1b[2J765")
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if run.GithubReviewID != "98\x1b[2J765" || store.runs[0].GithubReviewID != "98\x1b[2J765" {
		t.Fatalf("review id not persisted: run=%+v stored=%+v", run, store.runs[0])
	}
	if msgr.sends != 1 || msgr.gotID != "mer-1" {
		t.Fatalf("expected one message to worker mer-1, got %+v", msgr)
	}
	if !strings.Contains(msgr.gotMsg, "fix the bug") || !strings.Contains(msgr.gotMsg, "98[2J765") {
		t.Fatalf("message missing body or review id: %q", msgr.gotMsg)
	}
	if strings.Contains(msgr.gotMsg, "\x1b") {
		t.Fatalf("message should sanitize review id before sending to worker: %q", msgr.gotMsg)
	}
	// The worker must be able to tell an AO internal review from external SCM
	// reviewer feedback, and is asked to reply + resolve for an AO review.
	if !strings.Contains(msgr.gotMsg, "AO code reviewer") {
		t.Fatalf("message should identify the AO internal review: %q", msgr.gotMsg)
	}
	if !strings.Contains(msgr.gotMsg, "reply on that review") || !strings.Contains(msgr.gotMsg, "resolve") {
		t.Fatalf("message should ask the worker to reply and resolve: %q", msgr.gotMsg)
	}
}

func TestSubmitApprovedDoesNotMessageWorker(t *testing.T) {
	store := &fakeStore{runs: []domain.ReviewRun{{ID: "run-1", SessionID: "mer-1", Status: domain.ReviewRunRunning}}}
	msgr := &fakeMessenger{}
	eng := newEngineWithMessenger(store, msgr)

	if _, err := eng.Submit(context.Background(), "mer-1", "run-1", domain.VerdictApproved, "", "98765"); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if msgr.sends != 0 {
		t.Fatalf("approved review should not message the worker: %+v", msgr)
	}
}

func TestSubmitChangesRequestedOmitsReviewIDWhenAbsent(t *testing.T) {
	store := &fakeStore{runs: []domain.ReviewRun{{ID: "run-1", SessionID: "mer-1", Status: domain.ReviewRunRunning}}}
	msgr := &fakeMessenger{}
	eng := newEngineWithMessenger(store, msgr)

	if _, err := eng.Submit(context.Background(), "mer-1", "run-1", domain.VerdictChangesRequested, "fix it", ""); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if msgr.sends != 1 || !strings.Contains(msgr.gotMsg, "fix it") {
		t.Fatalf("expected message with body: %+v", msgr)
	}
	// Still identified as an AO review, but with no id there is nothing to reply
	// to or resolve, so that instruction is omitted.
	if !strings.Contains(msgr.gotMsg, "AO code reviewer") {
		t.Fatalf("message should identify the AO internal review: %q", msgr.gotMsg)
	}
	if strings.Contains(msgr.gotMsg, "reply on that review") {
		t.Fatalf("message should not ask to reply/resolve when no review id was supplied: %q", msgr.gotMsg)
	}
}

func TestSubmitPropagatesMessengerError(t *testing.T) {
	store := &fakeStore{runs: []domain.ReviewRun{{ID: "run-1", SessionID: "mer-1", Status: domain.ReviewRunRunning}}}
	msgr := &fakeMessenger{sendErr: fmt.Errorf("dead pane")}
	eng := newEngineWithMessenger(store, msgr)

	if _, err := eng.Submit(context.Background(), "mer-1", "run-1", domain.VerdictChangesRequested, "fix it", "1"); err == nil {
		t.Fatal("expected Submit to surface the messenger error")
	}
	// The run must stay running so a retried submit can try again, rather than
	// being marked complete and then failing the status='running' guard.
	if store.runs[0].Status != domain.ReviewRunRunning {
		t.Fatalf("run should stay running after a failed send, got %q", store.runs[0].Status)
	}

	// A retry once the pane is back completes the run and messages the worker.
	msgr.sendErr = nil
	if _, err := eng.Submit(context.Background(), "mer-1", "run-1", domain.VerdictChangesRequested, "fix it", "1"); err != nil {
		t.Fatalf("retry after recovered send: %v", err)
	}
	if store.runs[0].Status != domain.ReviewRunComplete || msgr.sends != 2 {
		t.Fatalf("retry should complete the run and re-send: status=%q sends=%d", store.runs[0].Status, msgr.sends)
	}
}

func TestSubmitValidationAndOwnership(t *testing.T) {
	store := &fakeStore{runs: []domain.ReviewRun{{ID: "run-1", SessionID: "other", Status: domain.ReviewRunRunning}}}
	eng := newEngineForTest(store, fakeSessions{rec: liveWorker(), ok: true}, prAt("sha1"), fakeProjects{}, &fakeLauncher{})

	if _, err := eng.Submit(context.Background(), "mer-1", "", domain.VerdictApproved, "", ""); !errors.Is(err, ErrInvalid) {
		t.Fatalf("missing run id err = %v", err)
	}
	if _, err := eng.Submit(context.Background(), "mer-1", "run-1", "garbage", "b", ""); !errors.Is(err, ErrInvalid) {
		t.Fatalf("bad verdict err = %v", err)
	}
	if _, err := eng.Submit(context.Background(), "mer-1", "missing", domain.VerdictApproved, "", ""); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown run err = %v", err)
	}
	if _, err := eng.Submit(context.Background(), "mer-1", "run-1", domain.VerdictApproved, "", ""); !errors.Is(err, ErrInvalid) {
		t.Fatalf("ownership err = %v", err)
	}
}

func TestListReturnsHandleAndRuns(t *testing.T) {
	store := &fakeStore{
		reviews: []domain.Review{{ID: "rev-1", SessionID: "mer-1", PRURL: "https://github.com/o/r/pull/1", ReviewerHandleID: "review-mer-1"}},
		runs:    []domain.ReviewRun{{ID: "run-1", SessionID: "mer-1", PRURL: "https://github.com/o/r/pull/1", TargetSHA: "sha1"}},
	}
	eng := newEngineForTest(store, fakeSessions{rec: liveWorker(), ok: true}, prAt("sha1"), fakeProjects{}, &fakeLauncher{})
	got, err := eng.List(context.Background(), "mer-1", "")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if got.ReviewerHandleID != "review-mer-1" || len(got.Runs) != 1 || len(got.Targets) != 1 {
		t.Fatalf("list = %+v", got)
	}
}

func TestListCanSelectOnePR(t *testing.T) {
	store := &fakeStore{
		reviews: []domain.Review{
			{ID: "rev-api", SessionID: "mer-1", PRURL: "https://github.com/o/api/pull/1", ReviewerHandleID: "review-api"},
			{ID: "rev-web", SessionID: "mer-1", PRURL: "https://github.com/o/web/pull/2", ReviewerHandleID: "review-web"},
		},
		runs: []domain.ReviewRun{
			{ID: "run-api", SessionID: "mer-1", PRURL: "https://github.com/o/api/pull/1", TargetSHA: "sha1"},
			{ID: "run-web", SessionID: "mer-1", PRURL: "https://github.com/o/web/pull/2", TargetSHA: "sha2"},
		},
	}
	prs := fakePRs{prs: []domain.PullRequest{
		{URL: "https://github.com/o/api/pull/1", HeadSHA: "sha1"},
		{URL: "https://github.com/o/web/pull/2", HeadSHA: "sha2"},
	}}
	eng := newEngineForTest(store, fakeSessions{rec: liveWorker(), ok: true}, prs, fakeProjects{}, &fakeLauncher{})

	got, err := eng.List(context.Background(), "mer-1", "https://github.com/o/web/pull/2")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if got.ReviewerHandleID != "review-web" || len(got.Runs) != 1 || got.Runs[0].ID != "run-web" {
		t.Fatalf("selected list = %+v", got)
	}
	if len(got.Targets) != 1 || got.Targets[0].PRURL != "https://github.com/o/web/pull/2" {
		t.Fatalf("targets = %+v", got.Targets)
	}
}
