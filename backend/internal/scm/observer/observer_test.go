package observer

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	scmlog "github.com/aoagents/agent-orchestrator/backend/internal/scm/logging"
	"github.com/aoagents/agent-orchestrator/backend/internal/scm/store"
)

type fakeProvider struct {
	res ports.SCMObserveResult
	err error
}

func (f fakeProvider) Provider() domain.SCMProvider { return domain.SCMProviderGitHub }
func (f fakeProvider) ObserveSessions(context.Context, ports.SCMObserveRequest, ports.SCMProviderCache) (ports.SCMObserveResult, error) {
	return f.res, f.err
}

type echoProvider struct {
	provider domain.SCMProvider
	seen     []domain.SCMSubject
}

func (e *echoProvider) Provider() domain.SCMProvider { return e.provider }
func (e *echoProvider) ObserveSessions(_ context.Context, req ports.SCMObserveRequest, _ ports.SCMProviderCache) (ports.SCMObserveResult, error) {
	e.seen = append([]domain.SCMSubject(nil), req.Subjects...)
	return ports.SCMObserveResult{ProviderName: e.provider, Subjects: req.Subjects}, nil
}

type fakeLCM struct{ facts []ports.SCMFacts }

func (f *fakeLCM) ApplySCMObservation(_ context.Context, _ domain.SessionID, facts ports.SCMFacts) error {
	f.facts = append(f.facts, facts)
	return nil
}
func (f *fakeLCM) ApplyRuntimeObservation(context.Context, domain.SessionID, ports.RuntimeFacts) error {
	return nil
}
func (f *fakeLCM) ApplyActivitySignal(context.Context, domain.SessionID, ports.ActivitySignal) error {
	return nil
}
func (f *fakeLCM) ApplyPRObservation(context.Context, domain.SessionID, ports.PRObservation) error {
	return nil
}
func (f *fakeLCM) OnSpawnCompleted(context.Context, domain.SessionID, ports.SpawnOutcome) error {
	return nil
}
func (f *fakeLCM) OnKillRequested(context.Context, domain.SessionID, domain.TerminationReason) error {
	return nil
}
func (f *fakeLCM) TickEscalations(context.Context, time.Time) error { return nil }
func (f *fakeLCM) RunningSessions(context.Context) ([]domain.SessionRecord, error) {
	return nil, nil
}

func TestObserverPersistsSnapshotFansOutAndAppliesLCMFacts(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 5, 28, 12, 0, 0, 0, time.UTC)
	subj := domain.SCMSubject{SessionID: "s1", ProjectID: "p1", Provider: domain.SCMProviderGitHub, Host: "github.com", Repo: "o/r", Branch: "feat/27", PRNumber: 7}
	snap := domain.SCMSnapshot{SessionID: "s1", Subject: subj, Freshness: domain.SCMFreshnessFresh, ObservedAt: now, PR: &domain.SCMPullRequest{Number: 7, URL: "https://github.com/o/r/pull/7", State: domain.PROpen}, CI: domain.SCMCI{Summary: "failing", Checks: []domain.SCMCheck{{Name: "test", Conclusion: "failure"}}}}
	st := store.NewMemoryStore()
	lcm := &fakeLCM{}
	var events int
	o := New(st, lcm, fakeProvider{res: ports.SCMObserveResult{ProviderName: domain.SCMProviderGitHub, Subjects: []domain.SCMSubject{subj}, Snapshots: []domain.SCMSnapshot{snap}}})
	o.Clock = func() time.Time { return now }
	o.OnSnapshot = func(context.Context, domain.SCMSnapshot) error { events++; return nil }
	if err := o.Refresh(ctx, []domain.SCMSubject{subj}); err != nil {
		t.Fatal(err)
	}
	latest, ok, err := st.GetLatestSnapshot(ctx, "s1")
	if err != nil || !ok {
		t.Fatalf("latest ok=%v err=%v", ok, err)
	}
	if latest.Revision != 1 || latest.SemanticHash == "" {
		t.Fatalf("latest revision/hash %+v", latest)
	}
	if events != 1 {
		t.Fatalf("fanout events=%d", events)
	}
	if len(lcm.facts) != 1 || lcm.facts[0].CISummary != ports.CIFailing || !lcm.facts[0].Fetched {
		t.Fatalf("facts=%+v", lcm.facts)
	}
}

