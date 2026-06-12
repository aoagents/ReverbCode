package session

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	sessionmanager "github.com/aoagents/agent-orchestrator/backend/internal/session_manager"
)

type fakeStore struct {
	sessions map[domain.SessionID]domain.SessionRecord
	pr       map[domain.SessionID]domain.PRFacts
	projects map[string]domain.ProjectRecord
	num      int
}

func newFakeStore() *fakeStore {
	return &fakeStore{sessions: map[domain.SessionID]domain.SessionRecord{}, pr: map[domain.SessionID]domain.PRFacts{}, projects: map[string]domain.ProjectRecord{}}
}

func (f *fakeStore) CreateSession(_ context.Context, rec domain.SessionRecord) (domain.SessionRecord, error) {
	f.num++
	rec.ID = domain.SessionID(fmt.Sprintf("%s-%d", rec.ProjectID, f.num))
	f.sessions[rec.ID] = rec
	return rec, nil
}

func (f *fakeStore) GetSession(_ context.Context, id domain.SessionID) (domain.SessionRecord, bool, error) {
	r, ok := f.sessions[id]
	return r, ok, nil
}

func (f *fakeStore) ListSessions(_ context.Context, p domain.ProjectID) ([]domain.SessionRecord, error) {
	var out []domain.SessionRecord
	for _, r := range f.sessions {
		if r.ProjectID == p {
			out = append(out, r)
		}
	}
	return out, nil
}

func (f *fakeStore) ListAllSessions(_ context.Context) ([]domain.SessionRecord, error) {
	out := make([]domain.SessionRecord, 0, len(f.sessions))
	for _, r := range f.sessions {
		out = append(out, r)
	}
	return out, nil
}

func (f *fakeStore) RenameSession(_ context.Context, id domain.SessionID, displayName string, updatedAt time.Time) (bool, error) {
	r, ok := f.sessions[id]
	if !ok {
		return false, nil
	}
	r.DisplayName = displayName
	r.UpdatedAt = updatedAt
	f.sessions[id] = r
	return true, nil
}

func (f *fakeStore) ArchiveSession(_ context.Context, id domain.SessionID, at time.Time) (bool, error) {
	r, ok := f.sessions[id]
	if !ok || !r.IsTerminated || !r.ArchivedAt.IsZero() {
		return false, nil
	}
	r.ArchivedAt = at
	r.UpdatedAt = at
	f.sessions[id] = r
	return true, nil
}

func (f *fakeStore) UnarchiveSession(_ context.Context, id domain.SessionID, at time.Time) (bool, error) {
	r, ok := f.sessions[id]
	if !ok || r.ArchivedAt.IsZero() {
		return false, nil
	}
	r.ArchivedAt = time.Time{}
	r.UpdatedAt = at
	f.sessions[id] = r
	return true, nil
}

func (f *fakeStore) GetDisplayPRFactsForSession(_ context.Context, id domain.SessionID) (domain.PRFacts, bool, error) {
	pr, ok := f.pr[id]
	return pr, ok, nil
}

func (f *fakeStore) ListPRsBySession(_ context.Context, id domain.SessionID) ([]domain.PullRequest, error) {
	pr, ok := f.pr[id]
	if !ok {
		return nil, nil
	}
	return []domain.PullRequest{{URL: pr.URL, SessionID: id, Number: pr.Number, Draft: pr.Draft, Merged: pr.Merged, Closed: pr.Closed, CI: pr.CI, Review: pr.Review, Mergeability: pr.Mergeability, UpdatedAt: pr.UpdatedAt}}, nil
}

func (f *fakeStore) ListPRComments(context.Context, string) ([]domain.PullRequestComment, error) {
	return nil, nil
}

func (f *fakeStore) GetProject(_ context.Context, id string) (domain.ProjectRecord, bool, error) {
	p, ok := f.projects[id]
	return p, ok, nil
}

