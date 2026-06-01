package scm

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	scmgithub "github.com/aoagents/agent-orchestrator/backend/internal/adapters/scm/github"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/project"
)

// ---------------------------------------------------------------------------
// Fakes
// ---------------------------------------------------------------------------

type fakeProvider struct {
	mu      sync.Mutex
	calls   []string
	obs     map[string]ports.PRObservation
	errs    map[string]error
	hangFor time.Duration
}

func (f *fakeProvider) Observe(ctx context.Context, prURL string) (ports.PRObservation, error) {
	f.mu.Lock()
	f.calls = append(f.calls, prURL)
	hang := f.hangFor
	f.mu.Unlock()
	if hang > 0 {
		select {
		case <-time.After(hang):
		case <-ctx.Done():
			return ports.PRObservation{URL: prURL}, ctx.Err()
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if err, ok := f.errs[prURL]; ok {
		return ports.PRObservation{URL: prURL}, err
	}
	if o, ok := f.obs[prURL]; ok {
		return o, nil
	}
	return ports.PRObservation{URL: prURL}, nil
}

func (f *fakeProvider) seenURLs() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, len(f.calls))
	copy(out, f.calls)
	return out
}

type fakeBranches struct {
	mu        sync.Mutex
	urls      map[string]string // owner/repo/branch -> prURL
	err       error
	callCount int
}

func (f *fakeBranches) FindOpenPRForBranch(_ context.Context, owner, repo, branch string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.callCount++
	if f.err != nil {
		return "", f.err
	}
	return f.urls[owner+"/"+repo+"/"+branch], nil
}

type fakeSessions struct {
	sessions []domain.SessionRecord
	err      error
}

func (f *fakeSessions) ListAllSessions(context.Context) ([]domain.SessionRecord, error) {
	if f.err != nil {
		return nil, f.err
	}
	out := make([]domain.SessionRecord, len(f.sessions))
	copy(out, f.sessions)
	return out, nil
}

type fakeProjects struct {
	projects map[domain.ProjectID]project.Project
}

func (f *fakeProjects) Get(_ context.Context, id domain.ProjectID) (project.GetResult, error) {
	p, ok := f.projects[id]
	if !ok {
		return project.GetResult{}, errors.New("project not found")
	}
	pp := p
	return project.GetResult{Status: "ok", Project: &pp}, nil
}

type fakePR struct {
	mu       sync.Mutex
	applied  []appliedObs
	applyErr error
}

type appliedObs struct {
	id  domain.SessionID
	obs ports.PRObservation
}

func (f *fakePR) ApplyObservation(_ context.Context, id domain.SessionID, o ports.PRObservation) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.applied = append(f.applied, appliedObs{id: id, obs: o})
	return f.applyErr
}

func (f *fakePR) records() []appliedObs {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]appliedObs, len(f.applied))
	copy(out, f.applied)
	return out
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func newTestPoller(t *testing.T, d Deps) *Poller {
	t.Helper()
	if d.Logger == nil {
		d.Logger = slog.New(slog.NewTextHandler(testWriter{t}, &slog.HandlerOptions{Level: slog.LevelDebug}))
	}
	return New(d)
}

type testWriter struct{ t *testing.T }

func (w testWriter) Write(p []byte) (int, error) {
	w.t.Log(string(p))
	return len(p), nil
}

func aliveSession(id domain.SessionID, project domain.ProjectID, branch string) domain.SessionRecord {
	return domain.SessionRecord{
		ID:        id,
		ProjectID: project,
		Kind:      domain.KindWorker,
		Metadata:  domain.SessionMetadata{Branch: branch, RuntimeHandleID: "h"},
	}
}

func terminatedSession(id domain.SessionID, project domain.ProjectID, branch string) domain.SessionRecord {
	s := aliveSession(id, project, branch)
	s.IsTerminated = true
	return s
}