func TestObserverProjectsDraftAndFailingCIToLCMFacts(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 5, 28, 12, 0, 0, 0, time.UTC)
	subj := domain.SCMSubject{SessionID: "s1", ProjectID: "p1", Provider: domain.SCMProviderGitHub, Host: "github.com", Repo: "o/r", Branch: "feat/27", PRNumber: 7}
	snap := domain.SCMSnapshot{
		SessionID:  "s1",
		Subject:    subj,
		Freshness:  domain.SCMFreshnessFresh,
		ObservedAt: now,
		PR:         &domain.SCMPullRequest{Number: 7, URL: "https://github.com/o/r/pull/7", State: domain.PRDraft, Draft: true},
		CI:         domain.SCMCI{Summary: "failing", Checks: []domain.SCMCheck{{Name: "test", Status: "completed", Conclusion: "failure"}}},
	}
	st := store.NewMemoryStore()
	lcm := &fakeLCM{}
	o := New(st, lcm, fakeProvider{res: ports.SCMObserveResult{ProviderName: domain.SCMProviderGitHub, Subjects: []domain.SCMSubject{subj}, Snapshots: []domain.SCMSnapshot{snap}}})
	o.Clock = func() time.Time { return now }
	if err := o.Refresh(ctx, []domain.SCMSubject{subj}); err != nil {
		t.Fatal(err)
	}
	if len(lcm.facts) != 1 {
		t.Fatalf("facts=%+v", lcm.facts)
	}
	got := lcm.facts[0]
	if got.PRState != domain.PRDraft || !got.Draft || got.CISummary != ports.CIFailing || len(got.CIFailedChecks) != 1 {
		t.Fatalf("draft/failing CI facts not projected: %+v", got)
	}
}

func TestObserverLogsProviderNeutralObserveEvents(t *testing.T) {
	ctx := scmlog.WithCorrelationID(context.Background(), "corr-observe")
	now := time.Date(2026, 5, 28, 12, 0, 0, 0, time.UTC)
	subj := domain.SCMSubject{SessionID: "s1", ProjectID: "p1", Provider: domain.SCMProviderGitHub, Host: "github.com", Repo: "o/r", Branch: "feat/45", PRNumber: 45}
	snap := domain.SCMSnapshot{SessionID: "s1", Subject: subj, Freshness: domain.SCMFreshnessFresh, ObservedAt: now, PR: &domain.SCMPullRequest{Number: 45, URL: "https://github.com/o/r/pull/45", State: domain.PROpen}}
	var logs bytes.Buffer
	st := store.NewMemoryStore()
	o := New(st, nil, fakeProvider{res: ports.SCMObserveResult{ProviderName: domain.SCMProviderGitHub, Subjects: []domain.SCMSubject{subj}, Snapshots: []domain.SCMSnapshot{snap}}})
	o.Clock = func() time.Time { return now }
	o.Logger = jsonLogger(&logs)
	if err := o.Refresh(ctx, []domain.SCMSubject{subj}); err != nil {
		t.Fatal(err)
	}
	records := decodeLogRecords(t, logs.String())
	started := findLogRecord(t, records, scmlog.EventObserveStarted)
	assertLogField(t, started, scmlog.FieldCorrelationID, "corr-observe")
	assertLogField(t, started, scmlog.FieldProvider, "github")
	assertLogField(t, started, scmlog.FieldHost, "github.com")
	assertLogField(t, started, scmlog.FieldRepo, "o/r")
	assertLogField(t, started, scmlog.FieldProjectID, "p1")
	assertLogNumber(t, started, scmlog.FieldSessionCount, 1)
	if _, ok := started["pr_number"]; ok {
		t.Fatal("observer log used provider-specific pr_number field")
	}
	completed := findLogRecord(t, records, scmlog.EventObserveCompleted)
	assertLogField(t, completed, scmlog.FieldCorrelationID, "corr-observe")
	assertLogField(t, completed, scmlog.FieldFreshness, "fresh")
	assertLogNumber(t, completed, scmlog.FieldSnapshotCount, 1)
	assertLogNumber(t, completed, scmlog.FieldChangedCount, 1)
	saved := findLogRecord(t, records, scmlog.EventSnapshotSaved)
	assertLogNumber(t, saved, scmlog.FieldChangeRequestNumber, 45)
}