func TestSessionListDerivesStatusFromPRFacts(t *testing.T) {
	st := newFakeStore()
	st.sessions["mer-1"] = domain.SessionRecord{ID: "mer-1", ProjectID: "mer", Activity: domain.Activity{State: domain.ActivityActive}}
	st.pr["mer-1"] = domain.PRFacts{URL: "pr1", CI: domain.CIFailing}

	list, err := (&Service{store: st}).List(context.Background(), ListFilter{ProjectID: "mer"})
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].Status != domain.StatusCIFailed {
		t.Fatalf("got %+v", list)
	}
}

func TestSessionRenameUpdatesDisplayName(t *testing.T) {
	st := newFakeStore()
	st.sessions["mer-1"] = domain.SessionRecord{ID: "mer-1", ProjectID: "mer"}

	err := (&Service{store: st}).Rename(context.Background(), "mer-1", "  Fix issue #90  ")
	if err != nil {
		t.Fatal(err)
	}
	if got := st.sessions["mer-1"].DisplayName; got != "Fix issue #90" {
		t.Fatalf("display name = %q, want trimmed rename", got)
	}
}

func TestSessionRenameMissingSessionReturnsNotFound(t *testing.T) {
	st := newFakeStore()

	err := (&Service{store: st}).Rename(context.Background(), "mer-404", "Missing")
	var e *apierr.Error
	if !errors.As(err, &e) || e.Kind != apierr.KindNotFound || e.Code != "SESSION_NOT_FOUND" {
		t.Fatalf("err = %v, want apierr NotFound SESSION_NOT_FOUND", err)
	}
}

func TestArchiveRequiresTerminatedSession(t *testing.T) {
	st := newFakeStore()
	st.sessions["mer-1"] = domain.SessionRecord{ID: "mer-1", ProjectID: "mer"}

	err := (&Service{store: st}).Archive(context.Background(), "mer-1")
	var e *apierr.Error
	if !errors.As(err, &e) || e.Kind != apierr.KindConflict || e.Code != "SESSION_NOT_TERMINATED" {
		t.Fatalf("err = %v, want apierr Conflict SESSION_NOT_TERMINATED", err)
	}
	if !st.sessions["mer-1"].ArchivedAt.IsZero() {
		t.Fatalf("running session must not be archived: %+v", st.sessions["mer-1"])
	}
}

func TestArchiveMissingSessionReturnsNotFound(t *testing.T) {
	err := (&Service{store: newFakeStore()}).Archive(context.Background(), "mer-404")
	var e *apierr.Error
	if !errors.As(err, &e) || e.Kind != apierr.KindNotFound || e.Code != "SESSION_NOT_FOUND" {
		t.Fatalf("err = %v, want apierr NotFound SESSION_NOT_FOUND", err)
	}
}

func TestArchiveHidesSessionFromFilteredListAndUnarchiveRestoresIt(t *testing.T) {
	st := newFakeStore()
	st.sessions["mer-1"] = domain.SessionRecord{ID: "mer-1", ProjectID: "mer", IsTerminated: true}
	st.sessions["mer-2"] = domain.SessionRecord{ID: "mer-2", ProjectID: "mer"}
	svc := &Service{store: st}

	if err := svc.Archive(context.Background(), "mer-1"); err != nil {
		t.Fatalf("Archive: %v", err)
	}
	if st.sessions["mer-1"].ArchivedAt.IsZero() {
		t.Fatalf("archived_at not set: %+v", st.sessions["mer-1"])
	}
	// Re-archiving is a no-op, not an error.
	if err := svc.Archive(context.Background(), "mer-1"); err != nil {
		t.Fatalf("repeat Archive: %v", err)
	}

	archived := true
	notArchived := false
	cases := []struct {
		filter ListFilter
		want   map[domain.SessionID]bool
	}{
		{ListFilter{ProjectID: "mer"}, map[domain.SessionID]bool{"mer-1": true, "mer-2": true}},
		{ListFilter{ProjectID: "mer", Archived: &notArchived}, map[domain.SessionID]bool{"mer-2": true}},
		{ListFilter{ProjectID: "mer", Archived: &archived}, map[domain.SessionID]bool{"mer-1": true}},
	}
	for _, tc := range cases {
		list, err := svc.List(context.Background(), tc.filter)
		if err != nil {
			t.Fatalf("List(%+v): %v", tc.filter, err)
		}
		got := map[domain.SessionID]bool{}
		for _, s := range list {
			got[s.ID] = true
			if s.ID == "mer-1" && !s.IsArchived {
				t.Fatalf("mer-1 read model must surface isArchived: %+v", s)
			}
		}
		if len(got) != len(tc.want) {
			t.Fatalf("List(%+v) = %v, want %v", tc.filter, got, tc.want)
		}
		for id := range tc.want {
			if !got[id] {
				t.Fatalf("List(%+v) = %v, want %v", tc.filter, got, tc.want)
			}
		}
	}

	if err := svc.Unarchive(context.Background(), "mer-1"); err != nil {
		t.Fatalf("Unarchive: %v", err)
	}
	if !st.sessions["mer-1"].ArchivedAt.IsZero() {
		t.Fatalf("archived_at not cleared: %+v", st.sessions["mer-1"])
	}
}

