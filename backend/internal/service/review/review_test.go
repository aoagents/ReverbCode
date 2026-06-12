package review

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// --- fakes ---

type fakeStore struct {
	review    *domain.Review
	runs      []domain.ReviewRun
	upsertErr error
	insertErr error
	updateErr error
}

func (f *fakeStore) UpsertReview(_ context.Context, r domain.Review) error {
	if f.upsertErr != nil {
		return f.upsertErr
	}
	cp := r
	f.review = &cp
	return nil
}
func (f *fakeStore) GetReviewBySession(_ context.Context, _ domain.SessionID) (domain.Review, bool, error) {
	if f.review == nil {
		return domain.Review{}, false, nil
	}
	return *f.review, true, nil
}
func (f *fakeStore) InsertReviewRun(_ context.Context, r domain.ReviewRun) error {
	if f.insertErr != nil {
		return f.insertErr
	}
	f.runs = append(f.runs, r)
	return nil
}
func (f *fakeStore) UpdateReviewRunResult(_ context.Context, id string, status domain.ReviewRunStatus, verdict domain.ReviewVerdict, body string) error {
	if f.updateErr != nil {
		return f.updateErr
	}
	for i := range f.runs {
		if f.runs[i].ID == id {
			f.runs[i].Status = status
			f.runs[i].Verdict = verdict
			f.runs[i].Body = body
		}
	}
	return nil
}
func (f *fakeStore) GetLatestReviewRunBySession(_ context.Context, _ domain.SessionID) (domain.ReviewRun, bool, error) {
	if len(f.runs) == 0 {
		return domain.ReviewRun{}, false, nil
	}
	return f.runs[len(f.runs)-1], true, nil
}
func (f *fakeStore) ListReviewRunsBySession(_ context.Context, _ domain.SessionID) ([]domain.ReviewRun, error) {
	return f.runs, nil
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

type fakeRunner struct {
	spec RunSpec
	err  error
	ran  bool
}

func (f *fakeRunner) Run(_ context.Context, spec RunSpec) error {
	f.ran = true
	f.spec = spec
	return f.err
}

func liveWorker() domain.SessionRecord {
	return domain.SessionRecord{
		ID:        "mer-1",
		ProjectID: "mer",
		Harness:   domain.HarnessCodex,
		Metadata:  domain.SessionMetadata{WorkspacePath: "/ws/mer-1"},
	}
}

func newServiceForTest(store Store, sessions Sessions, prs PRs, projects Projects, runner Runner) *Service {
	ids := 0
	return New(Deps{
		Store: store, Sessions: sessions, PRs: prs, Projects: projects, Runner: runner,
		Clock: func() time.Time { return time.Unix(0, 0).UTC() },
		NewID: func() string { ids++; return "id-" + string(rune('0'+ids)) },
	})
}

// --- tests ---

func TestTriggerCreatesPendingRunAndLaunchesReviewer(t *testing.T) {
	store := &fakeStore{}
	sessions := fakeSessions{rec: liveWorker(), ok: true}
	prs := fakePRs{prs: []domain.PullRequest{{URL: "https://github.com/o/r/pull/1"}}}
	projects := fakeProjects{cfg: domain.ProjectConfig{Reviewers: []domain.ReviewerConfig{{Harness: domain.HarnessAider}}}}
	runner := &fakeRunner{}
	svc := newServiceForTest(store, sessions, prs, projects, runner)

	run, err := svc.Trigger(context.Background(), "mer-1")
	if err != nil {
		t.Fatalf("Trigger: %v", err)
	}
	// A configured reviewer wins over the worker harness.
	if run.Status != domain.ReviewRunRunning || run.Iteration != 1 || run.Harness != domain.HarnessAider {
		t.Fatalf("run = %+v", run)
	}
	if !runner.ran || runner.spec.WorkspacePath != "/ws/mer-1" || runner.spec.Harness != domain.HarnessAider {
		t.Fatalf("runner spec = %+v ran=%v", runner.spec, runner.ran)
	}
	if store.review == nil || store.review.PRURL != "https://github.com/o/r/pull/1" {
		t.Fatalf("review row = %+v", store.review)
	}
}

func TestTriggerDefaultsToWorkerHarness(t *testing.T) {
	store := &fakeStore{}
	// No reviewer configured: reuse the worker's harness (codex).
	svc := newServiceForTest(store, fakeSessions{rec: liveWorker(), ok: true},
		fakePRs{prs: []domain.PullRequest{{URL: "u"}}}, fakeProjects{}, &fakeRunner{})
	run, err := svc.Trigger(context.Background(), "mer-1")
	if err != nil {
		t.Fatalf("Trigger: %v", err)
	}
	if run.Harness != domain.HarnessCodex {
		t.Fatalf("harness = %q, want worker harness codex", run.Harness)
	}
}

func TestTriggerFallsBackWhenWorkerHarnessUnknown(t *testing.T) {
	store := &fakeStore{}
	rec := liveWorker()
	rec.Harness = ""
	svc := newServiceForTest(store, fakeSessions{rec: rec, ok: true},
		fakePRs{prs: []domain.PullRequest{{URL: "u"}}}, fakeProjects{}, &fakeRunner{})
	run, err := svc.Trigger(context.Background(), "mer-1")
	if err != nil {
		t.Fatalf("Trigger: %v", err)
	}
	if run.Harness != domain.FallbackReviewerHarness {
		t.Fatalf("harness = %q, want fallback %q", run.Harness, domain.FallbackReviewerHarness)
	}
}

func TestTriggerSecondPassIncrementsIteration(t *testing.T) {
	store := &fakeStore{runs: []domain.ReviewRun{{ID: "old", Iteration: 1}}}
	svc := newServiceForTest(store, fakeSessions{rec: liveWorker(), ok: true},
		fakePRs{prs: []domain.PullRequest{{URL: "u"}}}, fakeProjects{}, &fakeRunner{})
	run, err := svc.Trigger(context.Background(), "mer-1")
	if err != nil {
		t.Fatalf("Trigger: %v", err)
	}
	if run.Iteration != 2 {
		t.Fatalf("iteration = %d, want 2", run.Iteration)
	}
}

func TestTriggerRejectsMissingWorkerPRAndState(t *testing.T) {
	base := func() *fakeStore { return &fakeStore{} }
	t.Run("unknown worker", func(t *testing.T) {
		svc := newServiceForTest(base(), fakeSessions{ok: false}, fakePRs{}, fakeProjects{}, &fakeRunner{})
		if _, err := svc.Trigger(context.Background(), "mer-1"); !errors.Is(err, ErrNotFound) {
			t.Fatalf("err = %v, want ErrNotFound", err)
		}
	})
	t.Run("terminated worker", func(t *testing.T) {
		rec := liveWorker()
		rec.IsTerminated = true
		svc := newServiceForTest(base(), fakeSessions{rec: rec, ok: true}, fakePRs{}, fakeProjects{}, &fakeRunner{})
		if _, err := svc.Trigger(context.Background(), "mer-1"); !errors.Is(err, ErrInvalid) {
			t.Fatalf("err = %v, want ErrInvalid", err)
		}
	})
	t.Run("no pr", func(t *testing.T) {
		svc := newServiceForTest(base(), fakeSessions{rec: liveWorker(), ok: true}, fakePRs{}, fakeProjects{}, &fakeRunner{})
		if _, err := svc.Trigger(context.Background(), "mer-1"); !errors.Is(err, ErrInvalid) {
			t.Fatalf("err = %v, want ErrInvalid", err)
		}
	})
}

func TestTriggerLaunchFailureMarksRunFailed(t *testing.T) {
	store := &fakeStore{}
	runner := &fakeRunner{err: errors.New("boom")}
	svc := newServiceForTest(store, fakeSessions{rec: liveWorker(), ok: true},
		fakePRs{prs: []domain.PullRequest{{URL: "u"}}}, fakeProjects{}, runner)
	if _, err := svc.Trigger(context.Background(), "mer-1"); err == nil {
		t.Fatal("want launch error")
	}
	if len(store.runs) != 1 || store.runs[0].Status != domain.ReviewRunFailed {
		t.Fatalf("run not marked failed: %+v", store.runs)
	}
}

func TestSubmitRecordsVerdictAndBody(t *testing.T) {
	store := &fakeStore{runs: []domain.ReviewRun{{ID: "run-1", PRURL: "u", Status: domain.ReviewRunRunning}}}
	svc := newServiceForTest(store, fakeSessions{rec: liveWorker(), ok: true}, fakePRs{}, fakeProjects{}, &fakeRunner{})

	run, err := svc.Submit(context.Background(), "mer-1", domain.VerdictChangesRequested, "please fix")
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if run.Status != domain.ReviewRunComplete || run.Verdict != domain.VerdictChangesRequested || run.Body != "please fix" {
		t.Fatalf("run = %+v", run)
	}
	if store.runs[0].Status != domain.ReviewRunComplete || store.runs[0].Body != "please fix" {
		t.Fatalf("persisted run = %+v", store.runs[0])
	}
}

func TestSubmitValidation(t *testing.T) {
	store := &fakeStore{runs: []domain.ReviewRun{{ID: "run-1", Status: domain.ReviewRunRunning}}}
	svc := newServiceForTest(store, fakeSessions{rec: liveWorker(), ok: true}, fakePRs{}, fakeProjects{}, &fakeRunner{})

	if _, err := svc.Submit(context.Background(), "mer-1", "garbage", "b"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("bad verdict err = %v", err)
	}
	if _, err := svc.Submit(context.Background(), "mer-1", domain.VerdictChangesRequested, ""); !errors.Is(err, ErrInvalid) {
		t.Fatalf("empty body err = %v", err)
	}
}

func TestSubmitNoRun(t *testing.T) {
	svc := newServiceForTest(&fakeStore{}, fakeSessions{rec: liveWorker(), ok: true}, fakePRs{}, fakeProjects{}, &fakeRunner{})
	if _, err := svc.Submit(context.Background(), "mer-1", domain.VerdictApproved, ""); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestListReturnsRuns(t *testing.T) {
	store := &fakeStore{runs: []domain.ReviewRun{{ID: "run-1", Iteration: 1}}}
	svc := newServiceForTest(store, fakeSessions{rec: liveWorker(), ok: true}, fakePRs{}, fakeProjects{}, &fakeRunner{})
	runs, err := svc.List(context.Background(), "mer-1")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(runs) != 1 || runs[0].ID != "run-1" {
		t.Fatalf("runs = %+v", runs)
	}
}
