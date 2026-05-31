package lifecycle

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

var ctx = context.Background()

// ---- fakes ----

// fakeStore is a mini SessionStore + PRWriter: it derives PRFacts and recent
// check statuses from what the engine writes, so PR-reaction tests exercise the
// write path and the read-back together.
type fakeStore struct {
	sessions map[domain.SessionID]domain.SessionRecord
	pr       map[domain.SessionID]ports.PRRow
	comments map[string][]ports.PRComment
	checks   []ports.PRCheckRow
	num      int
}

func newFakeStore() *fakeStore {
	return &fakeStore{
		sessions: map[domain.SessionID]domain.SessionRecord{},
		pr:       map[domain.SessionID]ports.PRRow{},
		comments: map[string][]ports.PRComment{},
	}
}

func (f *fakeStore) CreateSession(_ context.Context, rec domain.SessionRecord) (domain.SessionRecord, error) {
	f.num++
	rec.ID = domain.SessionID(fmt.Sprintf("%s-%d", rec.ProjectID, f.num))
	f.sessions[rec.ID] = rec
	return rec, nil
}
func (f *fakeStore) UpdateSession(_ context.Context, rec domain.SessionRecord) error {
	f.sessions[rec.ID] = rec
	return nil
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
func (f *fakeStore) PRFactsForSession(_ context.Context, id domain.SessionID) (domain.PRFacts, error) {
	r, ok := f.pr[id]
	if !ok {
		return domain.PRFacts{}, nil
	}
	facts := domain.PRFacts{
		URL: r.URL, Number: r.Number, Exists: true,
		Draft: r.Draft, Merged: r.Merged, Closed: r.Closed,
		CI: r.CI, Review: r.Review, Mergeability: r.Mergeability,
	}
	for _, c := range f.comments[r.URL] {
		if !c.Resolved {
			facts.ReviewComments = true
			break
		}
	}
	return facts, nil
}
func (f *fakeStore) WritePR(_ context.Context, pr ports.PRRow, checks []ports.PRCheckRow, comments []ports.PRComment) error {
	f.pr[domain.SessionID(pr.SessionID)] = pr
	f.checks = append(f.checks, checks...)
	f.comments[pr.URL] = comments
	return nil
}
func (f *fakeStore) RecentCheckStatuses(_ context.Context, url, name string, limit int) ([]string, error) {
	var out []string
	for i := len(f.checks) - 1; i >= 0 && len(out) < limit; i-- {
		if f.checks[i].PRURL == url && f.checks[i].Name == name {
			out = append(out, f.checks[i].Status)
		}
	}
	return out, nil
}

type fakeNotifier struct{ events []ports.Event }

func (f *fakeNotifier) Notify(_ context.Context, e ports.Event) error {
	f.events = append(f.events, e)
	return nil
}
func (f *fakeNotifier) last() string {
	if len(f.events) == 0 {
		return ""
	}
	return f.events[len(f.events)-1].Type
}

type fakeMessenger struct{ msgs []string }

func (f *fakeMessenger) Send(_ context.Context, _ domain.SessionID, m string) error {
	f.msgs = append(f.msgs, m)
	return nil
}

func newManager() (*Manager, *fakeStore, *fakeNotifier, *fakeMessenger) {
	st, n, msg := newFakeStore(), &fakeNotifier{}, &fakeMessenger{}
	return New(st, st, n, msg), st, n, msg
}

func working(id domain.SessionID) domain.SessionRecord {
	return domain.SessionRecord{
		ID: id, ProjectID: "mer",
		Lifecycle: domain.CanonicalSessionLifecycle{
			Version: domain.LifecycleVersion,
			Session: domain.SessionSubstate{State: domain.SessionWorking},
			IsAlive: true,
		},
	}
}

func openPR(o ports.PRObservation) ports.PRObservation {
	o.Fetched, o.URL, o.Number = true, "https://example/pr/1", 1
	return o
}

// ---- runtime observations ----

func TestRuntimeObservation_InferredDeath(t *testing.T) {
	m, st, n, _ := newManager()
	st.sessions["mer-1"] = working("mer-1")

	if err := m.ApplyRuntimeObservation(ctx, "mer-1", ports.RuntimeFacts{Runtime: ports.ProbeDead, Process: ports.ProbeDead}); err != nil {
		t.Fatal(err)
	}
	got := st.sessions["mer-1"].Lifecycle
	if got.Session.State != domain.SessionTerminated || got.TerminationReason != domain.TermRuntimeLost || got.IsAlive {
		t.Fatalf("want terminated/runtime_lost/dead, got %+v", got)
	}
	if n.last() != "reaction.agent-exited" {
		t.Fatalf("want agent-exited notify, got %q", n.last())
	}
}

func TestRuntimeObservation_FailedProbeQuarantines(t *testing.T) {
	m, st, _, _ := newManager()
	st.sessions["mer-1"] = working("mer-1")

	if err := m.ApplyRuntimeObservation(ctx, "mer-1", ports.RuntimeFacts{Runtime: ports.ProbeFailed, Process: ports.ProbeFailed}); err != nil {
		t.Fatal(err)
	}
	got := st.sessions["mer-1"].Lifecycle
	if got.Session.State != domain.SessionDetecting || !got.IsAlive || got.Detecting == nil {
		t.Fatalf("failed probe should quarantine alive, got %+v", got)
	}
}

func TestRuntimeObservation_RecoversDetecting(t *testing.T) {
	m, st, _, _ := newManager()
	rec := working("mer-1")
	rec.Lifecycle.Session.State = domain.SessionDetecting
	rec.Lifecycle.Detecting = &domain.DetectingState{Attempts: 1}
	st.sessions["mer-1"] = rec

	if err := m.ApplyRuntimeObservation(ctx, "mer-1", ports.RuntimeFacts{Runtime: ports.ProbeAlive, Process: ports.ProbeAlive}); err != nil {
		t.Fatal(err)
	}
	got := st.sessions["mer-1"].Lifecycle
	if got.Session.State != domain.SessionWorking || got.Detecting != nil {
		t.Fatalf("healthy probe should recover to working, got %+v", got)
	}
}

// ---- activity signals ----

func TestActivity_WaitingInputPagesHuman(t *testing.T) {
	m, st, n, _ := newManager()
	st.sessions["mer-1"] = working("mer-1")

	if err := m.ApplyActivitySignal(ctx, "mer-1", ports.ActivitySignal{Valid: true, State: domain.ActivityWaitingInput, Timestamp: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if st.sessions["mer-1"].Lifecycle.Session.State != domain.SessionNeedsInput {
		t.Fatalf("want needs_input, got %v", st.sessions["mer-1"].Lifecycle.Session.State)
	}
	if n.last() != "reaction.agent-needs-input" {
		t.Fatalf("want needs-input notify, got %q", n.last())
	}
}

func TestActivity_InvalidIsIgnored(t *testing.T) {
	m, st, _, _ := newManager()
	st.sessions["mer-1"] = working("mer-1")
	before := st.sessions["mer-1"]

	if err := m.ApplyActivitySignal(ctx, "mer-1", ports.ActivitySignal{Valid: false, State: domain.ActivityIdle}); err != nil {
		t.Fatal(err)
	}
	if st.sessions["mer-1"] != before {
		t.Fatal("invalid signal must not mutate the session")
	}
}

// ---- PR observations ----

func TestPR_CIFailingNudgesAgentWithLogs(t *testing.T) {
	m, st, _, msg := newManager()
	st.sessions["mer-1"] = working("mer-1")

	o := openPR(ports.PRObservation{CI: domain.CIFailing, Checks: []ports.PRCheckRow{{Name: "build", CommitHash: "c1", Status: "failed", LogTail: "boom"}}})
	if err := m.ApplyPRObservation(ctx, "mer-1", o); err != nil {
		t.Fatal(err)
	}
	if len(msg.msgs) != 1 || !strings.Contains(msg.msgs[0], "boom") {
		t.Fatalf("want one CI nudge with log tail, got %v", msg.msgs)
	}
}

func TestPR_CIBrakeEscalatesAfterThreeFails(t *testing.T) {
	m, st, n, msg := newManager()
	st.sessions["mer-1"] = working("mer-1")

	for _, commit := range []string{"c1", "c2", "c3"} {
		o := openPR(ports.PRObservation{CI: domain.CIFailing, Checks: []ports.PRCheckRow{{Name: "build", CommitHash: commit, Status: "failed", LogTail: "boom"}}})
		if err := m.ApplyPRObservation(ctx, "mer-1", o); err != nil {
			t.Fatal(err)
		}
	}
	if len(msg.msgs) != 2 {
		t.Fatalf("want 2 nudges then escalate, got %d nudges", len(msg.msgs))
	}
	if n.last() != "reaction.escalated" {
		t.Fatalf("3rd failure should escalate, got %q", n.last())
	}
}

func TestPR_ReviewCommentsInjectedRegardlessOfAuthor(t *testing.T) {
	m, st, _, msg := newManager()
	st.sessions["mer-1"] = working("mer-1")

	o := openPR(ports.PRObservation{
		Review:   domain.ReviewChangesRequest,
		Comments: []ports.PRComment{{ID: "1", Author: "greptileai", Body: "use a constant here"}},
	})
	if err := m.ApplyPRObservation(ctx, "mer-1", o); err != nil {
		t.Fatal(err)
	}
	if len(msg.msgs) != 1 || !strings.Contains(msg.msgs[0], "use a constant here") {
		t.Fatalf("review feedback should be injected verbatim, got %v", msg.msgs)
	}
}

func TestPR_ApprovedAndGreenNotifies(t *testing.T) {
	m, st, n, _ := newManager()
	st.sessions["mer-1"] = working("mer-1")

	o := openPR(ports.PRObservation{Review: domain.ReviewApproved, Mergeability: domain.MergeMergeable})
	if err := m.ApplyPRObservation(ctx, "mer-1", o); err != nil {
		t.Fatal(err)
	}
	if n.last() != "reaction.approved-and-green" {
		t.Fatalf("want approved-and-green, got %q", n.last())
	}
}

func TestPR_MergeTerminatesSession(t *testing.T) {
	m, st, n, _ := newManager()
	st.sessions["mer-1"] = working("mer-1")

	o := openPR(ports.PRObservation{Merged: true})
	if err := m.ApplyPRObservation(ctx, "mer-1", o); err != nil {
		t.Fatal(err)
	}
	got := st.sessions["mer-1"].Lifecycle
	if got.Session.State != domain.SessionTerminated || got.TerminationReason != domain.TermPRMerged {
		t.Fatalf("merge should terminate with pr_merged, got %+v", got)
	}
	if n.last() != "reaction.pr-merged" {
		t.Fatalf("want pr-merged notify, got %q", n.last())
	}
}

func TestPR_FailedFetchIsDropped(t *testing.T) {
	m, st, _, msg := newManager()
	st.sessions["mer-1"] = working("mer-1")

	if err := m.ApplyPRObservation(ctx, "mer-1", ports.PRObservation{Fetched: false, CI: domain.CIFailing}); err != nil {
		t.Fatal(err)
	}
	if len(msg.msgs) != 0 || len(st.pr) != 0 {
		t.Fatal("a failed fetch must write nothing and fire nothing")
	}
}

func TestSCMMergeabilityUnknownWithBlockerIsNotConflict(t *testing.T) {
	got := scmMergeability(ports.Mergeability{
		Unknown:     true,
		NoConflicts: false,
		Blockers:    []string{"merge status unknown (GitHub is computing)"},
	})
	if got != domain.MergeUnknown {
		t.Fatalf("unknown mergeability should stay unknown, got %q", got)
	}
}

// ---- explicit kill ----

func TestKill_TerminatesWithoutReacting(t *testing.T) {
	m, st, n, _ := newManager()
	st.sessions["mer-1"] = working("mer-1")

	if err := m.OnKillRequested(ctx, "mer-1", domain.TermManuallyKilled); err != nil {
		t.Fatal(err)
	}
	got := st.sessions["mer-1"].Lifecycle
	if got.Session.State != domain.SessionTerminated || got.TerminationReason != domain.TermManuallyKilled || got.IsAlive {
		t.Fatalf("want terminated/manually_killed/dead, got %+v", got)
	}
	if len(n.events) != 0 {
		t.Fatal("an explicit kill must not fire a reaction")
	}
}

// ---- duration escalation ----

func TestTickEscalations_DurationPagesHuman(t *testing.T) {
	m, st, n, msg := newManager()
	now := time.Now()
	m.clock = func() time.Time { return now }
	st.sessions["mer-1"] = working("mer-1")

	o := openPR(ports.PRObservation{Mergeability: domain.MergeConflicting})
	if err := m.ApplyPRObservation(ctx, "mer-1", o); err != nil {
		t.Fatal(err)
	}
	if len(msg.msgs) != 1 {
		t.Fatalf("merge-conflict should nudge once, got %d", len(msg.msgs))
	}
	if err := m.TickEscalations(ctx, now.Add(16*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if n.last() != "reaction.escalated" {
		t.Fatalf("unaddressed conflict should escalate after 15m, got %q", n.last())
	}
}

func TestRunningSessions_ExcludesTerminal(t *testing.T) {
	m, st, _, _ := newManager()
	st.sessions["mer-1"] = working("mer-1")
	dead := working("mer-2")
	dead.Lifecycle.Session.State = domain.SessionTerminated
	st.sessions["mer-2"] = dead

	got, err := m.RunningSessions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != "mer-1" {
		t.Fatalf("want only the live session, got %+v", got)
	}
}
