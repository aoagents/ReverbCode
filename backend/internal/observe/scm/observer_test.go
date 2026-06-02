package scm

// This file tests the SCM observer orchestration contract with fake provider,
// store, and lifecycle collaborators so ETag decisions, batching, log fetching,
// review cadence, semantic hashes, and notification behavior stay provider-neutral.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

var testRepo = ports.SCMRepo{Provider: "github", Host: "github.com", Owner: "o", Name: "r", Repo: "o/r"}

type fakeStore struct {
	mu sync.Mutex

	sessions []domain.SessionRecord
	projects map[string]domain.ProjectRecord
	prs      map[domain.SessionID][]domain.PullRequest
	checks   map[string][]domain.PullRequestCheck

	writes []fakeWrite

	listEntered chan struct{}
	listRelease chan struct{}
}

type fakeWrite struct {
	pr            domain.PullRequest
	checks        []domain.PullRequestCheck
	threads       []domain.PullRequestReviewThread
	comments      []domain.PullRequestComment
	replaceReview bool
}

func (s *fakeStore) ListAllSessions(ctx context.Context) ([]domain.SessionRecord, error) {
	if s.listEntered != nil {
		select {
		case <-s.listEntered:
		default:
			close(s.listEntered)
		}
	}
	if s.listRelease != nil {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-s.listRelease:
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]domain.SessionRecord(nil), s.sessions...), nil
}

func (s *fakeStore) GetProject(_ context.Context, id string) (domain.ProjectRecord, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.projects[id]
	return p, ok, nil
}

func (s *fakeStore) ListPRsBySession(_ context.Context, id domain.SessionID) ([]domain.PullRequest, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]domain.PullRequest(nil), s.prs[id]...), nil
}

func (s *fakeStore) ListChecks(_ context.Context, prURL string) ([]domain.PullRequestCheck, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]domain.PullRequestCheck(nil), s.checks[prURL]...), nil
}

func (s *fakeStore) WriteSCMObservation(_ context.Context, pr domain.PullRequest, checks []domain.PullRequestCheck, threads []domain.PullRequestReviewThread, comments []domain.PullRequestComment, replaceReview bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.writes = append(s.writes, fakeWrite{pr: pr, checks: append([]domain.PullRequestCheck(nil), checks...), threads: append([]domain.PullRequestReviewThread(nil), threads...), comments: append([]domain.PullRequestComment(nil), comments...), replaceReview: replaceReview})
	return nil
}

type fakeProvider struct {
	mu           sync.Mutex
	repoGuards   map[string]ports.SCMGuardResult
	checkGuards  map[string]ports.SCMGuardResult
	detected     map[string]ports.SCMPRObservation
	observations map[string]ports.SCMObservation
	reviews      map[string]ports.SCMReviewObservation
	logTails     map[string]string

	credentialGate   bool
	credentialOK     bool
	credentialErr    error
	credentialChecks int
	repoGuardCalls   int
	detectCalls      int
	fetchBatches     [][]ports.SCMPRRef
	logCalls         int
	reviewCalls      int
}

func (p *fakeProvider) SCMCredentialsAvailable(context.Context) (bool, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.credentialChecks++
	if !p.credentialGate {
		return true, nil
	}
	return p.credentialOK, p.credentialErr
}

