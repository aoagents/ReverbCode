package pr

import (
	"context"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

type fakeSessions struct {
	sessions map[domain.SessionID]domain.SessionRecord
}

func (f fakeSessions) GetSession(_ context.Context, id domain.SessionID) (domain.SessionRecord, bool, error) {
	r, ok := f.sessions[id]
	return r, ok, nil
}

type fakeWriter struct {
	pr       map[domain.SessionID]domain.PRRow
	comments map[string][]domain.PRComment
	checks   []domain.PRCheckRow
}

func (f *fakeWriter) WritePR(_ context.Context, pr domain.PRRow, checks []domain.PRCheckRow, comments []domain.PRComment) error {
	f.pr[pr.SessionID] = pr
	f.checks = append(f.checks, checks...)
	f.comments[pr.URL] = comments
	return nil
}

type fakeLifecycle struct {
	terminated []domain.SessionID
	observed   []ports.PRObservation
}

func (f *fakeLifecycle) ApplyPRObservation(_ context.Context, _ domain.SessionID, o ports.PRObservation) error {
	f.observed = append(f.observed, o)
	return nil
}

func (f *fakeLifecycle) MarkTerminated(_ context.Context, id domain.SessionID) error {
	f.terminated = append(f.terminated, id)
	return nil
}

func newPRManager() (*Manager, *fakeWriter, *fakeLifecycle) {
	fw := &fakeWriter{pr: map[domain.SessionID]domain.PRRow{}, comments: map[string][]domain.PRComment{}}
	fl := &fakeLifecycle{}
	m := New(Deps{
		Sessions:  fakeSessions{sessions: map[domain.SessionID]domain.SessionRecord{"mer-1": {ID: "mer-1", ProjectID: "mer"}}},
		Writer:    fw,
		Lifecycle: fl,
		Clock:     func() time.Time { return time.Unix(1, 0).UTC() },
	})
	return m, fw, fl
}

func TestApplyObservation_WritesPRChecksAndComments(t *testing.T) {
	m, fw, fl := newPRManager()
	o := ports.PRObservation{
		Fetched: true, URL: "https://example/pr/1", Number: 1, CI: domain.CIFailing,
		Checks:   []domain.PRCheckRow{{Name: "build", CommitHash: "c1", Status: domain.PRCheckFailed, LogTail: "boom"}},
		Comments: []domain.PRComment{{ID: "1", Author: "greptileai", Body: "use a constant here"}},
	}
	if err := m.ApplyObservation(context.Background(), "mer-1", o); err != nil {
		t.Fatal(err)
	}
	if got := fw.pr["mer-1"]; got.URL != o.URL || got.CI != domain.CIFailing {
		t.Fatalf("pr not written: %+v", got)
	}
	if len(fw.checks) != 1 || fw.checks[0].PRURL != o.URL || fw.checks[0].CreatedAt.IsZero() {
		t.Fatalf("checks not normalized: %+v", fw.checks)
	}
	if len(fw.comments[o.URL]) != 1 || fw.comments[o.URL][0].CreatedAt.IsZero() {
		t.Fatalf("comments not normalized: %+v", fw.comments)
	}
	if len(fl.terminated) != 0 {
		t.Fatalf("non-merged PR should not terminate: %v", fl.terminated)
	}
	if len(fl.observed) != 1 || fl.observed[0].URL != o.URL {
		t.Fatalf("non-merged PR should be forwarded to lifecycle, got %v", fl.observed)
	}
}

func TestApplyObservation_MergedTerminatesSession(t *testing.T) {
	m, _, fl := newPRManager()
	if err := m.ApplyObservation(context.Background(), "mer-1", ports.PRObservation{Fetched: true, URL: "pr1", Number: 1, Merged: true}); err != nil {
		t.Fatal(err)
	}
	if len(fl.terminated) != 1 || fl.terminated[0] != "mer-1" {
		t.Fatalf("merged PR should terminate session, got %v", fl.terminated)
	}
}

func TestApplyObservation_FailedFetchIsDropped(t *testing.T) {
	m, fw, fl := newPRManager()
	if err := m.ApplyObservation(context.Background(), "mer-1", ports.PRObservation{Fetched: false, URL: "pr1", CI: domain.CIFailing}); err != nil {
		t.Fatal(err)
	}
	if len(fw.pr) != 0 || len(fl.terminated) != 0 {
		t.Fatalf("failed fetch must write nothing, pr=%v term=%v", fw.pr, fl.terminated)
	}
}
