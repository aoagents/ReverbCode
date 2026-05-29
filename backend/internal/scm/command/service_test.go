package command

import (
	"context"
	"errors"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/scm/store"
)

type fakeCommandProvider struct {
	called        ports.SCMCommand
	invalidations []domain.SCMProviderCachePrefix
}

func (f *fakeCommandProvider) Provider() domain.SCMProvider { return domain.SCMProviderGitHub }
func (f *fakeCommandProvider) Capabilities() ports.SCMCommandCapabilities {
	return ports.SCMCommandCapabilities{Merge: true, Close: true, Comment: true, Assign: true, Checkout: true}
}
func (f *fakeCommandProvider) Merge(_ context.Context, r ports.SCMCommandRequest) (ports.SCMCommandResult, error) {
	f.called = ports.SCMCommandMerge
	return ports.SCMCommandResult{Provider: domain.SCMProviderGitHub, Command: r.Command, ChangeRequest: r.ChangeRequest}, nil
}
func (f *fakeCommandProvider) Close(context.Context, ports.SCMCommandRequest) (ports.SCMCommandResult, error) {
	f.called = ports.SCMCommandClose
	return ports.SCMCommandResult{}, nil
}
func (f *fakeCommandProvider) Comment(context.Context, ports.SCMCommandRequest) (ports.SCMCommandResult, error) {
	f.called = ports.SCMCommandComment
	return ports.SCMCommandResult{}, nil
}
func (f *fakeCommandProvider) Assign(context.Context, ports.SCMCommandRequest) (ports.SCMCommandResult, error) {
	f.called = ports.SCMCommandAssign
	return ports.SCMCommandResult{}, nil
}
func (f *fakeCommandProvider) Checkout(_ context.Context, r ports.SCMCommandRequest) (ports.SCMCommandResult, error) {
	f.called = ports.SCMCommandCheckout
	return ports.SCMCommandResult{Provider: domain.SCMProviderGitHub, Command: r.Command, ChangeRequest: r.ChangeRequest}, nil
}
func (f *fakeCommandProvider) CacheInvalidationPrefixes(domain.SCMSubject, ports.SCMCommand) []domain.SCMProviderCachePrefix {
	return f.invalidations
}

type fakeRefresh struct{ called bool }

func (f *fakeRefresh) Refresh(context.Context, []domain.SCMSubject) error {
	f.called = true
	return nil
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