// archivedRestoreCommander returns a canned record from Restore so the test
// can hand the service an archived-but-just-restored session.
type archivedRestoreCommander struct {
	fakeCommander
	rec domain.SessionRecord
}

func (c *archivedRestoreCommander) Restore(context.Context, domain.SessionID) (domain.SessionRecord, error) {
	return c.rec, nil
}

func TestRestoreClearsArchived(t *testing.T) {
	archivedAt := time.Date(2026, 6, 10, 12, 0, 0, 0, time.UTC)
	st := newFakeStore()
	st.sessions["mer-1"] = domain.SessionRecord{ID: "mer-1", ProjectID: "mer", IsTerminated: true, ArchivedAt: archivedAt}
	svc := &Service{
		manager: &archivedRestoreCommander{rec: domain.SessionRecord{ID: "mer-1", ProjectID: "mer", ArchivedAt: archivedAt}},
		store:   st,
	}

	sess, err := svc.Restore(context.Background(), "mer-1")
	if err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if sess.IsArchived {
		t.Fatalf("restored session must not read as archived: %+v", sess)
	}
	if !st.sessions["mer-1"].ArchivedAt.IsZero() {
		t.Fatalf("restore must clear archived_at: %+v", st.sessions["mer-1"])
	}
}

// fakeCommander records Kill/Spawn calls so a test can assert the
// clean-orchestrator ordering without wiring a real session engine.
type fakeCommander struct {
	killed       []domain.SessionID
	spawned      bool
	killsAtSpawn int
}

func (f *fakeCommander) Spawn(_ context.Context, cfg ports.SpawnConfig) (domain.SessionRecord, error) {
	f.spawned = true
	f.killsAtSpawn = len(f.killed)
	return domain.SessionRecord{ID: "mer-9", ProjectID: cfg.ProjectID, Kind: cfg.Kind}, nil
}
func (f *fakeCommander) Restore(context.Context, domain.SessionID) (domain.SessionRecord, error) {
	return domain.SessionRecord{}, nil
}
func (f *fakeCommander) Kill(_ context.Context, id domain.SessionID) (bool, error) {
	f.killed = append(f.killed, id)
	return true, nil
}
func (f *fakeCommander) Send(context.Context, domain.SessionID, string) error { return nil }
func (f *fakeCommander) Cleanup(context.Context, domain.ProjectID) (sessionmanager.CleanupResult, error) {
	return sessionmanager.CleanupResult{
		Cleaned: []domain.SessionID{"mer-1"},
		Skipped: []sessionmanager.CleanupSkip{{SessionID: "mer-2", Reason: "workspace has uncommitted changes"}},
	}, nil
}
func (f *fakeCommander) RollbackSpawn(context.Context, domain.SessionID) (bool, bool, error) {
	return false, false, nil
}

