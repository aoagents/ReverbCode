package notification

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

type fakeStore struct {
	sessions map[domain.SessionID]domain.SessionRecord
	projects map[string]domain.ProjectRecord
	prs      map[domain.SessionID][]domain.PullRequest
	checks   map[string][]domain.PullRequestCheck
	comments map[string][]domain.PullRequestComment
	threads  map[string][]domain.PullRequestReviewThread

	checkLookups   []string
	commentLookups []string
	threadLookups  []string

	notifications map[string]domain.Notification
	upsertChanged int
	resolveCount  int
	upsertErr     error
}

func newFakeStore() *fakeStore {
	return &fakeStore{
		sessions:      map[domain.SessionID]domain.SessionRecord{},
		projects:      map[string]domain.ProjectRecord{},
		prs:           map[domain.SessionID][]domain.PullRequest{},
		checks:        map[string][]domain.PullRequestCheck{},
		comments:      map[string][]domain.PullRequestComment{},
		threads:       map[string][]domain.PullRequestReviewThread{},
		notifications: map[string]domain.Notification{},
	}
}

func (f *fakeStore) UpsertNotification(_ context.Context, n domain.Notification) (domain.Notification, bool, error) {
	if f.upsertErr != nil {
		return domain.Notification{}, false, f.upsertErr
	}
	key := string(n.ProjectID) + "\x00" + n.DedupeKey
	if cur, ok := f.notifications[key]; ok {
		if cur.Fingerprint == n.Fingerprint {
			return cur, false, nil
		}
		n.ID = cur.ID
		n.CreatedAt = cur.CreatedAt
		f.notifications[key] = n
		f.upsertChanged++
		return n, true, nil
	}
	f.notifications[key] = n
	f.upsertChanged++
	return n, true, nil
}
func (f *fakeStore) ResolveNotifications(_ context.Context, filter domain.NotificationResolveFilter, _ time.Time) (int, error) {
	f.resolveCount++
	return 1, nil
}
func (f *fakeStore) GetSession(_ context.Context, id domain.SessionID) (domain.SessionRecord, bool, error) {
	r, ok := f.sessions[id]
	return r, ok, nil
}
func (f *fakeStore) GetProject(_ context.Context, id string) (domain.ProjectRecord, bool, error) {
	r, ok := f.projects[id]
	return r, ok, nil
}
func (f *fakeStore) ListPRsBySession(_ context.Context, id domain.SessionID) ([]domain.PullRequest, error) {
	return f.prs[id], nil
}
func (f *fakeStore) ListChecks(_ context.Context, prURL string) ([]domain.PullRequestCheck, error) {
	f.checkLookups = append(f.checkLookups, prURL)
	return f.checks[prURL], nil
}
func (f *fakeStore) ListPRComments(_ context.Context, prURL string) ([]domain.PullRequestComment, error) {
	f.commentLookups = append(f.commentLookups, prURL)
	return f.comments[prURL], nil
}
func (f *fakeStore) ListPRReviewThreads(_ context.Context, prURL string) ([]domain.PullRequestReviewThread, error) {
	f.threadLookups = append(f.threadLookups, prURL)
	return f.threads[prURL], nil
}

func seededServiceStore(now time.Time) *fakeStore {
	st := newFakeStore()
	st.projects["mer"] = domain.ProjectRecord{ID: "mer", Path: "/repo/mer", DisplayName: "Mer"}
	st.sessions["mer-1"] = domain.SessionRecord{ID: "mer-1", ProjectID: "mer", DisplayName: "Fix CI", Activity: domain.Activity{State: domain.ActivityActive, LastActivityAt: now}}
	st.prs["mer-1"] = []domain.PullRequest{{URL: "https://github.com/o/r/pull/1", HTMLURL: "https://github.com/o/r/pull/1", SessionID: "mer-1", Number: 1, Title: "PR title", HeadSHA: "c1"}}
	st.checks["https://github.com/o/r/pull/1"] = []domain.PullRequestCheck{{Name: "build", CommitHash: "c1", Status: domain.PRCheckFailed, URL: "https://ci/build", LogTail: "boom", CreatedAt: now}}
	st.comments["https://github.com/o/r/pull/1"] = []domain.PullRequestComment{{ID: "c1", ThreadID: "t1", Body: "fix", CreatedAt: now}}
	st.threads["https://github.com/o/r/pull/1"] = []domain.PullRequestReviewThread{{ThreadID: "t1", UpdatedAt: now}}
	return st
}

func TestNotifyEnrichesMakesActionsAndDedupes(t *testing.T) {
	now := time.Date(2026, 6, 6, 12, 0, 0, 0, time.UTC)
	st := seededServiceStore(now)
	svc := New(Deps{Store: st, Clock: func() time.Time { return now }})
	intent := domain.NotificationIntent{Type: domain.NotificationCIFailing, Priority: domain.NotificationWarning, ProjectID: "mer", SessionID: "mer-1", Source: "test", DedupeKey: "ci:pr:build:c1", OccurredAt: now, Context: domain.NotificationIntentContext{PRURL: "https://github.com/o/r/pull/1", CheckName: "build", CheckURL: "https://ci/build", CommitHash: "c1"}}
	if err := svc.Notify(context.Background(), intent); err != nil {
		t.Fatal(err)
	}
	if st.upsertChanged != 1 {
		t.Fatalf("writes = %d, want 1", st.upsertChanged)
	}
	var got domain.Notification
	for _, n := range st.notifications {
		got = n
	}
	if got.Title != "CI failed" || got.Subject.PRURL == "" || len(got.Actions) != 3 || !got.Actions[0].Primary {
		t.Fatalf("notification not enriched: %+v", got)
	}
	if err := svc.Notify(context.Background(), intent); err != nil {
		t.Fatal(err)
	}
	if st.upsertChanged != 1 {
		t.Fatalf("same fingerprint should no-op, writes = %d", st.upsertChanged)
	}
	intent.Context.CheckURL = "https://ci/build/2"
	if err := svc.Notify(context.Background(), intent); err != nil {
		t.Fatal(err)
	}
	if st.upsertChanged != 2 {
		t.Fatalf("changed fingerprint should update, writes = %d", st.upsertChanged)
	}
}