func (p *fakeProvider) ParseRepository(remote string) (ports.SCMRepo, bool) {
	return testRepo, remote != ""
}
func (p *fakeProvider) RepoPRListGuard(_ context.Context, repo ports.SCMRepo, _ string) (ports.SCMGuardResult, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.repoGuardCalls++
	return p.repoGuards[prKey(repo, 0)], nil
}
func (p *fakeProvider) DetectPRByBranch(_ context.Context, _ ports.SCMRepo, branch string) (ports.SCMPRObservation, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.detectCalls++
	pr, ok := p.detected[branch]
	if !ok {
		return ports.SCMPRObservation{}, errors.New("not found")
	}
	return pr, nil
}
func (p *fakeProvider) CommitChecksGuard(_ context.Context, repo ports.SCMRepo, sha, _ string) (ports.SCMGuardResult, error) {
	return p.checkGuards[commitKey(repo, sha)], nil
}
func (p *fakeProvider) FetchPullRequests(_ context.Context, refs []ports.SCMPRRef) ([]ports.SCMObservation, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.fetchBatches = append(p.fetchBatches, append([]ports.SCMPRRef(nil), refs...))
	out := make([]ports.SCMObservation, 0, len(refs))
	for _, ref := range refs {
		if obs, ok := p.observations[prKey(ref.Repo, ref.Number)]; ok {
			out = append(out, obs)
		}
	}
	return out, nil
}
func (p *fakeProvider) FetchFailedCheckLogTail(_ context.Context, _ ports.SCMRepo, check ports.SCMCheckObservation) (string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.logCalls++
	return p.logTails[check.Name], nil
}
func (p *fakeProvider) FetchReviewThreads(_ context.Context, ref ports.SCMPRRef) (ports.SCMReviewObservation, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.reviewCalls++
	return p.reviews[prKey(ref.Repo, ref.Number)], nil
}

type fakeLifecycle struct{ observed []ports.SCMObservation }

func (l *fakeLifecycle) ApplySCMObservation(_ context.Context, _ domain.SessionID, obs ports.SCMObservation) error {
	l.observed = append(l.observed, obs)
	return nil
}

func newTestObserver(store *fakeStore, provider *fakeProvider, lc *fakeLifecycle, now time.Time) *Observer {
	return New(provider, store, lc, Config{Clock: func() time.Time { return now }, Tick: time.Hour, Logger: quietSlog(), CacheMax: 128})
}

func quietSlog() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func testStoreWithSession() *fakeStore {
	return &fakeStore{
		sessions: []domain.SessionRecord{{ID: "p-1", ProjectID: "p", Metadata: domain.SessionMetadata{Branch: "feat"}}},
		projects: map[string]domain.ProjectRecord{"p": {ID: "p", RepoOriginURL: "https://github.com/o/r.git"}},
		prs:      map[domain.SessionID][]domain.PullRequest{},
		checks:   map[string][]domain.PullRequestCheck{},
	}
}

func testObs(num int) ports.SCMObservation {
	return ports.SCMObservation{
		Fetched: true, Provider: "github", Host: "github.com", Repo: "o/r",
		PR:           ports.SCMPRObservation{URL: "https://github.com/o/r/pull/" + fmt.Sprint(num), Number: num, State: "open", SourceBranch: "feat", TargetBranch: "main", HeadSHA: "sha" + fmt.Sprint(num), Title: "PR"},
		CI:           ports.SCMCIObservation{Summary: string(domain.CIPassing), HeadSHA: "sha" + fmt.Sprint(num), Checks: []ports.SCMCheckObservation{{Name: "build", Status: string(domain.PRCheckPassed), Conclusion: "success", URL: "ci"}}},
		Review:       ports.SCMReviewObservation{Decision: string(domain.ReviewNone)},
		Mergeability: ports.SCMMergeabilityObservation{State: string(domain.MergeMergeable), Mergeable: true},
	}
}

func knownPR(num int) domain.PullRequest {
	obs := testObs(num)
	pr, _, _, _ := domainFromObservation("p-1", obs, domain.PullRequest{}, false, time.Unix(1, 0).UTC())
	return pr
}

func TestStartAsyncPerformsImmediatePollAndStopsOnCancel(t *testing.T) {
	store := testStoreWithSession()
	store.listEntered = make(chan struct{})
	store.listRelease = make(chan struct{})
	provider := &fakeProvider{repoGuards: map[string]ports.SCMGuardResult{}, observations: map[string]ports.SCMObservation{}}
	obs := newTestObserver(store, provider, &fakeLifecycle{}, time.Unix(1, 0).UTC())
	ctx, cancel := context.WithCancel(context.Background())
	done := obs.Start(ctx)
	select {
	case <-store.listEntered:
	case <-time.After(time.Second):
		t.Fatal("initial poll did not start asynchronously")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("observer did not exit after context cancellation")
	}
}