func githubProject(id domain.ProjectID) project.Project {
	return project.Project{ID: id, Path: "/repo/" + string(id), Repo: "https://github.com/acme/repo.git"}
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

func TestTickObservesAliveSessionAndAppliesObservation(t *testing.T) {
	ctx := context.Background()
	sessions := &fakeSessions{sessions: []domain.SessionRecord{
		aliveSession("s-1", "acme", "feat/x"),
		terminatedSession("s-2", "acme", "feat/y"),
	}}
	projects := &fakeProjects{projects: map[domain.ProjectID]project.Project{"acme": githubProject("acme")}}
	branches := &fakeBranches{urls: map[string]string{
		"acme/repo/feat/x": "https://github.com/acme/repo/pull/11",
		"acme/repo/feat/y": "https://github.com/acme/repo/pull/12",
	}}
	provider := &fakeProvider{obs: map[string]ports.PRObservation{
		"https://github.com/acme/repo/pull/11": {Fetched: true, URL: "https://github.com/acme/repo/pull/11", Number: 11, CI: domain.CIPassing},
	}}
	prm := &fakePR{}

	p := newTestPoller(t, Deps{
		Provider: provider,
		Branches: branches,
		Sessions: sessions,
		Projects: projects,
		PR:       prm,
	})

	if err := p.Tick(ctx); err != nil {
		t.Fatalf("Tick error: %v", err)
	}

	if got := provider.seenURLs(); len(got) != 1 || got[0] != "https://github.com/acme/repo/pull/11" {
		t.Fatalf("provider.Observe calls = %v, want [pull/11] (terminated session skipped)", got)
	}
	rec := prm.records()
	if len(rec) != 1 || rec[0].id != "s-1" || rec[0].obs.Number != 11 {
		t.Fatalf("pr.ApplyObservation = %+v, want one call for s-1/pull-11", rec)
	}
}

func TestTickSkipsApplyWhenNotFetched(t *testing.T) {
	ctx := context.Background()
	sessions := &fakeSessions{sessions: []domain.SessionRecord{aliveSession("s-1", "acme", "feat/x")}}
	projects := &fakeProjects{projects: map[domain.ProjectID]project.Project{"acme": githubProject("acme")}}
	branches := &fakeBranches{urls: map[string]string{"acme/repo/feat/x": "https://github.com/acme/repo/pull/11"}}
	provider := &fakeProvider{obs: map[string]ports.PRObservation{
		"https://github.com/acme/repo/pull/11": {Fetched: false, URL: "https://github.com/acme/repo/pull/11"},
	}}
	prm := &fakePR{}
	p := newTestPoller(t, Deps{Provider: provider, Branches: branches, Sessions: sessions, Projects: projects, PR: prm})

	if err := p.Tick(ctx); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if got := prm.records(); len(got) != 0 {
		t.Fatalf("ApplyObservation called %d times on !Fetched obs", len(got))
	}
}

func TestTickSkipsSessionsWithoutBranch(t *testing.T) {
	ctx := context.Background()
	noBranch := aliveSession("s-1", "acme", "")
	sessions := &fakeSessions{sessions: []domain.SessionRecord{noBranch}}
	projects := &fakeProjects{projects: map[domain.ProjectID]project.Project{"acme": githubProject("acme")}}
	branches := &fakeBranches{}
	provider := &fakeProvider{}
	prm := &fakePR{}
	p := newTestPoller(t, Deps{Provider: provider, Branches: branches, Sessions: sessions, Projects: projects, PR: prm})

	if err := p.Tick(ctx); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if got := provider.seenURLs(); len(got) != 0 {
		t.Fatalf("provider should not be called for session without branch, got %v", got)
	}
	if got := branches.callCount; got != 0 {
		t.Fatalf("branches lookup should not be called for session without branch, got %d", got)
	}
}

func TestTickSkipsSessionsWithNoOpenPR(t *testing.T) {
	ctx := context.Background()
	sessions := &fakeSessions{sessions: []domain.SessionRecord{aliveSession("s-1", "acme", "feat/x")}}
	projects := &fakeProjects{projects: map[domain.ProjectID]project.Project{"acme": githubProject("acme")}}
	branches := &fakeBranches{urls: map[string]string{}} // empty: no PR exists
	provider := &fakeProvider{}
	prm := &fakePR{}
	p := newTestPoller(t, Deps{Provider: provider, Branches: branches, Sessions: sessions, Projects: projects, PR: prm})

	if err := p.Tick(ctx); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if got := provider.seenURLs(); len(got) != 0 {
		t.Fatalf("provider should not be called when no PR found, got %v", got)
	}
}

func TestTickRateLimitShortCircuits(t *testing.T) {
	ctx := context.Background()
	sessions := &fakeSessions{sessions: []domain.SessionRecord{
		aliveSession("s-1", "acme", "feat/x"),
		aliveSession("s-2", "acme", "feat/y"),
	}}
	projects := &fakeProjects{projects: map[domain.ProjectID]project.Project{"acme": githubProject("acme")}}
	branches := &fakeBranches{urls: map[string]string{
		"acme/repo/feat/x": "https://github.com/acme/repo/pull/11",
		"acme/repo/feat/y": "https://github.com/acme/repo/pull/12",
	}}
	provider := &fakeProvider{
		errs: map[string]error{
			"https://github.com/acme/repo/pull/11": scmgithub.ErrRateLimited,
		},
		obs: map[string]ports.PRObservation{
			"https://github.com/acme/repo/pull/12": {Fetched: true, URL: "https://github.com/acme/repo/pull/12", Number: 12},
		},
	}
	prm := &fakePR{}
	p := newTestPoller(t, Deps{Provider: provider, Branches: branches, Sessions: sessions, Projects: projects, PR: prm})

	if err := p.Tick(ctx); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if got := provider.seenURLs(); len(got) != 1 {
		t.Fatalf("expected exactly one Observe call (rate-limit short-circuits), got %v", got)
	}
	if got := prm.records(); len(got) != 0 {
		t.Fatalf("no observations should be applied after rate-limit, got %d", len(got))
	}
}

func TestTickAuthFailureMarksUnhealthyAndContinues(t *testing.T) {
	ctx := context.Background()
	sessions := &fakeSessions{sessions: []domain.SessionRecord{
		aliveSession("s-1", "acme", "feat/x"),
		aliveSession("s-2", "acme", "feat/y"),
	}}
	projects := &fakeProjects{projects: map[domain.ProjectID]project.Project{"acme": githubProject("acme")}}
	branches := &fakeBranches{urls: map[string]string{
		"acme/repo/feat/x": "https://github.com/acme/repo/pull/11",
		"acme/repo/feat/y": "https://github.com/acme/repo/pull/12",
	}}
	provider := &fakeProvider{
		errs: map[string]error{
			"https://github.com/acme/repo/pull/11": scmgithub.ErrAuthFailed,
		},
		obs: map[string]ports.PRObservation{
			"https://github.com/acme/repo/pull/12": {Fetched: true, URL: "https://github.com/acme/repo/pull/12", Number: 12, CI: domain.CIPassing},
		},
	}
	prm := &fakePR{}
	p := newTestPoller(t, Deps{Provider: provider, Branches: branches, Sessions: sessions, Projects: projects, PR: prm})
	if !p.Healthy() {
		t.Fatalf("poller should start healthy")
	}

	if err := p.Tick(ctx); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if p.Healthy() {
		t.Fatalf("poller should be unhealthy after ErrAuthFailed")
	}
	if got := provider.seenURLs(); len(got) != 2 {
		t.Fatalf("expected provider to be called for both sessions, got %v", got)
	}
	rec := prm.records()
	if len(rec) != 1 || rec[0].id != "s-2" {
		t.Fatalf("expected one apply for s-2 after auth failure on s-1, got %+v", rec)
	}
}

func TestTickProjectLookupErrorContinues(t *testing.T) {
	ctx := context.Background()
	sessions := &fakeSessions{sessions: []domain.SessionRecord{
		aliveSession("s-1", "missing", "feat/x"),
		aliveSession("s-2", "acme", "feat/y"),
	}}
	projects := &fakeProjects{projects: map[domain.ProjectID]project.Project{"acme": githubProject("acme")}}
	branches := &fakeBranches{urls: map[string]string{
		"acme/repo/feat/y": "https://github.com/acme/repo/pull/12",
	}}
	provider := &fakeProvider{obs: map[string]ports.PRObservation{
		"https://github.com/acme/repo/pull/12": {Fetched: true, URL: "https://github.com/acme/repo/pull/12", Number: 12},
	}}
	prm := &fakePR{}
	p := newTestPoller(t, Deps{Provider: provider, Branches: branches, Sessions: sessions, Projects: projects, PR: prm})

	if err := p.Tick(ctx); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if got := prm.records(); len(got) != 1 || got[0].id != "s-2" {
		t.Fatalf("expected s-2 applied after project-lookup err on s-1, got %+v", got)
	}
	if !p.Healthy() {
		t.Fatalf("project lookup error should not mark unhealthy")
	}
}

func TestTickGenericErrorContinues(t *testing.T) {
	ctx := context.Background()
	sessions := &fakeSessions{sessions: []domain.SessionRecord{
		aliveSession("s-1", "acme", "feat/x"),
		aliveSession("s-2", "acme", "feat/y"),
	}}
	projects := &fakeProjects{projects: map[domain.ProjectID]project.Project{"acme": githubProject("acme")}}
	branches := &fakeBranches{urls: map[string]string{
		"acme/repo/feat/x": "https://github.com/acme/repo/pull/11",
		"acme/repo/feat/y": "https://github.com/acme/repo/pull/12",
	}}
	provider := &fakeProvider{
		errs: map[string]error{
			"https://github.com/acme/repo/pull/11": errors.New("transient network blip"),
		},
		obs: map[string]ports.PRObservation{
			"https://github.com/acme/repo/pull/12": {Fetched: true, URL: "https://github.com/acme/repo/pull/12", Number: 12},
		},
	}
	prm := &fakePR{}
	p := newTestPoller(t, Deps{Provider: provider, Branches: branches, Sessions: sessions, Projects: projects, PR: prm})
	if err := p.Tick(ctx); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if got := prm.records(); len(got) != 1 || got[0].id != "s-2" {
		t.Fatalf("expected s-2 applied after generic err on s-1, got %+v", got)
	}
	if !p.Healthy() {
		t.Fatalf("generic errors should not mark unhealthy")
	}
}

func TestPerCallDeadline(t *testing.T) {
	ctx := context.Background()
	sessions := &fakeSessions{sessions: []domain.SessionRecord{aliveSession("s-1", "acme", "feat/x")}}
	projects := &fakeProjects{projects: map[domain.ProjectID]project.Project{"acme": githubProject("acme")}}
	branches := &fakeBranches{urls: map[string]string{"acme/repo/feat/x": "https://github.com/acme/repo/pull/11"}}
	provider := &fakeProvider{hangFor: 200 * time.Millisecond}
	prm := &fakePR{}
	p := newTestPoller(t, Deps{
		Provider:       provider,
		Branches:       branches,
		Sessions:       sessions,
		Projects:       projects,
		PR:             prm,
		ObserveTimeout: 10 * time.Millisecond,
	})
	start := time.Now()
	if err := p.Tick(ctx); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 150*time.Millisecond {
		t.Fatalf("Tick took %v — per-call deadline did not fire", elapsed)
	}
	if got := prm.records(); len(got) != 0 {
		t.Fatalf("no apply on deadline timeout, got %d", len(got))
	}
}

func TestStartDrainsOnContextCancel(t *testing.T) {
	sessions := &fakeSessions{}
	projects := &fakeProjects{}
	branches := &fakeBranches{}
	provider := &fakeProvider{}
	prm := &fakePR{}
	p := newTestPoller(t, Deps{
		Provider: provider, Branches: branches, Sessions: sessions, Projects: projects, PR: prm,
		Interval: 5 * time.Millisecond,
	})
	ctx, cancel := context.WithCancel(context.Background())
	done := p.Start(ctx)
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("poller did not exit within 1s of ctx cancel")
	}
}

