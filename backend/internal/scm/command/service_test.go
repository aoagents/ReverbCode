package command

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	scmlog "github.com/aoagents/agent-orchestrator/backend/internal/scm/logging"
	"github.com/aoagents/agent-orchestrator/backend/internal/scm/store"
)

type fakeCommandProvider struct {
	called        ports.SCMCommand
	invalidations []domain.SCMProviderCachePrefix
	err           error
}

func (f *fakeCommandProvider) Provider() domain.SCMProvider { return domain.SCMProviderGitHub }
func (f *fakeCommandProvider) Capabilities() ports.SCMCommandCapabilities {
	return ports.SCMCommandCapabilities{Merge: true, Close: true, Comment: true, Assign: true, Checkout: true}
}
func (f *fakeCommandProvider) Merge(_ context.Context, r ports.SCMCommandRequest) (ports.SCMCommandResult, error) {
	f.called = ports.SCMCommandMerge
	return ports.SCMCommandResult{Provider: domain.SCMProviderGitHub, Command: r.Command, ChangeRequest: r.ChangeRequest}, f.err
}
func (f *fakeCommandProvider) Close(context.Context, ports.SCMCommandRequest) (ports.SCMCommandResult, error) {
	f.called = ports.SCMCommandClose
	return ports.SCMCommandResult{}, f.err
}
func (f *fakeCommandProvider) Comment(context.Context, ports.SCMCommandRequest) (ports.SCMCommandResult, error) {
	f.called = ports.SCMCommandComment
	return ports.SCMCommandResult{}, f.err
}
func (f *fakeCommandProvider) Assign(context.Context, ports.SCMCommandRequest) (ports.SCMCommandResult, error) {
	f.called = ports.SCMCommandAssign
	return ports.SCMCommandResult{}, f.err
}
func (f *fakeCommandProvider) Checkout(_ context.Context, r ports.SCMCommandRequest) (ports.SCMCommandResult, error) {
	f.called = ports.SCMCommandCheckout
	return ports.SCMCommandResult{Provider: domain.SCMProviderGitHub, Command: r.Command, ChangeRequest: r.ChangeRequest}, f.err
}
func (f *fakeCommandProvider) CacheInvalidationPrefixes(domain.SCMSubject, ports.SCMCommand) []domain.SCMProviderCachePrefix {
	return f.invalidations
}

type fakeRefresh struct {
	called bool
	err    error
}

func (f *fakeRefresh) Refresh(context.Context, []domain.SCMSubject) error {
	f.called = true
	return f.err
}

type fakeAudit struct {
	called bool
	err    error
}

func (f *fakeAudit) RecordSCMCommand(context.Context, ports.SCMCommandResult, error) error {
	f.called = true
	return f.err
}