func TestPoll_DisablesOnceWhenCredentialsUnavailable(t *testing.T) {
	store := testStoreWithSession()
	provider := &fakeProvider{
		credentialGate: true,
		credentialOK:   false,
		repoGuards:     map[string]ports.SCMGuardResult{prKey(testRepo, 0): {ETag: "v1"}},
		observations:   map[string]ports.SCMObservation{},
	}
	obs := newTestObserver(store, provider, &fakeLifecycle{}, time.Unix(1, 0).UTC())
	if err := obs.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := obs.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if provider.credentialChecks != 1 {
		t.Fatalf("credential checks = %d, want one lazy check", provider.credentialChecks)
	}
	if provider.repoGuardCalls != 0 || provider.detectCalls != 0 || len(provider.fetchBatches) != 0 {
		t.Fatalf("provider API calls should be skipped without credentials: guards=%d detects=%d batches=%d",
			provider.repoGuardCalls, provider.detectCalls, len(provider.fetchBatches))
	}
}

func TestPoll_RepoETag304SkipsDetectPR(t *testing.T) {
	store := testStoreWithSession()
	provider := &fakeProvider{repoGuards: map[string]ports.SCMGuardResult{prKey(testRepo, 0): {ETag: "v1", NotModified: true}}, observations: map[string]ports.SCMObservation{}}
	obs := newTestObserver(store, provider, &fakeLifecycle{}, time.Unix(1, 0).UTC())
	obs.Cache.RepoPRListETag[prKey(testRepo, 0)] = "v1"
	if err := obs.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if provider.detectCalls != 0 {
		t.Fatalf("detectPR called on 304: %d", provider.detectCalls)
	}
}

func TestPoll_RepoETag200DetectsPRAndRefreshesSamePoll(t *testing.T) {
	store := testStoreWithSession()
	provider := &fakeProvider{
		repoGuards:   map[string]ports.SCMGuardResult{prKey(testRepo, 0): {ETag: "v2"}},
		detected:     map[string]ports.SCMPRObservation{"feat": {URL: "https://github.com/o/r/pull/1", Number: 1, SourceBranch: "feat", TargetBranch: "main", HeadSHA: "sha1"}},
		observations: map[string]ports.SCMObservation{prKey(testRepo, 1): testObs(1)},
	}
	lc := &fakeLifecycle{}
	obs := newTestObserver(store, provider, lc, time.Unix(1, 0).UTC())
	if err := obs.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if provider.detectCalls != 1 {
		t.Fatalf("detectPR calls = %d, want 1", provider.detectCalls)
	}
	if len(provider.fetchBatches) != 1 || len(provider.fetchBatches[0]) != 1 || provider.fetchBatches[0][0].Number != 1 {
		t.Fatalf("new PR not refreshed in same poll: %#v", provider.fetchBatches)
	}
	if len(store.writes) != 1 || len(lc.observed) != 1 {
		t.Fatalf("write/lifecycle missing: writes=%d lifecycle=%d", len(store.writes), len(lc.observed))
	}
}

func TestPoll_CIETagChangeRefreshesWhenRepoUnchanged(t *testing.T) {
	store := testStoreWithSession()
	store.prs["p-1"] = []domain.PullRequest{knownPR(1)}
	provider := &fakeProvider{
		repoGuards:   map[string]ports.SCMGuardResult{prKey(testRepo, 0): {ETag: "repo", NotModified: true}},
		checkGuards:  map[string]ports.SCMGuardResult{commitKey(testRepo, "sha1"): {ETag: "ci2"}},
		observations: map[string]ports.SCMObservation{prKey(testRepo, 1): testObs(1)},
	}
	obs := newTestObserver(store, provider, &fakeLifecycle{}, time.Unix(2, 0).UTC())
	obs.Cache.RepoPRListETag[prKey(testRepo, 0)] = "repo"
	obs.Cache.CommitChecksETag[commitKey(testRepo, "sha1")] = "ci1"
	if err := obs.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(provider.fetchBatches) != 1 {
		t.Fatalf("CI ETag 200 should trigger batch fetch, got %d", len(provider.fetchBatches))
	}
}

