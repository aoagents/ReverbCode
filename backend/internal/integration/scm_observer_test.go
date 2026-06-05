// This file is the end-to-end regression guard for the SCM observer lane wired
// in PR #114 (issue #108). It wires a real sqlite.Store, a real lifecycle.Manager
// with a recording messenger spy, and a canned observe/scm.Provider into the
// real observe/scm.Observer, then drives Observer.Poll directly (never the
// ticker) to assert the full observation -> reducer -> store -> messenger path.
// Provider/store/lifecycle unit coverage already live in their own packages;
// this file's job is to catch wiring regressions only an integration view can
// see — e.g. a nil messenger, a wrong RepoOriginURL plumbing, or a dedup
// signature that does not persist across polls.
package integration

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/lifecycle"
	scmobserve "github.com/aoagents/agent-orchestrator/backend/internal/observe/scm"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
)

var scmTestRepo = ports.SCMRepo{
	Provider: "github",
	Host:     "github.com",
	Owner:    "octocat",
	Name:     "hello",
	Repo:     "octocat/hello",
}

const scmTestOriginURL = "https://github.com/octocat/hello.git"

// scmMessengerSpy is a minimal lifecycle.messenger that records every nudge so
// tests can assert exactly which lifecycle reactions fired and what they sent.
type scmMessengerSpy struct {
	mu   sync.Mutex
	sent []scmCapturedNudge
}

type scmCapturedNudge struct {
	session domain.SessionID
	body    string
}

func (s *scmMessengerSpy) Send(_ context.Context, id domain.SessionID, msg string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sent = append(s.sent, scmCapturedNudge{session: id, body: msg})
	return nil
}

func (s *scmMessengerSpy) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.sent)
}

func (s *scmMessengerSpy) snapshot() []scmCapturedNudge {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]scmCapturedNudge(nil), s.sent...)
}

// cannedSCMProvider satisfies observe/scm.Provider with hand-built observations
// keyed by branch (for DetectPRByBranch) and by PR number (for everything else,
// since every test case uses scmTestRepo). It is the integration-package analog
// of observer_test.go's fakeProvider: the SCM adapter has its own httptest-based
// coverage, so this test holds the provider constant and exercises every other
// layer end-to-end.
type cannedSCMProvider struct {
	mu sync.Mutex

	parsedRepo   ports.SCMRepo
	detected     map[string]ports.SCMPRObservation
	observations map[int]ports.SCMObservation
	reviews      map[int]ports.SCMReviewObservation
}

func newCannedSCMProvider() *cannedSCMProvider {
	return &cannedSCMProvider{
		parsedRepo:   scmTestRepo,
		detected:     map[string]ports.SCMPRObservation{},
		observations: map[int]ports.SCMObservation{},
		reviews:      map[int]ports.SCMReviewObservation{},
	}
}

func (p *cannedSCMProvider) ParseRepository(remote string) (ports.SCMRepo, bool) {
	if strings.TrimSpace(remote) == "" {
		return ports.SCMRepo{}, false
	}
	return p.parsedRepo, true
}

func (p *cannedSCMProvider) RepoPRListGuard(_ context.Context, _ ports.SCMRepo, _ string) (ports.SCMGuardResult, error) {
	return ports.SCMGuardResult{ETag: "repo-etag"}, nil
}

func (p *cannedSCMProvider) DetectPRByBranch(_ context.Context, _ ports.SCMRepo, branch string) (ports.SCMPRObservation, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	pr, ok := p.detected[branch]
	if !ok {
		return ports.SCMPRObservation{}, ports.ErrSCMNotFound
	}
	return pr, nil
}

func (p *cannedSCMProvider) CommitChecksGuard(_ context.Context, _ ports.SCMRepo, _, _ string) (ports.SCMGuardResult, error) {
	return ports.SCMGuardResult{ETag: "commit-etag"}, nil
}

func (p *cannedSCMProvider) FetchPullRequests(_ context.Context, refs []ports.SCMPRRef) ([]ports.SCMObservation, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]ports.SCMObservation, 0, len(refs))
	for _, ref := range refs {
		if obs, ok := p.observations[ref.Number]; ok {
			out = append(out, obs)
		}
	}
	return out, nil
}

