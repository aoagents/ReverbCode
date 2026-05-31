package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func TestSaveSnapshotRevisionAndSemanticHash(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 5, 28, 12, 0, 0, 0, time.UTC)
	s := NewMemoryStore(WithClock(func() time.Time { return now }))
	subj := domain.SCMSubject{SessionID: "s1", ProjectID: "p1", Provider: domain.SCMProviderGitHub, Host: "github.com", Repo: "ao/test", Branch: "feat/27", PRNumber: 7}
	snap := domain.SCMSnapshot{SessionID: "s1", Subject: subj, ObservedAt: now, PR: &domain.SCMPullRequest{Number: 7, URL: "https://github.com/ao/test/pull/7", State: domain.PROpen}, CI: domain.SCMCI{Summary: "passing"}}

	saved, changed, err := s.SaveSnapshot(ctx, snap)
	if err != nil || !changed {
		t.Fatalf("SaveSnapshot first changed=%v err=%v", changed, err)
	}
	if saved.Revision != 1 || saved.SemanticHash == "" {
		t.Fatalf("revision/hash = %d/%q", saved.Revision, saved.SemanticHash)
	}
	gotSubject, ok, err := s.GetSubject(ctx, "s1")
	if err != nil || !ok {
		t.Fatalf("subject from snapshot ok=%v err=%v", ok, err)
	}
	if gotSubject.CreatedAt.IsZero() || gotSubject.UpdatedAt.IsZero() {
		t.Fatalf("snapshot subject timestamps were not initialized: %+v", gotSubject)
	}

	snap.ObservedAt = now.Add(time.Hour)
	saved2, changed, err := s.SaveSnapshot(ctx, snap)
	if err != nil {
		t.Fatal(err)
	}
	if changed {
		t.Fatalf("ObservedAt-only change should not create a revision")
	}
	if saved2.Revision != 1 {
		t.Fatalf("unchanged revision = %d", saved2.Revision)
	}

	snap.CI.Summary = "failing"
	saved3, changed, err := s.SaveSnapshot(ctx, snap)
	if err != nil || !changed {
		t.Fatalf("semantic change changed=%v err=%v", changed, err)
	}
	if saved3.Revision != 2 {
		t.Fatalf("revision after semantic change = %d", saved3.Revision)
	}
}

func TestFileStorePersistsSubjectsSnapshotsAndScopedCache(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "scm.json")
	s, err := NewFileStore(path)
	if err != nil {
		t.Fatal(err)
	}
	subj := domain.SCMSubject{SessionID: "s1", ProjectID: "p1", Provider: domain.SCMProviderGitHub, Host: "github.com", Repo: "ao/test", Branch: "feat/27", CredentialHash: "cred-a"}
	if err := s.UpsertSubject(ctx, subj); err != nil {
		t.Fatal(err)
	}
	keyA := domain.SCMProviderCacheKey{SCMProviderCacheScope: subj.CacheScope(), Namespace: "provider-cache", Key: "sha"}
	if err := s.PutProviderCache(ctx, domain.SCMProviderCacheEntry{Key: keyA, ETag: "a"}); err != nil {
		t.Fatal(err)
	}
	keyB := keyA
	keyB.CredentialHash = "cred-b"
	if err := s.PutProviderCache(ctx, domain.SCMProviderCacheEntry{Key: keyB, ETag: "b"}); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteProviderCache(ctx, domain.SCMProviderCachePrefix{SCMProviderCacheScope: subj.CacheScope(), Namespace: "provider-cache"}); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := s.GetProviderCache(ctx, keyA); ok {
		t.Fatalf("credential scoped cache A was not deleted")
	}
	if got, ok, _ := s.GetProviderCache(ctx, keyB); !ok || got.ETag != "b" {
		t.Fatalf("credential scoped cache B got=%+v ok=%v", got, ok)
	}

	reopened, err := NewFileStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if got, ok, _ := reopened.GetSubject(ctx, "s1"); !ok || got.Repo != subj.Repo {
		t.Fatalf("reopened subject got=%+v ok=%v", got, ok)
	}
}

func TestProviderCachePrunesOldestEntriesByNamespace(t *testing.T) {
	ctx := context.Background()
	base := time.Date(2026, 5, 28, 12, 0, 0, 0, time.UTC)
	s := NewMemoryStore()
	scope := domain.SCMProviderCacheScope{Provider: domain.SCMProviderGitHub, Host: "github.com", Repo: "ao/test", CredentialHash: "cred"}
	otherScope := domain.SCMProviderCacheScope{Provider: domain.SCMProviderGitHub, Host: "github.com", Repo: "ao/other", CredentialHash: "cred"}
	capN := 3
	for i := 0; i < capN+2; i++ {
		key := domain.SCMProviderCacheKey{SCMProviderCacheScope: scope, Namespace: "provider-owned", Key: string(rune('a' + i))}
		if err := s.PutProviderCache(ctx, domain.SCMProviderCacheEntry{Key: key, ETag: key.Key, UpdatedAt: base.Add(time.Duration(i) * time.Second), MaxEntries: capN}); err != nil {
			t.Fatal(err)
		}
	}
	otherKey := domain.SCMProviderCacheKey{SCMProviderCacheScope: otherScope, Namespace: "provider-owned", Key: "a"}
	if err := s.PutProviderCache(ctx, domain.SCMProviderCacheEntry{Key: otherKey, ETag: "other", UpdatedAt: base, MaxEntries: capN}); err != nil {
		t.Fatal(err)
	}
	oldKey := domain.SCMProviderCacheKey{SCMProviderCacheScope: scope, Namespace: "provider-owned", Key: string(rune('a'))}
	if _, ok, _ := s.GetProviderCache(ctx, oldKey); ok {
		t.Fatal("oldest scoped cache entry should have been pruned")
	}
	newKey := domain.SCMProviderCacheKey{SCMProviderCacheScope: scope, Namespace: "provider-owned", Key: string(rune('a' + capN + 1))}
	if _, ok, _ := s.GetProviderCache(ctx, newKey); !ok {
		t.Fatal("newest scoped cache entry should remain")
	}
	if _, ok, _ := s.GetProviderCache(ctx, otherKey); !ok {
		t.Fatal("cache cap should not prune another scope")
	}
}
