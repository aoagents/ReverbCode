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
func (f *fakeStore) UpdateReviewRunResult(_ context.Context, id string, status domain.ReviewRunStatus, verdict domain.ReviewVerdict, updatedAt time.Time) error {
	if f.updateErr != nil {
		return f.updateErr
	}
	for i := range f.runs {
		if f.runs[i].ID == id {
			f.runs[i].Status = status
			f.runs[i].Verdict = verdict
			f.runs[i].UpdatedAt = updatedAt
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

type fakePoster struct {
	verdict domain.ReviewVerdict
	body    string
	url     string
	err     error
	called  bool
}

func (f *fakePoster) PostPRReview(_ context.Context, prURL string, verdict domain.ReviewVerdict, body string) error {
	f.called = true
	f.url = prURL
	f.verdict = verdict
	f.body = body
	return f.err
}

func liveWorker() domain.SessionRecord {
	return domain.SessionRecord{
		ID:        "mer-1",
		ProjectID: "mer",
		Metadata:  domain.SessionMetadata{WorkspacePath: "/ws/mer-1"},
	}
}

func newServiceForTest(store Store, sessions Sessions, prs PRs, projects Projects, runner Runner, poster *fakePoster) *Service {
	ids := 0
	return New(Deps{
		Store: store, Sessions: sessions, PRs: prs, Projects: projects, Runner: runner, Poster: poster,
		Clock: func() time.Time { return time.Unix(0, 0).UTC() },
		NewID: func() string { ids++; return "id-" + string(rune('0'+ids)) },
	})
}

// --- tests ---

func TestTriggerCreatesPendingRunAndLaunchesReviewer(t *testing.T) {
	store := &fakeStore{}
	sessions := fakeSessions{rec: liveWorker(), ok: true}
	prs := fakePRs{prs: []domain.PullRequest{{URL: "https://github.com/o/r/pull/1"}}}
	projects := fakeProjects{cfg: domain.ProjectConfig{Reviewers: []domain.ReviewerConfig{{Harness: domain.HarnessCodex}}}}
	runner := &fakeRunner{}
	svc := newServiceForTest(store, sessions, prs, projects, runner, &fakePoster{})

	run, err := svc.Trigger(context.Background(), "mer-1")
	if err != nil {
		t.Fatalf("Trigger: %v", err)
	}
	if run.Status != domain.ReviewRunPending || run.Iteration != 1 || run.Harness != domain.HarnessCodex {
		t.Fatalf("run = %+v", run)
	}
	if !runner.ran || runner.spec.WorkspacePath != "/ws/mer-1" || runner.spec.Harness != domain.HarnessCodex {
		t.Fatalf("runner spec = %+v ran=%v", runner.spec, runner.ran)
	}
	if store.review == nil || store.review.PRURL != "https://github.com/o/r/pull/1" {
		t.Fatalf("review row = %+v", store.review)
	}
}

func TestTriggerDefaultsReviewerHarness(t *testing.T) {
	store := &fakeStore{}
	svc := newServiceForTest(store, fakeSessions{rec: liveWorker(), ok: true},
		fakePRs{prs: []domain.PullRequest{{URL: "u"}}}, fakeProjects{}, &fakeRunner{}, &fakePoster{})
	run, err := svc.Trigger(context.Background(), "mer-1")
	if err != nil {
		t.Fatalf("Trigger: %v", err)
	}
	if run.Harness != domain.DefaultReviewerHarness {
		t.Fatalf("harness = %q, want default %q", run.Harness, domain.DefaultReviewerHarness)
	}
}

func TestTriggerSecondPassIncrementsIteration(t *testing.T) {
	store := &fakeStore{runs: []domain.ReviewRun{{ID: "old", Iteration: 1}}}
	svc := newServiceForTest(store, fakeSessions{rec: liveWorker(), ok: true},
		fakePRs{prs: []domain.PullRequest{{URL: "u"}}}, fakeProjects{}, &fakeRunner{}, &fakePoster{})
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
		svc := newServiceForTest(base(), fakeSessions{ok: false}, fakePRs{}, fakeProjects{}, &fakeRunner{}, &fakePoster{})
		if _, err := svc.Trigger(context.Background(), "mer-1"); !errors.Is(err, ErrNotFound) {
			t.Fatalf("err = %v, want ErrNotFound", err)
		}
	})
	t.Run("terminated worker", func(t *testing.T) {
		rec := liveWorker()
		rec.IsTerminated = true
		svc := newServiceForTest(base(), fakeSessions{rec: rec, ok: true}, fakePRs{}, fakeProjects{}, &fakeRunner{}, &fakePoster{})
		if _, err := svc.Trigger(context.Background(), "mer-1"); !errors.Is(err, ErrInvalid) {
			t.Fatalf("err = %v, want ErrInvalid", err)
		}
	})
	t.Run("no pr", func(t *testing.T) {
		svc := newServiceForTest(base(), fakeSessions{rec: liveWorker(), ok: true}, fakePRs{}, fakeProjects{}, &fakeRunner{}, &fakePoster{})
		if _, err := svc.Trigger(context.Background(), "mer-1"); !errors.Is(err, ErrInvalid) {
			t.Fatalf("err = %v, want ErrInvalid", err)
		}
	})
}

func TestTriggerLaunchFailureMarksRunFailed(t *testing.T) {
	store := &fakeStore{}
	runner := &fakeRunner{err: errors.New("boom")}
	svc := newServiceForTest(store, fakeSessions{rec: liveWorker(), ok: true},
		fakePRs{prs: []domain.PullRequest{{URL: "u"}}}, fakeProjects{}, runner, &fakePoster{})
	if _, err := svc.Trigger(context.Background(), "mer-1"); err == nil {
		t.Fatal("want launch error")
	}
	if len(store.runs) != 1 || store.runs[0].Status != domain.ReviewRunFailed {
		t.Fatalf("run not marked failed: %+v", store.runs)
	}
}

func TestSubmitPostsToGitHubAndCompletes(t *testing.T) {
	store := &fakeStore{runs: []domain.ReviewRun{{ID: "run-1", PRURL: "https://github.com/o/r/pull/1", Status: domain.ReviewRunPending}}}
	poster := &fakePoster{}
	svc := newServiceForTest(store, fakeSessions{rec: liveWorker(), ok: true}, fakePRs{}, fakeProjects{}, &fakeRunner{}, poster)

	run, err := svc.Submit(context.Background(), "mer-1", domain.VerdictChangesRequested, "fix it")
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if !poster.called || poster.url != "https://github.com/o/r/pull/1" || poster.verdict != domain.VerdictChangesRequested || poster.body != "fix it" {
		t.Fatalf("poster = %+v", poster)
	}
	if run.Status != domain.ReviewRunComplete || run.Verdict != domain.VerdictChangesRequested {
		t.Fatalf("run = %+v", run)
	}
}

func TestSubmitValidation(t *testing.T) {
	store := &fakeStore{runs: []domain.ReviewRun{{ID: "run-1", Status: domain.ReviewRunPending}}}
	svc := newServiceForTest(store, fakeSessions{rec: liveWorker(), ok: true}, fakePRs{}, fakeProjects{}, &fakeRunner{}, &fakePoster{})

	if _, err := svc.Submit(context.Background(), "mer-1", "garbage", "b"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("bad verdict err = %v", err)
	}
	if _, err := svc.Submit(context.Background(), "mer-1", domain.VerdictChangesRequested, ""); !errors.Is(err, ErrInvalid) {
		t.Fatalf("empty body err = %v", err)
	}
}

func TestSubmitNoRun(t *testing.T) {
	svc := newServiceForTest(&fakeStore{}, fakeSessions{rec: liveWorker(), ok: true}, fakePRs{}, fakeProjects{}, &fakeRunner{}, &fakePoster{})
	if _, err := svc.Submit(context.Background(), "mer-1", domain.VerdictApproved, ""); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestSubmitPostFailureMarksFailed(t *testing.T) {
	store := &fakeStore{runs: []domain.ReviewRun{{ID: "run-1", PRURL: "u", Status: domain.ReviewRunPending}}}
	poster := &fakePoster{err: errors.New("network")}
	svc := newServiceForTest(store, fakeSessions{rec: liveWorker(), ok: true}, fakePRs{}, fakeProjects{}, &fakeRunner{}, poster)
	if _, err := svc.Submit(context.Background(), "mer-1", domain.VerdictApproved, ""); err == nil {
		t.Fatal("want post error")
	}
	if store.runs[0].Status != domain.ReviewRunFailed {
		t.Fatalf("run not marked failed: %+v", store.runs[0])
	}
}