func TestMergeInvalidatesProviderCacheAndRefreshes(t *testing.T) {
	ctx := context.Background()
	st := store.NewMemoryStore()
	subj := domain.SCMSubject{SessionID: "s1", ProjectID: "p1", Provider: domain.SCMProviderGitHub, Host: "github.com", Repo: "o/r", Branch: "feat/27", PRNumber: 7, CredentialHash: "cred"}
	if err := st.UpsertSubject(ctx, subj); err != nil {
		t.Fatal(err)
	}
	key := domain.SCMProviderCacheKey{SCMProviderCacheScope: subj.CacheScope(), Namespace: "provider-checks", Key: "sha"}
	if err := st.PutProviderCache(ctx, domain.SCMProviderCacheEntry{Key: key, ETag: "etag"}); err != nil {
		t.Fatal(err)
	}
	provider := &fakeCommandProvider{invalidations: []domain.SCMProviderCachePrefix{{SCMProviderCacheScope: subj.CacheScope(), Namespace: "provider-checks"}}}
	refresh := &fakeRefresh{}
	svc := New(st, refresh, provider)
	res, err := svc.MergeChangeRequest(ctx, "s1", ports.SCMCommandRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if provider.called != ports.SCMCommandMerge || res.ChangeRequest.Number != 7 {
		t.Fatalf("provider called=%s result=%+v", provider.called, res)
	}
	if _, ok, _ := st.GetProviderCache(ctx, key); ok {
		t.Fatal("merge should invalidate check cache")
	}
	if !refresh.called {
		t.Fatal("command should trigger observer refresh")
	}
}

func TestCommandServiceLogsStartedCompletedAndCacheInvalidated(t *testing.T) {
	ctx := scmlog.WithCorrelationID(context.Background(), "corr-command")
	st := store.NewMemoryStore()
	subj := domain.SCMSubject{SessionID: "s1", ProjectID: "p1", Provider: domain.SCMProviderGitHub, Host: "github.com", Repo: "o/r", Branch: "feat/45", PRNumber: 45, CredentialHash: "cred"}
	if err := st.UpsertSubject(ctx, subj); err != nil {
		t.Fatal(err)
	}
	key := domain.SCMProviderCacheKey{SCMProviderCacheScope: subj.CacheScope(), Namespace: "provider-checks", Key: "sha"}
	if err := st.PutProviderCache(ctx, domain.SCMProviderCacheEntry{Key: key, ETag: "etag"}); err != nil {
		t.Fatal(err)
	}
	var logs bytes.Buffer
	provider := &fakeCommandProvider{invalidations: []domain.SCMProviderCachePrefix{{SCMProviderCacheScope: subj.CacheScope(), Namespace: "provider-checks"}}}
	svc := New(st, nil, provider)
	svc.Logger = jsonLogger(&logs)
	if _, err := svc.MergeChangeRequest(ctx, "s1", ports.SCMCommandRequest{Actor: "ao"}); err != nil {
		t.Fatal(err)
	}
	records := decodeLogRecords(t, logs.String())
	started := findLogRecord(t, records, scmlog.EventCommandStarted)
	assertLogField(t, started, scmlog.FieldCorrelationID, "corr-command")
	assertLogField(t, started, scmlog.FieldProvider, "github")
	assertLogField(t, started, scmlog.FieldRepo, "o/r")
	assertLogField(t, started, scmlog.FieldCommand, string(ports.SCMCommandMerge))
	assertLogField(t, started, scmlog.FieldActor, "ao")
	assertLogNumber(t, started, scmlog.FieldChangeRequestNumber, 45)
	if _, ok := started["pr_number"]; ok {
		t.Fatal("command log used provider-specific pr_number field")
	}
	completed := findLogRecord(t, records, scmlog.EventCommandCompleted)
	assertLogNumber(t, completed, scmlog.FieldChangeRequestNumber, 45)
	findLogRecord(t, records, scmlog.EventCommandCacheInvalid)
}

func TestCommandServiceLogsFailuresWithoutCommentBodyAndAttachesDiagnostic(t *testing.T) {
	ctx := scmlog.WithCorrelationID(context.Background(), "corr-command-fail")
	st := store.NewMemoryStore()
	subj := domain.SCMSubject{SessionID: "s1", ProjectID: "p1", Provider: domain.SCMProviderGitHub, Host: "github.com", Repo: "o/r", Branch: "feat/45", PRNumber: 45}
	if err := st.UpsertSubject(ctx, subj); err != nil {
		t.Fatal(err)
	}
	commentBody := "SECRET_COMMENT_BODY"
	providerErr := &domain.SCMError{Kind: domain.SCMErrorAuthFailed, Operation: "github.command.comment", StatusCode: 401, Message: "bad credentials"}
	var logs bytes.Buffer
	svc := New(st, nil, &fakeCommandProvider{err: providerErr})
	svc.Logger = jsonLogger(&logs)
	res, err := svc.CommentOnChangeRequest(ctx, "s1", commentBody)
	if err == nil {
		t.Fatal("expected command error")
	}
	records := decodeLogRecords(t, logs.String())
	failed := findLogRecord(t, records, scmlog.EventCommandFailed)
	assertLogField(t, failed, scmlog.FieldCorrelationID, "corr-command-fail")
	assertLogField(t, failed, scmlog.FieldErrorKind, string(domain.SCMErrorAuthFailed))
	assertLogNumber(t, failed, scmlog.FieldStatusCode, 401)
	if strings.Contains(logs.String(), commentBody) {
		t.Fatalf("comment body leaked into command logs: %s", logs.String())
	}
	if len(res.Diagnostics) != 1 || res.Diagnostics[0].ErrorKind != domain.SCMErrorAuthFailed || res.Diagnostics[0].StatusCode != 401 {
		t.Fatalf("bad command diagnostics: %+v", res.Diagnostics)
	}
}

func TestCommandServiceLogsAuditAndRefreshFailuresSeparately(t *testing.T) {
	ctx := scmlog.WithCorrelationID(context.Background(), "corr-command-after")
	st := store.NewMemoryStore()
	subj := domain.SCMSubject{SessionID: "s1", ProjectID: "p1", Provider: domain.SCMProviderGitHub, Host: "github.com", Repo: "o/r", Branch: "feat/45", PRNumber: 45}
	if err := st.UpsertSubject(ctx, subj); err != nil {
		t.Fatal(err)
	}
	var logs bytes.Buffer
	audit := &fakeAudit{err: fmt.Errorf("audit down")}
	refresh := &fakeRefresh{err: &domain.SCMError{Kind: domain.SCMErrorNetwork, Operation: "observe", Message: "refresh down"}}
	svc := New(st, refresh, &fakeCommandProvider{})
	svc.Audit = audit
	svc.Logger = jsonLogger(&logs)
	res, err := svc.MergeChangeRequest(ctx, "s1", ports.SCMCommandRequest{})
	if err == nil {
		t.Fatal("expected refresh error")
	}
	if !audit.called || !refresh.called {
		t.Fatalf("audit=%v refresh=%v", audit.called, refresh.called)
	}
	if res.Command != ports.SCMCommandMerge || res.ChangeRequest.Number != 45 {
		t.Fatalf("command result hidden by refresh failure: %+v", res)
	}
	records := decodeLogRecords(t, logs.String())
	findLogRecord(t, records, scmlog.EventCommandCompleted)
	findLogRecord(t, records, scmlog.EventCommandAuditFailed)
	refreshFailed := findLogRecord(t, records, scmlog.EventCommandRefreshFailed)
	assertLogField(t, refreshFailed, scmlog.FieldErrorKind, string(domain.SCMErrorNetwork))
}

func TestCheckoutDoesNotInvalidateOrRefreshProviderCache(t *testing.T) {
	ctx := context.Background()
	st := store.NewMemoryStore()
	subj := domain.SCMSubject{SessionID: "s1", ProjectID: "p1", Provider: domain.SCMProviderGitHub, Host: "github.com", Repo: "o/r", Branch: "feat/27", PRNumber: 7, CredentialHash: "cred"}
	if err := st.UpsertSubject(ctx, subj); err != nil {
		t.Fatal(err)
	}
	key := domain.SCMProviderCacheKey{SCMProviderCacheScope: subj.CacheScope(), Namespace: "provider-checks", Key: "sha"}
	if err := st.PutProviderCache(ctx, domain.SCMProviderCacheEntry{Key: key, ETag: "etag"}); err != nil {
		t.Fatal(err)
	}
	provider := &fakeCommandProvider{}
	refresh := &fakeRefresh{}
	svc := New(st, refresh, provider)
	if _, err := svc.CheckoutChangeRequest(ctx, "s1", "/tmp/workspace"); err != nil {
		t.Fatal(err)
	}
	if provider.called != ports.SCMCommandCheckout {
		t.Fatalf("provider called=%s", provider.called)
	}
	if _, ok, _ := st.GetProviderCache(ctx, key); !ok {
		t.Fatal("checkout should not invalidate provider cache")
	}
	if refresh.called {
		t.Fatal("checkout should not trigger observer refresh")
	}
}

func TestCommentInvalidatesOnlyProviderReviewCache(t *testing.T) {
	ctx := context.Background()
	st := store.NewMemoryStore()
	subj := domain.SCMSubject{SessionID: "s1", ProjectID: "p1", Provider: domain.SCMProviderGitHub, Host: "github.com", Repo: "o/r", Branch: "feat/27", PRNumber: 7, CredentialHash: "cred"}
	if err := st.UpsertSubject(ctx, subj); err != nil {
		t.Fatal(err)
	}
	reviewKey := domain.SCMProviderCacheKey{SCMProviderCacheScope: subj.CacheScope(), Namespace: "reviews", Key: "7"}
	checkKey := domain.SCMProviderCacheKey{SCMProviderCacheScope: subj.CacheScope(), Namespace: "checks", Key: "sha"}
	for _, key := range []domain.SCMProviderCacheKey{reviewKey, checkKey} {
		if err := st.PutProviderCache(ctx, domain.SCMProviderCacheEntry{Key: key, ETag: key.Namespace}); err != nil {
			t.Fatal(err)
		}
	}
	provider := &fakeCommandProvider{invalidations: []domain.SCMProviderCachePrefix{{SCMProviderCacheScope: subj.CacheScope(), Namespace: "reviews"}}}
	refresh := &fakeRefresh{}
	svc := New(st, refresh, provider)
	if _, err := svc.CommentOnChangeRequest(ctx, "s1", "hello"); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := st.GetProviderCache(ctx, reviewKey); ok {
		t.Fatal("review cache should be invalidated")
	}
	if _, ok, _ := st.GetProviderCache(ctx, checkKey); !ok {
		t.Fatal("check cache should remain after comment")
	}
	if !refresh.called {
		t.Fatal("comment should refresh after precise invalidation")
	}
}

func TestCommandRejectsSessionWithoutBoundChangeRequest(t *testing.T) {
	ctx := context.Background()
	st := store.NewMemoryStore()
	subj := domain.SCMSubject{SessionID: "s1", ProjectID: "p1", Provider: domain.SCMProviderGitHub, Host: "github.com", Repo: "o/r", Branch: "feat/27"}
	if err := st.UpsertSubject(ctx, subj); err != nil {
		t.Fatal(err)
	}
	provider := &fakeCommandProvider{}
	svc := New(st, nil, provider)
	_, err := svc.MergeChangeRequest(ctx, "s1", ports.SCMCommandRequest{})
	var scmErr *domain.SCMError
	if !errors.As(err, &scmErr) || scmErr.Kind != domain.SCMErrorNotFound {
		t.Fatalf("err=%T %[1]v", err)
	}
	if provider.called != "" {
		t.Fatalf("provider should not be called, got %s", provider.called)
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