func TestObserverFailureLogsAndPersistsConciseDiagnostic(t *testing.T) {
	ctx := scmlog.WithCorrelationID(context.Background(), "corr-fail")
	now := time.Date(2026, 5, 28, 12, 0, 0, 0, time.UTC)
	subj := domain.SCMSubject{SessionID: "s1", ProjectID: "p1", Provider: domain.SCMProviderGitHub, Host: "github.com", Repo: "o/r", Branch: "feat/45", PRNumber: 45}
	err := &domain.SCMError{Kind: domain.SCMErrorRateLimited, Operation: "github.graphql_pr_batch", StatusCode: 403, Message: "rate limit exceeded " + string(bytes.Repeat([]byte("x"), 500))}
	var logs bytes.Buffer
	st := store.NewMemoryStore()
	o := New(st, nil, fakeProvider{res: ports.SCMObserveResult{ProviderName: domain.SCMProviderGitHub}, err: err})
	o.Clock = func() time.Time { return now }
	o.Logger = jsonLogger(&logs)
	if gotErr := o.Refresh(ctx, []domain.SCMSubject{subj}); gotErr == nil {
		t.Fatal("expected observe error")
	}
	records := decodeLogRecords(t, logs.String())
	failed := findLogRecord(t, records, scmlog.EventObserveFailed)
	assertLogField(t, failed, scmlog.FieldCorrelationID, "corr-fail")
	assertLogField(t, failed, scmlog.FieldErrorKind, string(domain.SCMErrorRateLimited))
	assertLogNumber(t, failed, scmlog.FieldStatusCode, 403)
	findLogRecord(t, records, scmlog.EventSnapshotUnavailable)
	latest, ok, gotErr := st.GetLatestSnapshot(ctx, "s1")
	if gotErr != nil || !ok {
		t.Fatalf("latest ok=%v err=%v", ok, gotErr)
	}
	if len(latest.Diagnostics) != 1 {
		t.Fatalf("diagnostics=%+v", latest.Diagnostics)
	}
	diag := latest.Diagnostics[0]
	if diag.Operation != "github.graphql_pr_batch" || diag.ErrorKind != domain.SCMErrorRateLimited || diag.StatusCode != 403 {
		t.Fatalf("bad diagnostic: %+v", diag)
	}
	if len(diag.Message) > 300 {
		t.Fatalf("diagnostic message too large: %d", len(diag.Message))
	}
}

func TestFactsFromUnavailableSnapshotIsNotFetched(t *testing.T) {
	facts := FactsFromSnapshot(domain.SCMSnapshot{Freshness: domain.SCMFreshnessUnavailable})
	if facts.Fetched {
		t.Fatal("unavailable snapshot must project to Fetched=false")
	}
}

func TestObserverPersistsProviderSnapshotsEvenWhenProviderReturnsError(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 5, 28, 12, 0, 0, 0, time.UTC)
	subj := domain.SCMSubject{SessionID: "s1", ProjectID: "p1", Provider: domain.SCMProviderGitHub, Host: "github.com", Repo: "o/r", Branch: "feat/27", PRNumber: 7}
	snap := domain.SCMSnapshot{
		SessionID:  "s1",
		Subject:    subj,
		Freshness:  domain.SCMFreshnessUnavailable,
		ObservedAt: now,
		PR:         &domain.SCMPullRequest{Number: 7, URL: "https://github.com/o/r/pull/7", State: domain.PROpen},
		CI:         domain.SCMCI{Summary: "passing"},
		Review:     domain.SCMReview{UnresolvedThreads: []domain.SCMReviewThread{{ID: "thread-old"}}},
	}
	st := store.NewMemoryStore()
	o := New(st, nil, fakeProvider{
		res: ports.SCMObserveResult{ProviderName: domain.SCMProviderGitHub, Subjects: []domain.SCMSubject{subj}, Snapshots: []domain.SCMSnapshot{snap}},
		err: &domain.SCMError{Kind: domain.SCMErrorNetwork, Operation: "github.graphql_pr_batch", Message: "down"},
	})
	o.Clock = func() time.Time { return now }
	if err := o.Refresh(ctx, []domain.SCMSubject{subj}); err == nil {
		t.Fatal("expected provider error")
	}
	latest, ok, err := st.GetLatestSnapshot(ctx, "s1")
	if err != nil || !ok {
		t.Fatalf("latest ok=%v err=%v", ok, err)
	}
	if latest.PR == nil || latest.PR.Number != 7 || latest.CI.Summary != "passing" || len(latest.Review.UnresolvedThreads) != 1 {
		t.Fatalf("provider snapshot was not preserved: %+v", latest)
	}
}

