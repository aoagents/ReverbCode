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

type fakeStore struct {
	sessions map[domain.SessionID]domain.SessionRecord
	pr       map[domain.SessionID]domain.PRRow
	comments map[string][]domain.PRComment
	checks   []domain.PRCheckRow
	num      int
}

func newFakeStore() *fakeStore {
	return &fakeStore{sessions: map[domain.SessionID]domain.SessionRecord{}, pr: map[domain.SessionID]domain.PRRow{}, comments: map[string][]domain.PRComment{}}
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
	facts := domain.PRFacts{URL: r.URL, Number: r.Number, Exists: true, Draft: r.Draft, Merged: r.Merged, Closed: r.Closed, CI: r.CI, Review: r.Review, Mergeability: r.Mergeability}
	for _, c := range f.comments[r.URL] {
		if !c.Resolved {
			facts.ReviewComments = true
			break
		}
	}
	return facts, nil
}
func (f *fakeStore) WritePR(_ context.Context, pr domain.PRRow, checks []domain.PRCheckRow, comments []domain.PRComment) error {
	f.pr[pr.SessionID] = pr
	f.checks = append(f.checks, checks...)
	f.comments[pr.URL] = comments
	return nil
}
func (f *fakeStore) RecentCheckStatuses(_ context.Context, url, name string, limit int) ([]domain.PRCheckStatus, error) {
	var out []domain.PRCheckStatus
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
	return domain.SessionRecord{ID: id, ProjectID: "mer", Activity: domain.ActivitySubstate{State: domain.ActivityActive, LastActivityAt: time.Now(), Source: domain.SourceNative}}
}

func openPR(o ports.PRObservation) ports.PRObservation {
	o.Fetched, o.URL, o.Number = true, "https://example/pr/1", 1
	return o
}

func TestRuntimeObservation_InferredDeathSetsTerminated(t *testing.T) {
	m, st, _, _ := newManager()
	rec := working("mer-1")
	rec.Activity.LastActivityAt = time.Now().Add(-2 * time.Minute)
	st.sessions["mer-1"] = rec
	if err := m.ApplyRuntimeObservation(ctx, "mer-1", ports.RuntimeFacts{Runtime: ports.ProbeDead, Process: ports.ProbeDead}); err != nil {
		t.Fatal(err)
	}
	got := st.sessions["mer-1"]
	if !got.IsTerminated || got.Activity.State != domain.ActivityExited {
		t.Fatalf("want terminated/exited, got %+v", got)
	}
}

func TestRuntimeObservation_FailedProbeDoesNotMutate(t *testing.T) {
	m, st, _, _ := newManager()
	st.sessions["mer-1"] = working("mer-1")
	before := st.sessions["mer-1"]
	if err := m.ApplyRuntimeObservation(ctx, "mer-1", ports.RuntimeFacts{Runtime: ports.ProbeFailed, Process: ports.ProbeFailed}); err != nil {
		t.Fatal(err)
	}
	if st.sessions["mer-1"] != before {
		t.Fatalf("failed probe should not persist a state, got %+v", st.sessions["mer-1"])
	}
}

func TestActivity_WaitingInputPagesHuman(t *testing.T) {
	m, st, n, _ := newManager()
	st.sessions["mer-1"] = working("mer-1")
	if err := m.ApplyActivitySignal(ctx, "mer-1", ports.ActivitySignal{Valid: true, State: domain.ActivityWaitingInput, Timestamp: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if st.sessions["mer-1"].Activity.State != domain.ActivityWaitingInput {
		t.Fatalf("want waiting_input, got %+v", st.sessions["mer-1"].Activity)
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
		t.Fatal("invalid signal must not mutate")
	}
}

func TestPR_CIFailingNudgesAgentWithLogs(t *testing.T) {
	m, st, _, msg := newManager()
	st.sessions["mer-1"] = working("mer-1")
	o := openPR(ports.PRObservation{CI: domain.CIFailing, Checks: []domain.PRCheckRow{{Name: "build", CommitHash: "c1", Status: "failed", LogTail: "boom"}}})
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
		o := openPR(ports.PRObservation{CI: domain.CIFailing, Checks: []domain.PRCheckRow{{Name: "build", CommitHash: commit, Status: "failed", LogTail: "boom"}}})
		if err := m.ApplyPRObservation(ctx, "mer-1", o); err != nil {
			t.Fatal(err)
		}
	}
	if len(msg.msgs) != 2 {
		t.Fatalf("want 2 nudges then escalate, got %d", len(msg.msgs))
	}
	if n.last() != "reaction.escalated" {
		t.Fatalf("3rd failure should escalate, got %q", n.last())
	}
}

func TestPR_ReviewCommentsInjectedRegardlessOfAuthor(t *testing.T) {
	m, st, _, msg := newManager()
	st.sessions["mer-1"] = working("mer-1")
	o := openPR(ports.PRObservation{Review: domain.ReviewChangesRequest, Comments: []domain.PRComment{{ID: "1", Author: "greptileai", Body: "use a constant here"}}})
	if err := m.ApplyPRObservation(ctx, "mer-1", o); err != nil {
		t.Fatal(err)
	}
	if len(msg.msgs) != 1 || !strings.Contains(msg.msgs[0], "use a constant here") {
		t.Fatalf("review feedback should be injected, got %v", msg.msgs)
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
	if err := m.ApplyPRObservation(ctx, "mer-1", openPR(ports.PRObservation{Merged: true})); err != nil {
		t.Fatal(err)
	}
	got := st.sessions["mer-1"]
	if !got.IsTerminated {
		t.Fatalf("merge should terminate, got %+v", got)
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
		t.Fatal("failed fetch must write/fire nothing")
	}
}

func TestKill_TerminatesWithoutReacting(t *testing.T) {
	m, st, n, _ := newManager()
	st.sessions["mer-1"] = working("mer-1")
	if err := m.OnKillRequested(ctx, "mer-1"); err != nil {
		t.Fatal(err)
	}
	if !st.sessions["mer-1"].IsTerminated {
		t.Fatalf("want terminated, got %+v", st.sessions["mer-1"])
	}
	if len(n.events) != 0 {
		t.Fatal("explicit kill must not notify")
	}
}

func TestTickEscalations_DurationPagesHuman(t *testing.T) {
	m, st, n, msg := newManager()
	now := time.Now()
	m.clock = func() time.Time { return now }
	st.sessions["mer-1"] = working("mer-1")
	if err := m.ApplyPRObservation(ctx, "mer-1", openPR(ports.PRObservation{Mergeability: domain.MergeConflicting})); err != nil {
		t.Fatal(err)
	}
	if len(msg.msgs) != 1 {
		t.Fatalf("merge-conflict should nudge once, got %d", len(msg.msgs))
	}
	if err := m.TickEscalations(ctx, now.Add(16*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if n.last() != "reaction.escalated" {
		t.Fatalf("unaddressed conflict should escalate, got %q", n.last())
	}
}

func TestRunningSessions_ExcludesTerminated(t *testing.T) {
	m, st, _, _ := newManager()
	st.sessions["mer-1"] = working("mer-1")
	dead := working("mer-2")
	dead.IsTerminated = true
	st.sessions["mer-2"] = dead
	got, err := m.RunningSessions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != "mer-1" {
		t.Fatalf("want only live session, got %+v", got)
	}
}