// TestCleanupMapsManagerResult: the service forwards both reclaimed and
// skipped sessions, with non-nil slices so the wire shape stays stable.
func TestCleanupMapsManagerResult(t *testing.T) {
	svc := &Service{manager: &fakeCommander{}}
	out, err := svc.Cleanup(context.Background(), "mer")
	if err != nil {
		t.Fatalf("Cleanup: %v", err)
	}
	if len(out.Cleaned) != 1 || out.Cleaned[0] != "mer-1" {
		t.Fatalf("cleaned = %#v", out.Cleaned)
	}
	if len(out.Skipped) != 1 || out.Skipped[0].SessionID != "mer-2" || out.Skipped[0].Reason != "workspace has uncommitted changes" {
		t.Fatalf("skipped = %#v", out.Skipped)
	}
}

func TestSpawnOrchestratorCleanKillsActiveOrchestratorsBeforeSpawn(t *testing.T) {
	st := newFakeStore()
	st.projects["mer"] = domain.ProjectRecord{ID: "mer"}
	// Two active orchestrators plus an unrelated worker and a terminated
	// orchestrator that must be left alone.
	st.sessions["mer-1"] = domain.SessionRecord{ID: "mer-1", ProjectID: "mer", Kind: domain.KindOrchestrator}
	st.sessions["mer-2"] = domain.SessionRecord{ID: "mer-2", ProjectID: "mer", Kind: domain.KindOrchestrator}
	st.sessions["mer-3"] = domain.SessionRecord{ID: "mer-3", ProjectID: "mer", Kind: domain.KindWorker}
	st.sessions["mer-4"] = domain.SessionRecord{ID: "mer-4", ProjectID: "mer", Kind: domain.KindOrchestrator, IsTerminated: true}

	fc := &fakeCommander{}
	svc := &Service{manager: fc, store: st}

	if _, err := svc.SpawnOrchestrator(context.Background(), "mer", true); err != nil {
		t.Fatalf("SpawnOrchestrator: %v", err)
	}

	if len(fc.killed) != 2 {
		t.Fatalf("killed = %v, want the two active orchestrators", fc.killed)
	}
	if !fc.spawned || fc.killsAtSpawn != 2 {
		t.Fatalf("spawn must run after both kills: spawned=%v killsAtSpawn=%d", fc.spawned, fc.killsAtSpawn)
	}
}

// TestSpawnUnknownProjectReturns404 covers Bug 1: an HTTP spawn for an
// unregistered projectId must surface PROJECT_NOT_FOUND (apierr.NotFound)
// BEFORE any session row is created, so no orphan terminated row is left
// behind under `--include-terminated`.
func TestSpawnUnknownProjectReturns404(t *testing.T) {
	st := newFakeStore()
	fc := &fakeCommander{}
	svc := &Service{manager: fc, store: st}

	_, err := svc.Spawn(context.Background(), ports.SpawnConfig{ProjectID: "ghost", Kind: domain.KindWorker})
	var e *apierr.Error
	if !errors.As(err, &e) || e.Kind != apierr.KindNotFound || e.Code != "PROJECT_NOT_FOUND" {
		t.Fatalf("err = %v, want apierr.NotFound PROJECT_NOT_FOUND", err)
	}
	if fc.spawned {
		t.Fatal("manager.Spawn must NOT be invoked for an unknown project")
	}
}

// TestSpawnOrchestratorUnknownProjectReturns404 is the orchestrator-side guard
// for Bug 1: same pre-validation, same typed envelope.
func TestSpawnOrchestratorUnknownProjectReturns404(t *testing.T) {
	st := newFakeStore()
	fc := &fakeCommander{}
	svc := &Service{manager: fc, store: st}

	_, err := svc.SpawnOrchestrator(context.Background(), "ghost", false)
	var e *apierr.Error
	if !errors.As(err, &e) || e.Kind != apierr.KindNotFound || e.Code != "PROJECT_NOT_FOUND" {
		t.Fatalf("err = %v, want apierr.NotFound PROJECT_NOT_FOUND", err)
	}
	if fc.spawned {
		t.Fatal("manager.Spawn must NOT be invoked for an unknown project")
	}
}