func TestPoll_GraphQLBatchChunksAt25(t *testing.T) {
	store := &fakeStore{projects: map[string]domain.ProjectRecord{"p": {ID: "p", RepoOriginURL: "https://github.com/o/r.git"}}, prs: map[domain.SessionID][]domain.PullRequest{}, checks: map[string][]domain.PullRequestCheck{}}
	provider := &fakeProvider{repoGuards: map[string]ports.SCMGuardResult{prKey(testRepo, 0): {ETag: "repo"}}, observations: map[string]ports.SCMObservation{}}
	for i := 1; i <= 26; i++ {
		id := domain.SessionID("p-" + fmt.Sprint(i))
		store.sessions = append(store.sessions, domain.SessionRecord{ID: id, ProjectID: "p", Metadata: domain.SessionMetadata{Branch: "b" + fmt.Sprint(i)}})
		pr := knownPR(i)
		pr.SessionID = id
		pr.MetadataHash = "" // force candidate
		store.prs[id] = []domain.PullRequest{pr}
		provider.observations[prKey(testRepo, i)] = testObs(i)
	}
	obs := newTestObserver(store, provider, &fakeLifecycle{}, time.Unix(1, 0).UTC())
	if err := obs.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(provider.fetchBatches) != 2 || len(provider.fetchBatches[0]) != 25 || len(provider.fetchBatches[1]) != 1 {
		t.Fatalf("batch sizes = %#v", provider.fetchBatches)
	}
}

func TestPoll_FailingCIFetchesLogTailOnlyWhenFingerprintChanged(t *testing.T) {
	failing := testObs(1)
	failing.CI.Summary = string(domain.CIFailing)
	failing.CI.Checks = []ports.SCMCheckObservation{{Name: "build", Status: string(domain.PRCheckFailed), Conclusion: "failure", ProviderID: "99"}}
	failing.CI.FailedChecks = failing.CI.Checks
	failing.CI.FailedFingerprint = "fp"

	store := testStoreWithSession()
	local := knownPR(1)
	local.CIHash = "old"
	store.prs["p-1"] = []domain.PullRequest{local}
	provider := &fakeProvider{repoGuards: map[string]ports.SCMGuardResult{prKey(testRepo, 0): {ETag: "repo"}}, checkGuards: map[string]ports.SCMGuardResult{commitKey(testRepo, "sha1"): {ETag: "ci2"}}, observations: map[string]ports.SCMObservation{prKey(testRepo, 1): failing}, logTails: map[string]string{"build": "tail"}}
	obs := newTestObserver(store, provider, &fakeLifecycle{}, time.Unix(1, 0).UTC())
	if err := obs.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if provider.logCalls != 1 {
		t.Fatalf("log calls = %d, want 1", provider.logCalls)
	}

	provider.logCalls = 0
	store.writes = nil
	withTail := failing
	withTail.CI.Checks[0].LogTail = "tail"
	withTail.CI.FailedChecks[0].LogTail = "tail"
	withTail.CI.FailureLogTail = "tail"
	local.CIHash = ciSemanticHash(withTail.CI)
	store.prs["p-1"] = []domain.PullRequest{local}
	store.checks[local.URL] = []domain.PullRequestCheck{{Name: "build", Status: domain.PRCheckFailed, LogTail: "tail"}}
	if err := obs.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if provider.logCalls != 0 {
		t.Fatalf("unchanged fingerprint fetched logs again: %d", provider.logCalls)
	}
	if len(store.writes) != 0 {
		t.Fatalf("unchanged failed fingerprint with stored log tail should not write, got %d writes", len(store.writes))
	}
}