func TestNotifyMissingOptionalFactsStillProducesFallback(t *testing.T) {
	now := time.Date(2026, 6, 6, 12, 0, 0, 0, time.UTC)
	st := newFakeStore()
	st.projects["mer"] = domain.ProjectRecord{ID: "mer"}
	st.sessions["mer-1"] = domain.SessionRecord{ID: "mer-1", ProjectID: "mer"}
	svc := New(Deps{Store: st, Clock: func() time.Time { return now }})
	err := svc.Notify(context.Background(), domain.NotificationIntent{Type: domain.NotificationSessionInput, Priority: domain.NotificationUrgent, ProjectID: "mer", SessionID: "mer-1", Source: "test", DedupeKey: "session-input:mer-1:t", OccurredAt: now})
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range st.notifications {
		if n.Summary == "" || n.Actions[0].ID != "open_session" {
			t.Fatalf("fallback notification = %+v", n)
		}
	}
}

func TestNotifyPreservesRequestedPRURLWhenStoredPRDoesNotMatch(t *testing.T) {
	now := time.Date(2026, 6, 6, 12, 0, 0, 0, time.UTC)
	st := newFakeStore()
	st.projects["mer"] = domain.ProjectRecord{ID: "mer"}
	st.sessions["mer-1"] = domain.SessionRecord{ID: "mer-1", ProjectID: "mer"}
	newerPRURL := "https://github.com/o/r/pull/2"
	requestedPRURL := "https://github.com/o/r/pull/1"
	st.prs["mer-1"] = []domain.PullRequest{{URL: newerPRURL, HTMLURL: newerPRURL, SessionID: "mer-1", Number: 2, Title: "newer"}}
	st.checks[newerPRURL] = []domain.PullRequestCheck{{Name: "build", CommitHash: "c2", Status: domain.PRCheckFailed, URL: "https://ci/newer"}}
	svc := New(Deps{Store: st, Clock: func() time.Time { return now }})

	err := svc.Notify(context.Background(), domain.NotificationIntent{
		Type:      domain.NotificationCIFailing,
		Priority:  domain.NotificationWarning,
		ProjectID: "mer",
		SessionID: "mer-1",
		Source:    "test",
		DedupeKey: "ci:older:build:c1",
		Context: domain.NotificationIntentContext{
			PRURL:      requestedPRURL,
			CheckName:  "build",
			CheckURL:   "https://ci/older",
			CommitHash: "c1",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(st.checkLookups) != 1 || st.checkLookups[0] != requestedPRURL {
		t.Fatalf("checks looked up for %v, want only requested PR %q", st.checkLookups, requestedPRURL)
	}
	for _, n := range st.notifications {
		if n.Subject.PRURL != requestedPRURL {
			t.Fatalf("notification subject PR URL = %q, want %q (notification=%+v)", n.Subject.PRURL, requestedPRURL, n)
		}
	}
}

func TestNotifyRequiredFactAndStoreErrors(t *testing.T) {
	now := time.Date(2026, 6, 6, 12, 0, 0, 0, time.UTC)
	st := seededServiceStore(now)
	svc := New(Deps{Store: st, Clock: func() time.Time { return now }})
	base := domain.NotificationIntent{Type: domain.NotificationSessionInput, Priority: domain.NotificationUrgent, ProjectID: "mer", SessionID: "missing", Source: "test", DedupeKey: "k", OccurredAt: now}
	if err := svc.Notify(context.Background(), base); err == nil {
		t.Fatal("unknown session should error")
	}
	base.SessionID = "mer-1"
	st.upsertErr = errors.New("disk full")
	if err := svc.Notify(context.Background(), base); err == nil {
		t.Fatal("store failure should error")
	}
}

func TestMergeCompletedResolvesSupersededPRNotifications(t *testing.T) {
	now := time.Date(2026, 6, 6, 12, 0, 0, 0, time.UTC)
	st := seededServiceStore(now)
	svc := New(Deps{Store: st, Clock: func() time.Time { return now }})
	err := svc.Notify(context.Background(), domain.NotificationIntent{Type: domain.NotificationMergeCompleted, Priority: domain.NotificationInfo, ProjectID: "mer", SessionID: "mer-1", Source: "test", DedupeKey: "merge-completed:pr:m1", OccurredAt: now, Context: domain.NotificationIntentContext{PRURL: "https://github.com/o/r/pull/1"}})
	if err != nil {
		t.Fatal(err)
	}
	if st.resolveCount != 1 {
		t.Fatalf("resolve count = %d, want 1", st.resolveCount)
	}
}