func TestObserverInfersOnlyRegisteredProviderWithoutGitHubDefault(t *testing.T) {
	ctx := context.Background()
	st := store.NewMemoryStore()
	provider := &echoProvider{provider: domain.SCMProviderGitLab}
	o := New(st, nil, provider)
	if err := o.Refresh(ctx, []domain.SCMSubject{{SessionID: "s1", ProjectID: "p1", Repo: "g/r", Branch: "feat/27"}}); err != nil {
		t.Fatal(err)
	}
	if len(provider.seen) != 1 || provider.seen[0].Provider != domain.SCMProviderGitLab {
		t.Fatalf("provider inference leaked GitHub default: %+v", provider.seen)
	}
}

func TestObserverRequiresProviderWhenMultipleProvidersRegistered(t *testing.T) {
	ctx := context.Background()
	st := store.NewMemoryStore()
	o := New(st, nil, &echoProvider{provider: domain.SCMProviderGitLab}, &echoProvider{provider: domain.SCMProviderBitbucket})
	err := o.Refresh(ctx, []domain.SCMSubject{{SessionID: "s1", ProjectID: "p1", Repo: "g/r", Branch: "feat/27"}})
	if err == nil {
		t.Fatal("missing provider should fail when observer has multiple providers")
	}
}

func TestSubjectsFromSessionsUsesConfigAndTypedBranchMetadataWithoutGitHubDefaults(t *testing.T) {
	sessions := []domain.Session{{
		SessionRecord: domain.SessionRecord{
			ID:        "s1",
			ProjectID: "p1",
			Metadata:  domain.SessionMetadata{Branch: "feat/27"},
		},
	}}
	subjects := SubjectsFromSessions(sessions, SubjectConfig{Provider: domain.SCMProviderGitLab, Host: "gitlab.com", Repo: "group/repo"})
	if len(subjects) != 1 {
		t.Fatalf("subjects=%d", len(subjects))
	}
	got := subjects[0]
	if got.Provider != domain.SCMProviderGitLab || got.Host != "gitlab.com" || got.Repo != "group/repo" || got.Branch != "feat/27" || got.PRNumber != 0 {
		t.Fatalf("bad subject: %+v", got)
	}
}

func jsonLogger(buf *bytes.Buffer) *slog.Logger {
	return slog.New(slog.NewJSONHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
}

func decodeLogRecords(t *testing.T, raw string) []map[string]any {
	t.Helper()
	lines := bytes.Split([]byte(raw), []byte("\n"))
	records := make([]map[string]any, 0, len(lines))
	for _, line := range lines {
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		var rec map[string]any
		if err := json.Unmarshal(line, &rec); err != nil {
			t.Fatalf("decode log %s: %v", line, err)
		}
		records = append(records, rec)
	}
	return records
}

func findLogRecord(t *testing.T, records []map[string]any, msg string) map[string]any {
	t.Helper()
	for _, rec := range records {
		if rec["msg"] == msg {
			return rec
		}
	}
	t.Fatalf("missing log %q in %+v", msg, records)
	return nil
}

func assertLogField(t *testing.T, rec map[string]any, key, want string) {
	t.Helper()
	if got, _ := rec[key].(string); got != want {
		t.Fatalf("%s=%q want %q in %+v", key, got, want, rec)
	}
}

func assertLogNumber(t *testing.T, rec map[string]any, key string, want float64) {
	t.Helper()
	if got, _ := rec[key].(float64); got != want {
		t.Fatalf("%s=%v want %v in %+v", key, rec[key], want, rec)
	}
}
