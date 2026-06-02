package prpoller

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

type fakeSessions struct {
	recs []domain.SessionRecord
	err  error
}

func (f fakeSessions) ListAllSessions(context.Context) ([]domain.SessionRecord, error) {
	return f.recs, f.err
}

type discoverCall struct{ owner, repo, branch string }

type fakeDiscoverer struct {
	mu    sync.Mutex
	calls []discoverCall
	url   string
	found bool
	err   error
}

func (f *fakeDiscoverer) FindPRForBranch(_ context.Context, owner, repo, branch string) (string, bool, error) {
	f.mu.Lock()
	f.calls = append(f.calls, discoverCall{owner, repo, branch})
	f.mu.Unlock()
	return f.url, f.found, f.err
}

type fakeObserver struct {
	mu   sync.Mutex
	urls []string
	obs  ports.PRObservation
	err  error
}

func (f *fakeObserver) Observe(_ context.Context, prURL string) (ports.PRObservation, error) {
	f.mu.Lock()
	f.urls = append(f.urls, prURL)
	f.mu.Unlock()
	o := f.obs
	o.URL = prURL
	return o, f.err
}

type sinkCall struct {
	id  domain.SessionID
	obs ports.PRObservation
}

type fakeSink struct {
	mu    sync.Mutex
	calls []sinkCall
	err   error
}

func (f *fakeSink) ApplyObservation(_ context.Context, id domain.SessionID, o ports.PRObservation) error {
	f.mu.Lock()
	f.calls = append(f.calls, sinkCall{id, o})
	f.mu.Unlock()
	return f.err
}

type fakeRepos struct {
	mu    sync.Mutex
	calls int
	owner string
	repo  string
	err   error
}

func (f *fakeRepos) RepoIdent(context.Context, domain.ProjectID) (string, string, error) {
	f.mu.Lock()
	f.calls++
	f.mu.Unlock()
	return f.owner, f.repo, f.err
}

func testLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func sessionWithBranch(id domain.SessionID, project domain.ProjectID, branch string) domain.SessionRecord {
	return domain.SessionRecord{ID: id, ProjectID: project, Metadata: domain.SessionMetadata{Branch: branch}}
}

func TestTick_DiscoversObservesAndApplies(t *testing.T) {
	sessions := fakeSessions{recs: []domain.SessionRecord{
		sessionWithBranch("s1", "p1", "feature-x"),
	}}
	disc := &fakeDiscoverer{url: "https://github.com/octocat/hello/pull/7", found: true}
	obs := &fakeObserver{obs: ports.PRObservation{Fetched: true, Number: 7, CI: domain.CIFailing}}
	sink := &fakeSink{}
	repos := &fakeRepos{owner: "octocat", repo: "hello"}

	p := New(sessions, disc, obs, sink, repos, Config{Logger: testLogger()})
	if err := p.Tick(context.Background()); err != nil {
		t.Fatalf("Tick: %v", err)
	}

	if len(disc.calls) != 1 || disc.calls[0] != (discoverCall{"octocat", "hello", "feature-x"}) {
		t.Fatalf("discover calls = %+v", disc.calls)
	}
	if len(obs.urls) != 1 || obs.urls[0] != "https://github.com/octocat/hello/pull/7" {
		t.Fatalf("observe urls = %+v", obs.urls)
	}
	if len(sink.calls) != 1 || sink.calls[0].id != "s1" || sink.calls[0].obs.CI != domain.CIFailing {
		t.Fatalf("sink calls = %+v", sink.calls)
	}
}

func TestTick_SkipsTerminatedAndBranchless(t *testing.T) {
	term := sessionWithBranch("s1", "p1", "feature-x")
	term.IsTerminated = true
	noBranch := sessionWithBranch("s2", "p1", "")
	sessions := fakeSessions{recs: []domain.SessionRecord{term, noBranch}}
	disc := &fakeDiscoverer{found: true, url: "u"}
	p := New(sessions, disc, &fakeObserver{}, &fakeSink{}, &fakeRepos{owner: "o", repo: "r"}, Config{Logger: testLogger()})

	if err := p.Tick(context.Background()); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if len(disc.calls) != 0 {
		t.Fatalf("expected no discovery for terminated/branchless sessions, got %+v", disc.calls)
	}
}

func TestTick_NoOpenPRSkipsObserve(t *testing.T) {
	sessions := fakeSessions{recs: []domain.SessionRecord{sessionWithBranch("s1", "p1", "b")}}
	disc := &fakeDiscoverer{found: false}
	obs := &fakeObserver{}
	sink := &fakeSink{}
	p := New(sessions, disc, obs, sink, &fakeRepos{owner: "o", repo: "r"}, Config{Logger: testLogger()})

	if err := p.Tick(context.Background()); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if len(obs.urls) != 0 {
		t.Fatal("must not observe when no open PR")
	}
	if len(sink.calls) != 0 {
		t.Fatal("must not apply when no open PR")
	}
}

func TestTick_RepoResolutionCachedPerProject(t *testing.T) {
	sessions := fakeSessions{recs: []domain.SessionRecord{
		sessionWithBranch("s1", "p1", "b1"),
		sessionWithBranch("s2", "p1", "b2"),
		sessionWithBranch("s3", "p1", "b3"),
	}}
	repos := &fakeRepos{owner: "o", repo: "r"}
	disc := &fakeDiscoverer{found: false}
	p := New(sessions, disc, &fakeObserver{}, &fakeSink{}, repos, Config{Logger: testLogger()})

	if err := p.Tick(context.Background()); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if repos.calls != 1 {
		t.Fatalf("RepoIdent calls = %d, want 1 (cached per project)", repos.calls)
	}
	if len(disc.calls) != 3 {
		t.Fatalf("discover calls = %d, want 3", len(disc.calls))
	}
}

func TestTick_RepoResolutionFailureSkipsSession(t *testing.T) {
	sessions := fakeSessions{recs: []domain.SessionRecord{sessionWithBranch("s1", "p1", "b")}}
	disc := &fakeDiscoverer{}
	repos := &fakeRepos{err: errors.New("no origin remote")}
	p := New(sessions, disc, &fakeObserver{}, &fakeSink{}, repos, Config{Logger: testLogger()})

	if err := p.Tick(context.Background()); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if len(disc.calls) != 0 {
		t.Fatal("must not discover when repo resolution fails")
	}
}

func TestTick_ObserveFailureDoesNotApply(t *testing.T) {
	sessions := fakeSessions{recs: []domain.SessionRecord{sessionWithBranch("s1", "p1", "b")}}
	disc := &fakeDiscoverer{found: true, url: "u"}
	obs := &fakeObserver{err: errors.New("rate limited")}
	sink := &fakeSink{}
	p := New(sessions, disc, obs, sink, &fakeRepos{owner: "o", repo: "r"}, Config{Logger: testLogger()})

	if err := p.Tick(context.Background()); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if len(sink.calls) != 0 {
		t.Fatal("must not apply a failed observation")
	}
}

func TestTick_ListErrorPropagates(t *testing.T) {
	sentinel := errors.New("db down")
	p := New(fakeSessions{err: sentinel}, &fakeDiscoverer{}, &fakeObserver{}, &fakeSink{}, &fakeRepos{}, Config{Logger: testLogger()})
	if err := p.Tick(context.Background()); !errors.Is(err, sentinel) {
		t.Fatalf("Tick err = %v, want sentinel", err)
	}
}