func TestStartTicksRepeatedly(t *testing.T) {
	var ticks atomic.Int32
	sessions := &fakeSessions{}
	projects := &fakeProjects{}
	branches := &fakeBranches{}
	provider := &fakeProvider{}
	prm := &fakePR{}
	p := newTestPoller(t, Deps{
		Provider: provider,
		Branches: branches,
		Sessions: &countingSessions{wrap: sessions, ticks: &ticks},
		Projects: projects,
		PR:       prm,
		Interval: 5 * time.Millisecond,
	})
	ctx, cancel := context.WithCancel(context.Background())
	done := p.Start(ctx)
	deadline := time.After(500 * time.Millisecond)
loop:
	for ticks.Load() < 3 {
		select {
		case <-deadline:
			break loop
		case <-time.After(2 * time.Millisecond):
		}
	}
	cancel()
	<-done
	if ticks.Load() < 2 {
		t.Fatalf("expected at least 2 ticks, got %d", ticks.Load())
	}
}

// countingSessions ticks the counter each time ListAllSessions is called.
type countingSessions struct {
	wrap  *fakeSessions
	ticks *atomic.Int32
}

func (c *countingSessions) ListAllSessions(ctx context.Context) ([]domain.SessionRecord, error) {
	c.ticks.Add(1)
	return c.wrap.ListAllSessions(ctx)
}

// ---------------------------------------------------------------------------
// owner/repo derivation
// ---------------------------------------------------------------------------

func TestParseGitHubRemote(t *testing.T) {
	tests := []struct{ in, owner, repo string }{
		{"https://github.com/acme/repo.git", "acme", "repo"},
		{"https://github.com/acme/repo", "acme", "repo"},
		{"git@github.com:acme/repo.git", "acme", "repo"},
		{"ssh://git@github.com/acme/repo.git", "acme", "repo"},
		{"acme/repo", "acme", "repo"},
		{"", "", ""},
		{"https://gitlab.com/x/y", "x", "y"}, // host-agnostic parser; provider rejects non-GitHub at Observe time
	}
	for _, tc := range tests {
		owner, repo, ok := parseGitHubRemote(tc.in)
		if tc.owner == "" {
			if ok {
				t.Errorf("parseGitHubRemote(%q): expected !ok, got %q/%q", tc.in, owner, repo)
			}
			continue
		}
		if !ok || owner != tc.owner || repo != tc.repo {
			t.Errorf("parseGitHubRemote(%q) = %q/%q ok=%v; want %q/%q true", tc.in, owner, repo, ok, tc.owner, tc.repo)
		}
	}
}