func TestPoll_ReviewPollingRespectsInterval(t *testing.T) {
	store := testStoreWithSession()
	local := knownPR(1)
	local.Review = domain.ReviewChangesRequest
	local.ReviewHash = "old-review"
	store.prs["p-1"] = []domain.PullRequest{local}
	provider := &fakeProvider{repoGuards: map[string]ports.SCMGuardResult{prKey(testRepo, 0): {ETag: "repo", NotModified: true}}, observations: map[string]ports.SCMObservation{}, reviews: map[string]ports.SCMReviewObservation{prKey(testRepo, 1): {Decision: string(domain.ReviewChangesRequest), Threads: []ports.SCMReviewThreadObservation{{ID: "t1", Path: "f.go", Line: 1, Comments: []ports.SCMReviewCommentObservation{{ID: "c1", Body: "fix"}}}}}}}
	obs := newTestObserver(store, provider, &fakeLifecycle{}, time.Unix(120, 0).UTC())
	obs.Cache.RepoPRListETag[prKey(testRepo, 0)] = "repo"
	obs.Cache.LastReviewPollAt[prKey(testRepo, 1)] = time.Unix(90, 0).UTC()
	if err := obs.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if provider.reviewCalls != 0 {
		t.Fatalf("review fetched before interval: %d", provider.reviewCalls)
	}
	obs.Cache.LastReviewPollAt[prKey(testRepo, 1)] = time.Unix(0, 0).UTC()
	if err := obs.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if provider.reviewCalls != 1 {
		t.Fatalf("review not fetched after interval: %d", provider.reviewCalls)
	}
	if len(store.writes) != 1 || !store.writes[0].replaceReview {
		t.Fatalf("review refresh not persisted with replaceReview: %#v", store.writes)
	}
}

func TestPoll_UnchangedHashesDoNotWriteOrNotify(t *testing.T) {
	store := testStoreWithSession()
	obsValue := testObs(1)
	local := knownPR(1)
	local.MetadataHash = metadataSemanticHash(obsValue)
	local.CIHash = ciSemanticHash(obsValue.CI)
	local.ReviewHash = reviewSemanticHash(obsValue.Review)
	store.prs["p-1"] = []domain.PullRequest{local}
	provider := &fakeProvider{repoGuards: map[string]ports.SCMGuardResult{prKey(testRepo, 0): {ETag: "repo"}}, observations: map[string]ports.SCMObservation{prKey(testRepo, 1): obsValue}}
	lc := &fakeLifecycle{}
	obs := newTestObserver(store, provider, lc, time.Unix(1, 0).UTC())
	if err := obs.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(store.writes) != 0 || len(lc.observed) != 0 {
		t.Fatalf("unchanged hashes wrote/notified: writes=%d observed=%d", len(store.writes), len(lc.observed))
	}
}

func TestPoll_ReviewHashDrivesPersistenceAndLifecycle(t *testing.T) {
	store := testStoreWithSession()
	local := knownPR(1)
	local.ReviewHash = "old"
	local.Review = domain.ReviewChangesRequest
	store.prs["p-1"] = []domain.PullRequest{local}
	review := ports.SCMReviewObservation{Decision: string(domain.ReviewChangesRequest), Threads: []ports.SCMReviewThreadObservation{{ID: "t1", Path: "f.go", Line: 2, Comments: []ports.SCMReviewCommentObservation{{ID: "c1", Author: "ann", Body: "fix this"}}}}}
	provider := &fakeProvider{repoGuards: map[string]ports.SCMGuardResult{prKey(testRepo, 0): {ETag: "repo", NotModified: true}}, observations: map[string]ports.SCMObservation{}, reviews: map[string]ports.SCMReviewObservation{prKey(testRepo, 1): review}}
	lc := &fakeLifecycle{}
	obs := newTestObserver(store, provider, lc, time.Unix(200, 0).UTC())
	obs.Cache.RepoPRListETag[prKey(testRepo, 0)] = "repo"
	if err := obs.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(store.writes) != 1 || len(store.writes[0].comments) != 1 {
		t.Fatalf("review change not persisted: %#v", store.writes)
	}
	if len(lc.observed) != 1 || !lc.observed[0].Changed.Review {
		t.Fatalf("review change not notified: %#v", lc.observed)
	}
}