func (p *cannedSCMProvider) FetchFailedCheckLogTail(_ context.Context, _ ports.SCMRepo, _ ports.SCMCheckObservation) (string, error) {
	// Observations in this test always carry their LogTail inline, so the
	// observer's failed-log enrichment short-circuits without calling here.
	// Returning the empty string keeps the contract honest if a future case
	// drops the inline tail.
	return "", nil
}

func (p *cannedSCMProvider) FetchReviewThreads(_ context.Context, ref ports.SCMPRRef) (ports.SCMReviewObservation, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.reviews[ref.Number], nil
}

// scmFixture bundles the live collaborators a single SCM observer scenario
// needs. Every test case constructs its own fixture against a fresh tmpdir DB
// so writes/lifecycle/messenger state never leak between cases.
type scmFixture struct {
	store    *sqlite.Store
	lcm      *lifecycle.Manager
	spy      *scmMessengerSpy
	provider *cannedSCMProvider
	observer *scmobserve.Observer
	session  domain.SessionRecord
	now      time.Time
}

func newSCMFixture(t *testing.T, branch string) *scmFixture {
	t.Helper()
	ctx := context.Background()

	store, err := sqlite.Open(t.TempDir())
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	now := time.Date(2026, 6, 5, 12, 0, 0, 0, time.UTC)
	if err := store.UpsertProject(ctx, domain.ProjectRecord{
		ID:            "octo",
		Path:          t.TempDir(),
		DisplayName:   "octo",
		RepoOriginURL: scmTestOriginURL,
		RegisteredAt:  now,
	}); err != nil {
		t.Fatalf("UpsertProject: %v", err)
	}
	sess, err := store.CreateSession(ctx, domain.SessionRecord{
		ProjectID: "octo",
		Kind:      domain.KindWorker,
		Metadata:  domain.SessionMetadata{Branch: branch, WorkspacePath: "/ws/octo"},
		CreatedAt: now,
		UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	spy := &scmMessengerSpy{}
	lcm := lifecycle.New(store, spy)
	provider := newCannedSCMProvider()
	observer := scmobserve.New(provider, store, lcm, scmobserve.Config{
		Tick:   time.Hour,
		Clock:  func() time.Time { return now },
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	return &scmFixture{
		store:    store,
		lcm:      lcm,
		spy:      spy,
		provider: provider,
		observer: observer,
		session:  sess,
		now:      now,
	}
}

func failingSCMObservation(prURL string, num int, headSHA, logTail string) ports.SCMObservation {
	failed := ports.SCMCheckObservation{
		Name:       "build",
		Status:     string(domain.PRCheckFailed),
		Conclusion: "failure",
		URL:        "https://github.com/octocat/hello/runs/9001",
		ProviderID: "9001",
		LogTail:    logTail,
	}
	return ports.SCMObservation{
		Fetched:  true,
		Provider: "github", Host: "github.com", Repo: "octocat/hello",
		PR: ports.SCMPRObservation{
			URL:          prURL,
			HTMLURL:      prURL,
			Number:       num,
			State:        string(domain.PRStateOpen),
			SourceBranch: "feat/x",
			TargetBranch: "main",
			HeadSHA:      headSHA,
			Title:        "Found a bug",
		},
		CI: ports.SCMCIObservation{
			Summary:           string(domain.CIFailing),
			HeadSHA:           headSHA,
			FailedFingerprint: "fp-build",
			Checks:            []ports.SCMCheckObservation{failed},
			FailedChecks:      []ports.SCMCheckObservation{failed},
			FailureLogTail:    logTail,
		},
		Review:       ports.SCMReviewObservation{Decision: string(domain.ReviewNone)},
		Mergeability: ports.SCMMergeabilityObservation{State: string(domain.MergeBlocked), Blockers: []string{"ci_failing"}},
	}
}

func mergedSCMObservation(prURL string, num int, headSHA string) ports.SCMObservation {
	return ports.SCMObservation{
		Fetched:  true,
		Provider: "github", Host: "github.com", Repo: "octocat/hello",
		PR: ports.SCMPRObservation{
			URL:          prURL,
			HTMLURL:      prURL,
			Number:       num,
			State:        string(domain.PRStateMerged),
			Merged:       true,
			SourceBranch: "feat/x",
			TargetBranch: "main",
			HeadSHA:      headSHA,
			Title:        "Ship it",
		},
		CI:           ports.SCMCIObservation{Summary: string(domain.CIPassing), HeadSHA: headSHA},
		Review:       ports.SCMReviewObservation{Decision: string(domain.ReviewApproved)},
		Mergeability: ports.SCMMergeabilityObservation{State: string(domain.MergeMergeable), Mergeable: true},
	}
}

// TestSCMObserverEndToEnd is the wiring regression guard for issue #109. It
// drives Observer.Poll against a real sqlite.Store + real lifecycle.Manager so
// the observation -> reducer -> store -> messenger pipeline the daemon runs in
// production stays connected end-to-end after PR #114.
func TestSCMObserverEndToEnd(t *testing.T) {
	t.Run("CI failing observation persists rows, nudges once, and is idempotent on re-poll", func(t *testing.T) {
		ctx := context.Background()
		f := newSCMFixture(t, "feat/x")
		const (
			prURL   = "https://github.com/octocat/hello/pull/42"
			headSHA = "deadbeef"
			logTail = "setup\nsetup\nFAILED: build broke\n"
		)
		f.provider.detected["feat/x"] = ports.SCMPRObservation{
			URL: prURL, Number: 42, SourceBranch: "feat/x", TargetBranch: "main", HeadSHA: headSHA,
		}
		f.provider.observations[42] = failingSCMObservation(prURL, 42, headSHA, logTail)

		if err := f.observer.Poll(ctx); err != nil {
			t.Fatalf("Poll: %v", err)
		}

		// PR row reflects the observation: provider-neutral identity columns,
		// failing CI roll-up, and persisted semantic hashes.
		pr, ok, err := f.store.GetPR(ctx, prURL)
		if err != nil || !ok {
			t.Fatalf("GetPR after Poll: ok=%v err=%v", ok, err)
		}
		if pr.SessionID != f.session.ID {
			t.Fatalf("PR.SessionID = %q, want %q", pr.SessionID, f.session.ID)
		}
		if pr.Number != 42 || pr.HeadSHA != headSHA {
			t.Fatalf("PR identity wrong: %+v", pr)
		}
		if pr.Provider != "github" || pr.Host != "github.com" || pr.Repo != "octocat/hello" {
			t.Fatalf("provider-neutral columns wrong: %+v", pr)
		}
		if pr.CI != domain.CIFailing {
			t.Fatalf("PR.CI = %q, want %q", pr.CI, domain.CIFailing)
		}
		if pr.MetadataHash == "" || pr.CIHash == "" {
			t.Fatalf("semantic hashes not persisted: metadata=%q ci=%q", pr.MetadataHash, pr.CIHash)
		}

		// pr_checks rows are a transactional mirror of the observation's CI.Checks.
		checks, err := f.store.ListChecks(ctx, prURL)
		if err != nil {
			t.Fatalf("ListChecks: %v", err)
		}
		if len(checks) != 1 {
			t.Fatalf("pr_checks rows = %d, want 1: %+v", len(checks), checks)
		}
		got := checks[0]
		if got.Name != "build" || got.Status != domain.PRCheckFailed || got.CommitHash != headSHA || got.LogTail != logTail {
			t.Fatalf("pr_checks row mismatch: %+v", got)
		}

		// Exactly one nudge reached the messenger, containing the log tail the
		// agent needs to fix CI.
		msgs := f.spy.snapshot()
		if len(msgs) != 1 {
			t.Fatalf("messenger captured %d nudges, want 1: %+v", len(msgs), msgs)
		}
		nudge := msgs[0]
		if nudge.session != f.session.ID {
			t.Fatalf("nudge addressed to session %q, want %q", nudge.session, f.session.ID)
		}
		if !strings.Contains(nudge.body, "CI is failing") {
			t.Fatalf("nudge body missing CI-failure cue: %q", nudge.body)
		}
		if !strings.Contains(nudge.body, logTail) {
			t.Fatalf("nudge body missing log tail %q: %q", logTail, nudge.body)
		}

		// Persisted dedup signature proves the lifecycle wrote its
		// nudge-acknowledgement state through, so a daemon restart would not
		// re-fire the same nudge against the same observation.
		sigBeforeSecondPoll, err := f.store.GetPRLastNudgeSignature(ctx, prURL)
		if err != nil {
			t.Fatalf("GetPRLastNudgeSignature: %v", err)
		}
		if sigBeforeSecondPoll == "" {
			t.Fatalf("last_nudge_signature not persisted after first nudge")
		}

		// A second identical Poll must produce zero additional nudges. This
		// exercises the hash-match short-circuit in prepareForPersistence —
		// the production fallback the observer relies on when the upstream
		// ETag guard misses. The ETag-driven 304 short-circuit on the same
		// SHA is covered by the unit tests in observe/scm/observer_test.go
		// (Poll_RepoETag304SkipsDetectPR, Poll_CIETagChangeRefreshesWhenRepoUnchanged).
		if err := f.observer.Poll(ctx); err != nil {
			t.Fatalf("second Poll: %v", err)
		}
		if got := f.spy.count(); got != 1 {
			t.Fatalf("nudges after idempotent re-poll = %d, want 1", got)
		}
		sigAfterSecondPoll, err := f.store.GetPRLastNudgeSignature(ctx, prURL)
		if err != nil {
			t.Fatalf("GetPRLastNudgeSignature after re-poll: %v", err)
		}
		if sigAfterSecondPoll != sigBeforeSecondPoll {
			t.Fatalf("idempotent re-poll mutated last_nudge_signature: before=%q after=%q", sigBeforeSecondPoll, sigAfterSecondPoll)
		}
	})

	t.Run("Merged observation terminates the session and sends no nudge", func(t *testing.T) {
		ctx := context.Background()
		f := newSCMFixture(t, "feat/x")
		const (
			prURL   = "https://github.com/octocat/hello/pull/77"
			headSHA = "cafef00d"
		)
		f.provider.detected["feat/x"] = ports.SCMPRObservation{
			URL: prURL, Number: 77, SourceBranch: "feat/x", TargetBranch: "main", HeadSHA: headSHA, Merged: true,
		}
		f.provider.observations[77] = mergedSCMObservation(prURL, 77, headSHA)

		if err := f.observer.Poll(ctx); err != nil {
			t.Fatalf("Poll: %v", err)
		}

		rec, ok, err := f.store.GetSession(ctx, f.session.ID)
		if err != nil || !ok {
			t.Fatalf("GetSession: ok=%v err=%v", ok, err)
		}
		if !rec.IsTerminated {
			t.Fatalf("merged observation should MarkTerminated the session: %+v", rec)
		}
		if got := f.spy.count(); got != 0 {
			t.Fatalf("merged observation must not nudge, got %d msgs: %+v", got, f.spy.snapshot())
		}
	})

	t.Run("Branch with no open PR writes nothing and sends no nudge", func(t *testing.T) {
		ctx := context.Background()
		f := newSCMFixture(t, "feat/quiet")
		// No entry in provider.detected — DetectPRByBranch returns ErrSCMNotFound,
		// the production "no PR yet" signal.

		if err := f.observer.Poll(ctx); err != nil {
			t.Fatalf("Poll: %v", err)
		}

		prs, err := f.store.ListPRsBySession(ctx, f.session.ID)
		if err != nil {
			t.Fatalf("ListPRsBySession: %v", err)
		}
		if len(prs) != 0 {
			t.Fatalf("no PR should be persisted for a quiet branch: %+v", prs)
		}
		if got := f.spy.count(); got != 0 {
			t.Fatalf("quiet branch must not nudge, got %d msgs: %+v", got, f.spy.snapshot())
		}
	})
}
