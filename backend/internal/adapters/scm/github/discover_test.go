package github

import (
	"net/http"
	"testing"
)

func TestFindPRForBranch_Found(t *testing.T) {
	f := newFakeGH(t)
	f.on(http.MethodGet, "/repos/octocat/hello/pulls", func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("head"); got != "octocat:feature-x" {
			t.Errorf("head = %q, want octocat:feature-x", got)
		}
		if got := r.URL.Query().Get("state"); got != "open" {
			t.Errorf("state = %q, want open", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{"html_url":"https://github.com/octocat/hello/pull/42","url":"https://api.github.com/repos/octocat/hello/pulls/42"}]`))
	})
	p := newProviderForTest(t, f)

	url, found, err := p.FindPRForBranch(ctx(), "octocat", "hello", "feature-x")
	if err != nil {
		t.Fatalf("FindPRForBranch: %v", err)
	}
	if !found {
		t.Fatal("found = false, want true")
	}
	if url != "https://github.com/octocat/hello/pull/42" {
		t.Fatalf("url = %q", url)
	}
}

func TestFindPRForBranch_NoOpenPR(t *testing.T) {
	f := newFakeGH(t)
	f.on(http.MethodGet, "/repos/octocat/hello/pulls", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[]`))
	})
	p := newProviderForTest(t, f)

	url, found, err := p.FindPRForBranch(ctx(), "octocat", "hello", "feature-x")
	if err != nil {
		t.Fatalf("FindPRForBranch: %v", err)
	}
	if found {
		t.Fatalf("found = true (url %q), want false for empty list", url)
	}
}

func TestFindPRForBranch_AuthErrorSurfaces(t *testing.T) {
	f := newFakeGH(t)
	f.on(http.MethodGet, "/repos/octocat/hello/pulls", func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, `{"message":"Bad credentials"}`, http.StatusUnauthorized)
	})
	p := newProviderForTest(t, f)

	if _, _, err := p.FindPRForBranch(ctx(), "octocat", "hello", "feature-x"); err == nil {
		t.Fatal("expected error for 401, got nil")
	}
}

func TestFindPRForBranch_RequiresArgs(t *testing.T) {
	p := &Provider{}
	if _, _, err := p.FindPRForBranch(ctx(), "", "hello", "b"); err == nil {
		t.Fatal("expected error for empty owner")
	}
	if _, _, err := p.FindPRForBranch(ctx(), "o", "", "b"); err == nil {
		t.Fatal("expected error for empty repo")
	}
	if _, _, err := p.FindPRForBranch(ctx(), "o", "hello", ""); err == nil {
		t.Fatal("expected error for empty branch")
	}
}