// TestToAPIErrorMapsWorkspaceBranchSentinels covers Bug 3: the workspace
// adapter's typed branch errors map to typed envelope errors instead of
// collapsing to a 500.
func TestToAPIErrorMapsWorkspaceBranchSentinels(t *testing.T) {
	cases := []struct {
		name     string
		err      error
		wantKind apierr.Kind
		wantCode string
	}{
		{"checked out elsewhere", fmt.Errorf("spawn mer-1: workspace: %w: \"x\" is checked out at \"/tmp\"", ports.ErrWorkspaceBranchCheckedOutElsewhere), apierr.KindConflict, "BRANCH_CHECKED_OUT_ELSEWHERE"},
		{"not fetched", fmt.Errorf("spawn mer-1: workspace: %w: \"x\" has no local head", ports.ErrWorkspaceBranchNotFetched), apierr.KindInvalid, "BRANCH_NOT_FETCHED"},
		{"agent binary not found", fmt.Errorf("spawn mer-1: %w", ports.ErrAgentBinaryNotFound), apierr.KindInvalid, "AGENT_BINARY_NOT_FOUND"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mapped := toAPIError(tc.err)
			var e *apierr.Error
			if !errors.As(mapped, &e) || e.Kind != tc.wantKind || e.Code != tc.wantCode {
				t.Fatalf("mapped = %v, want %s %s", mapped, tc.wantCode, e)
			}
		})
	}
}

func TestSpawnOrchestratorNoCleanSkipsKills(t *testing.T) {
	st := newFakeStore()
	st.projects["mer"] = domain.ProjectRecord{ID: "mer"}
	st.sessions["mer-1"] = domain.SessionRecord{ID: "mer-1", ProjectID: "mer", Kind: domain.KindOrchestrator}

	fc := &fakeCommander{}
	svc := &Service{manager: fc, store: st}

	if _, err := svc.SpawnOrchestrator(context.Background(), "mer", false); err != nil {
		t.Fatalf("SpawnOrchestrator: %v", err)
	}
	if len(fc.killed) != 0 || !fc.spawned {
		t.Fatalf("clean=false must spawn without kills: killed=%v spawned=%v", fc.killed, fc.spawned)
	}
}

type fakePRClaimer struct {
	out errorFreeClaimOutcome
	err error
}

type errorFreeClaimOutcome struct {
	ports.ClaimOutcome
}

func (f fakePRClaimer) ClaimPR(context.Context, domain.PullRequest, []domain.PullRequestCheck, []domain.PullRequestReviewThread, []domain.PullRequestComment, ports.ReviewWriteMode, bool) (ports.ClaimOutcome, error) {
	return f.out.ClaimOutcome, f.err
}

type fakeSCM struct {
	obs       ports.SCMObservation
	review    ports.SCMReviewObservation
	fetchErr  error
	reviewErr error
}

func (f fakeSCM) ParseRepository(remote string) (ports.SCMRepo, bool) {
	owner, repo, err := githubRepoFromURL(remote)
	if err != nil {
		return ports.SCMRepo{}, false
	}
	return ports.SCMRepo{Provider: "github", Host: "github.com", Owner: owner, Name: repo, Repo: owner + "/" + repo}, true
}

func (f fakeSCM) FetchPullRequests(context.Context, []ports.SCMPRRef) ([]ports.SCMObservation, error) {
	if f.fetchErr != nil {
		return nil, f.fetchErr
	}
	if !f.obs.Fetched && f.obs.PR.URL == "" && f.obs.PR.Number == 0 {
		return nil, nil
	}
	return []ports.SCMObservation{f.obs}, nil
}

func (f fakeSCM) FetchReviewThreads(context.Context, ports.SCMPRRef) (ports.SCMReviewObservation, error) {
	return f.review, f.reviewErr
}

