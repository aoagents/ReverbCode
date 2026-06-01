package github

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
)

func TestFindOpenPRForBranchSingleMatch(t *testing.T) {
	fake := newFakeGH(t)
	p := newProviderForTest(t, fake)
	fake.on(http.MethodGet, "/repos/acme/repo/pulls", func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("head"); got != "acme:feat/x" {
			t.Errorf("head query = %q, want acme:feat/x", got)
		}
		if got := r.URL.Query().Get("state"); got != "open" {
			t.Errorf("state query = %q, want open", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode([]map[string]any{
			{"number": 7, "state": "open", "html_url": "https://github.com/acme/repo/pull/7", "updated_at": "2026-05-01T10:00:00Z"},
		})
	})

	url, err := p.FindOpenPRForBranch(ctx(), "acme", "repo", "feat/x")
	if err != nil {
		t.Fatalf("FindOpenPRForBranch: %v", err)
	}
	if url != "https://github.com/acme/repo/pull/7" {
		t.Fatalf("url = %q", url)
	}
}

func TestFindOpenPRForBranchNoMatch(t *testing.T) {
	fake := newFakeGH(t)
	p := newProviderForTest(t, fake)
	fake.on(http.MethodGet, "/repos/acme/repo/pulls", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte("[]"))
	})
	url, err := p.FindOpenPRForBranch(ctx(), "acme", "repo", "feat/x")
	if err != nil {
		t.Fatalf("FindOpenPRForBranch: %v", err)
	}
	if url != "" {
		t.Fatalf("url = %q, want empty", url)
	}
}

func TestFindOpenPRForBranchMultiplePicksMostRecent(t *testing.T) {
	fake := newFakeGH(t)
	p := newProviderForTest(t, fake)
	fake.on(http.MethodGet, "/repos/acme/repo/pulls", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode([]map[string]any{
			{"number": 1, "state": "open", "html_url": "https://github.com/acme/repo/pull/1", "updated_at": "2026-01-01T00:00:00Z"},
			{"number": 9, "state": "open", "html_url": "https://github.com/acme/repo/pull/9", "updated_at": "2026-05-01T00:00:00Z"},
			{"number": 4, "state": "open", "html_url": "https://github.com/acme/repo/pull/4", "updated_at": "2026-03-01T00:00:00Z"},
		})
	})
	url, err := p.FindOpenPRForBranch(ctx(), "acme", "repo", "feat/x")
	if err != nil {
		t.Fatalf("FindOpenPRForBranch: %v", err)
	}
	if url != "https://github.com/acme/repo/pull/9" {
		t.Fatalf("url = %q, want pull/9", url)
	}
}

func TestFindOpenPRForBranchEmptyInputsError(t *testing.T) {
	fake := newFakeGH(t)
	p := newProviderForTest(t, fake)
	for _, tc := range []struct{ owner, repo, branch string }{
		{"", "repo", "b"},
		{"o", "", "b"},
		{"o", "r", ""},
	} {
		_, err := p.FindOpenPRForBranch(ctx(), tc.owner, tc.repo, tc.branch)
		if err == nil {
			t.Errorf("expected error for empty input %+v", tc)
		}
	}
}

func TestFindOpenPRForBranchRateLimited(t *testing.T) {
	fake := newFakeGH(t)
	p := newProviderForTest(t, fake)
	fake.on(http.MethodGet, "/repos/acme/repo/pulls", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-RateLimit-Remaining", "0")
		w.Header().Set("X-RateLimit-Reset", "1700000000")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"message":"API rate limit exceeded"}`))
	})
	_, err := p.FindOpenPRForBranch(ctx(), "acme", "repo", "feat/x")
	if !errors.Is(err, ErrRateLimited) {
		t.Fatalf("err = %v, want ErrRateLimited", err)
	}
}

func TestFindOpenPRForBranchAuthFailed(t *testing.T) {
	fake := newFakeGH(t)
	p := newProviderForTest(t, fake)
	fake.on(http.MethodGet, "/repos/acme/repo/pulls", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"message":"Bad credentials"}`))
	})
	_, err := p.FindOpenPRForBranch(ctx(), "acme", "repo", "feat/x")
	if !errors.Is(err, ErrAuthFailed) {
		t.Fatalf("err = %v, want ErrAuthFailed", err)
	}
}

func TestFindOpenPRForBranchSynthesizesURLWhenHTMLEmpty(t *testing.T) {
	fake := newFakeGH(t)
	p := newProviderForTest(t, fake)
	fake.on(http.MethodGet, "/repos/acme/repo/pulls", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode([]map[string]any{
			{"number": 42, "state": "open", "updated_at": "2026-05-01T10:00:00Z"},
		})
	})
	url, err := p.FindOpenPRForBranch(ctx(), "acme", "repo", "feat/x")
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if !strings.HasSuffix(url, "/acme/repo/pull/42") {
		t.Fatalf("url = %q, want suffix /acme/repo/pull/42", url)
	}
}