func TestClaimPRMapsObserverAndStoreErrors(t *testing.T) {
	st := newFakeStore()
	now := time.Date(2026, 6, 4, 12, 0, 0, 0, time.UTC)
	st.sessions["mer-1"] = domain.SessionRecord{ID: "mer-1", ProjectID: "mer", Kind: domain.KindWorker, Metadata: domain.SessionMetadata{WorkspacePath: "/ws"}}
	st.projects["mer"] = domain.ProjectRecord{ID: "mer", RepoOriginURL: "https://github.com/acme/repo"}

	cases := []struct {
		name string
		svc  *Service
		want error
	}{
		{"missing scm", NewWithDeps(Deps{Store: st}), ErrSCMUnavailable},
		{"not found", NewWithDeps(Deps{Store: st, PRClaimer: fakePRClaimer{}, SCM: fakeSCM{fetchErr: ports.ErrSCMNotFound}}), ErrPRNotFound},
		{"closed", NewWithDeps(Deps{Store: st, PRClaimer: fakePRClaimer{}, SCM: fakeSCM{obs: ports.SCMObservation{Fetched: true, Provider: "github", Host: "github.com", Repo: "acme/repo", PR: ports.SCMPRObservation{URL: "https://github.com/acme/repo/pull/7", Number: 7, Closed: true}}}}), ErrPRNotOpen},
		{"active owner", NewWithDeps(Deps{Store: st, PRClaimer: fakePRClaimer{err: ports.PRClaimedByActiveSessionError{Owner: "mer-2"}}, SCM: fakeSCM{obs: ports.SCMObservation{Fetched: true, Provider: "github", Host: "github.com", Repo: "acme/repo", PR: ports.SCMPRObservation{URL: "https://github.com/acme/repo/pull/7", Number: 7}}}}), ports.ErrPRClaimedByActiveSession},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := tc.svc.ClaimPR(context.Background(), "mer-1", "7", ClaimPROptions{AllowTakeover: false})
			if !errors.Is(err, tc.want) {
				t.Fatalf("err=%v, want %v", err, tc.want)
			}
		})
	}

	st.pr["mer-1"] = domain.PRFacts{URL: "https://github.com/acme/repo/pull/7", Number: 7, CI: domain.CIPassing, UpdatedAt: now}
	svc := NewWithDeps(Deps{Store: st, PRClaimer: fakePRClaimer{out: errorFreeClaimOutcome{ports.ClaimOutcome{PreviousOwner: "mer-2"}}}, SCM: fakeSCM{obs: ports.SCMObservation{Fetched: true, Provider: "github", Host: "github.com", Repo: "acme/repo", PR: ports.SCMPRObservation{URL: "https://github.com/acme/repo/pull/7", Number: 7}}}})
	res, err := svc.ClaimPR(context.Background(), "mer-1", "7", ClaimPROptions{AllowTakeover: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.TakenOverFrom) != 1 || res.TakenOverFrom[0] != "mer-2" || len(res.PRs) != 1 || res.PRs[0].URL == "" {
		t.Fatalf("claim result = %+v", res)
	}
}

func TestListPRsOrdersActiveBeforeClosedThenUpdatedDesc(t *testing.T) {
	st := newFakeStore()
	st.sessions["mer-1"] = domain.SessionRecord{ID: "mer-1", ProjectID: "mer", Kind: domain.KindWorker}
	now := time.Date(2026, 6, 4, 12, 0, 0, 0, time.UTC)
	st.pr = map[domain.SessionID]domain.PRFacts{}
	stList := &multiPRFakeStore{fakeStore: st, prs: []domain.PullRequest{
		{URL: "closed-new", SessionID: "mer-1", Number: 1, Closed: true, UpdatedAt: now.Add(2 * time.Hour)},
		{URL: "open-old", SessionID: "mer-1", Number: 2, UpdatedAt: now},
		{URL: "open-new", SessionID: "mer-1", Number: 3, UpdatedAt: now.Add(time.Hour)},
	}}
	got, err := (&Service{store: stList}).ListPRs(context.Background(), "mer-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || got[0].URL != "open-new" || got[1].URL != "open-old" || got[2].URL != "closed-new" {
		t.Fatalf("order = %+v", got)
	}
}

type multiPRFakeStore struct {
	*fakeStore
	prs []domain.PullRequest
}

func (f *multiPRFakeStore) ListPRsBySession(context.Context, domain.SessionID) ([]domain.PullRequest, error) {
	return f.prs, nil
}
