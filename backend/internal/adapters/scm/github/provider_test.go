package github

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/scm/store"
)

func TestRESTETag200And304(t *testing.T) {
	var calls atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer token" {
			t.Fatalf("auth header %q", got)
		}
		w.Header().Set("ETag", `"v1"`)
		if calls.Add(1) == 2 {
			if got := r.Header.Get("If-None-Match"); got != `"v1"` {
				t.Fatalf("If-None-Match %q", got)
			}
			w.WriteHeader(http.StatusNotModified)
			return
		}
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer ts.Close()
	c := NewClient(ClientOptions{RESTBase: ts.URL, GraphQLURL: ts.URL + "/graphql", Token: StaticTokenSource("token")})
	ctx := context.Background()
	resp, err := c.DoREST(ctx, http.MethodGet, "/x", nil, nil, "", "test")
	if err != nil || resp.NotModified || resp.ETag != `"v1"` {
		t.Fatalf("first resp=%+v err=%v", resp, err)
	}
	resp, err = c.DoREST(ctx, http.MethodGet, "/x", nil, nil, resp.ETag, "test")
	if err != nil || !resp.NotModified {
		t.Fatalf("second resp=%+v err=%v", resp, err)
	}
}

func TestBranchDiscoveryCachesPositiveMapping(t *testing.T) {
	var pullListCalls atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/repos/o/r/pulls":
			pullListCalls.Add(1)
			w.Header().Set("ETag", `"pulls"`)
			_ = json.NewEncoder(w).Encode([]map[string]any{{"number": 3, "html_url": "https://github.com/o/r/pull/3", "head": map[string]any{"ref": "feat/27"}, "base": map[string]any{"ref": "main"}}})
		case r.Method == http.MethodPost && r.URL.Path == "/graphql":
			writeGraphQLPR(t, w, 3, "feat/27", "SUCCESS", "APPROVED", nil)
		case strings.Contains(r.URL.Path, "/check-runs"):
			_, _ = w.Write([]byte(`{"check_runs":[]}`))
		case strings.Contains(r.URL.Path, "/comments"):
			_, _ = w.Write([]byte(`[]`))
		default:
			t.Fatalf("unexpected %s %s", r.Method, r.URL.Path)
		}
	}))
	defer ts.Close()
	st := store.NewMemoryStore()
	p := NewProvider(ProviderOptions{RESTBase: ts.URL, GraphQLURL: ts.URL + "/graphql", Token: StaticTokenSource("token")})
	subj := domain.SCMSubject{SessionID: "s1", ProjectID: "p1", Provider: domain.SCMProviderGitHub, Host: "github.com", Repo: "o/r", Branch: "feat/27"}
	res, err := p.ObserveSessions(context.Background(), observeReq(subj), st)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Subjects) != 1 || res.Subjects[0].PRNumber != 3 {
		t.Fatalf("discovered subjects=%+v", res.Subjects)
	}
	res, err = p.ObserveSessions(context.Background(), observeReq(subj), st)
	if err != nil {
		t.Fatal(err)
	}
	if pullListCalls.Load() != 1 {
		t.Fatalf("positive branch mapping did not avoid rediscovery; calls=%d", pullListCalls.Load())
	}
}

func TestGraphQLBatchNormalizationReviewAndMergeability(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/graphql":
			var req struct {
				Query string `json:"query"`
			}
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Fatal(err)
			}
			if strings.Contains(req.Query, "reviewThreads") || strings.Contains(req.Query, "comments(first") {
				t.Fatalf("main batch query should not fetch review threads/comments: %s", req.Query)
			}
			if !strings.Contains(req.Query, "contexts(first:20)") {
				t.Fatalf("main batch query should request contexts(first:20): %s", req.Query)
			}
			writeGraphQLPR(t, w, 5, "feat/27", "SUCCESS", "APPROVED", nil)
		case strings.Contains(r.URL.Path, "/check-runs"):
			_, _ = w.Write([]byte(`{"check_runs":[{"name":"test","status":"completed","conclusion":"success","html_url":"https://checks"}]}`))
		case strings.Contains(r.URL.Path, "/comments"):
			_, _ = w.Write([]byte(`[{"id":1,"body":"please change","html_url":"https://example/comment","path":"main.go","line":12,"pull_request_review_id":10,"user":{"login":"alice","type":"User"}},{"id":2,"body":"lint","html_url":"https://example/bot","path":"lint.go","line":1,"pull_request_review_id":11,"user":{"login":"review-bot","type":"Bot"}}]`))
		default:
			t.Fatalf("unexpected %s %s", r.Method, r.URL.Path)
		}
	}))
	defer ts.Close()
	p := NewProvider(ProviderOptions{RESTBase: ts.URL, GraphQLURL: ts.URL + "/graphql", Token: StaticTokenSource("token")})
	subj := domain.SCMSubject{SessionID: "s1", ProjectID: "p1", Provider: domain.SCMProviderGitHub, Host: "github.com", Repo: "o/r", Branch: "feat/27", PRNumber: 5}
	res, err := p.ObserveSessions(context.Background(), observeReq(subj), store.NewMemoryStore())
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Snapshots) != 1 {
		t.Fatalf("snapshots=%d", len(res.Snapshots))
	}
	s := res.Snapshots[0]
	if s.PR == nil || s.PR.State != domain.PROpen || s.CI.Summary != "passing" {
		t.Fatalf("bad pr/ci snapshot=%+v", s)
	}
	if len(s.Review.HumanComments) != 1 || len(s.Review.BotComments) != 1 {
		t.Fatalf("review split human=%d bot=%d", len(s.Review.HumanComments), len(s.Review.BotComments))
	}
	if !s.Mergeability.Mergeable {
		t.Fatalf("expected mergeable: %+v", s.Mergeability)
	}
}

func TestETagGuardsReuseLatestSnapshotAndSkipGraphQL(t *testing.T) {
	var graphQLCalls atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/repos/o/r/pulls":
			if got := r.Header.Get("If-None-Match"); got != `"pulls"` {
				t.Fatalf("pr-list If-None-Match = %q", got)
			}
			w.Header().Set("ETag", `"pulls"`)
			w.WriteHeader(http.StatusNotModified)
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/check-runs"):
			if got := r.Header.Get("If-None-Match"); got != `"checks"` {
				t.Fatalf("check guard If-None-Match = %q", got)
			}
			w.Header().Set("ETag", `"checks"`)
			w.WriteHeader(http.StatusNotModified)
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/comments"):
			if got := r.Header.Get("If-None-Match"); got != `"reviews"` {
				t.Fatalf("review guard If-None-Match = %q", got)
			}
			w.Header().Set("ETag", `"reviews"`)
			w.WriteHeader(http.StatusNotModified)
		case r.Method == http.MethodPost && r.URL.Path == "/graphql":
			graphQLCalls.Add(1)
			w.WriteHeader(http.StatusInternalServerError)
		default:
			t.Fatalf("unexpected %s %s", r.Method, r.URL.Path)
		}
	}))
	defer ts.Close()

	ctx := context.Background()
	st := store.NewMemoryStore()
	subj := domain.SCMSubject{SessionID: "s1", ProjectID: "p1", Provider: domain.SCMProviderGitHub, Host: "github.com", Repo: "o/r", Branch: "feat/27", PRNumber: 5, CredentialHash: "cred"}
	snap := domain.SCMSnapshot{SessionID: "s1", Subject: subj, Freshness: domain.SCMFreshnessFresh, ObservedAt: time.Date(2026, 5, 28, 11, 0, 0, 0, time.UTC), PR: &domain.SCMPullRequest{Number: 5, URL: "https://github.com/o/r/pull/5", State: domain.PROpen, HeadSHA: "sha"}, CI: domain.SCMCI{Summary: "passing"}}
	if _, _, err := st.SaveSnapshot(ctx, snap); err != nil {
		t.Fatal(err)
	}
	pullsBody := []byte(`[{"number":5,"html_url":"https://github.com/o/r/pull/5","head":{"ref":"feat/27"},"base":{"ref":"main"}}]`)
	if err := st.PutProviderCache(ctx, domain.SCMProviderCacheEntry{Key: domain.SCMProviderCacheKey{SCMProviderCacheScope: subj.CacheScope(), Namespace: cachePRList, Key: "open"}, ETag: `"pulls"`, Value: pullsBody, UpdatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if err := st.PutProviderCache(ctx, domain.SCMProviderCacheEntry{Key: domain.SCMProviderCacheKey{SCMProviderCacheScope: subj.CacheScope(), Namespace: cacheCheckGuard, Key: "sha"}, ETag: `"checks"`, UpdatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if err := st.PutProviderCache(ctx, domain.SCMProviderCacheEntry{Key: domain.SCMProviderCacheKey{SCMProviderCacheScope: subj.CacheScope(), Namespace: cacheReviews, Key: "5"}, ETag: `"reviews"`, Value: []byte(`[]`), UpdatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}

	p := NewProvider(ProviderOptions{RESTBase: ts.URL, GraphQLURL: ts.URL + "/graphql", Token: StaticTokenSource("token")})
	res, err := p.ObserveSessions(ctx, observeReq(subj), st)
	if err != nil {
		t.Fatal(err)
	}
	if graphQLCalls.Load() != 0 {
		t.Fatalf("GraphQL should have been skipped, calls=%d", graphQLCalls.Load())
	}
	if len(res.Snapshots) != 1 || res.Snapshots[0].Freshness != domain.SCMFreshnessUnchanged {
		t.Fatalf("expected reused unchanged snapshot, got %+v", res.Snapshots)
	}
}

func TestGraphQLBatchIsCappedAtTwentyFivePRs(t *testing.T) {
	var graphQLCalls atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/graphql":
			graphQLCalls.Add(1)
			var req struct {
				Query string `json:"query"`
			}
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Fatal(err)
			}
			if n := strings.Count(req.Query, "pullRequest(number:"); n > maxGraphQLBatchSize {
				t.Fatalf("batch contained %d PRs, want <= %d", n, maxGraphQLBatchSize)
			}
			repo := map[string]any{}
			for i := 1; i <= 26; i++ {
				alias := fmt.Sprintf("pr%d", i)
				if strings.Contains(req.Query, alias+": pullRequest") {
					repo[alias] = graphQLPRPayload(i, fmt.Sprintf("feat/%d", i), "SUCCESS", "APPROVED", nil)
				}
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"repository": repo, "rateLimit": map[string]any{"limit": 5000, "remaining": 4999, "resetAt": "2026-05-28T13:00:00Z"}}})
		case strings.Contains(r.URL.Path, "/comments"):
			_, _ = w.Write([]byte(`[]`))
		default:
			t.Fatalf("unexpected %s %s", r.Method, r.URL.Path)
		}
	}))
	defer ts.Close()
	p := NewProvider(ProviderOptions{RESTBase: ts.URL, GraphQLURL: ts.URL + "/graphql", Token: StaticTokenSource("token")})
	subjects := make([]domain.SCMSubject, 0, 26)
	for i := 1; i <= 26; i++ {
		subjects = append(subjects, domain.SCMSubject{SessionID: domain.SessionID(fmt.Sprintf("s%d", i)), ProjectID: "p1", Provider: domain.SCMProviderGitHub, Host: "github.com", Repo: "o/r", Branch: fmt.Sprintf("feat/%d", i), PRNumber: i})
	}
	res, err := p.ObserveSessions(context.Background(), ports.SCMObserveRequest{Subjects: subjects, Now: time.Date(2026, 5, 28, 12, 0, 0, 0, time.UTC)}, store.NewMemoryStore())
	if err != nil {
		t.Fatal(err)
	}
	if graphQLCalls.Load() != 2 {
		t.Fatalf("GraphQL calls=%d, want 2", graphQLCalls.Load())
	}
	if len(res.Snapshots) != 26 {
		t.Fatalf("snapshots=%d", len(res.Snapshots))
	}
}

func observeReq(subj domain.SCMSubject) ports.SCMObserveRequest {
	return ports.SCMObserveRequest{Subjects: []domain.SCMSubject{subj}, Now: time.Date(2026, 5, 28, 12, 0, 0, 0, time.UTC)}
}

func writeGraphQLPR(t *testing.T, w http.ResponseWriter, number int, branch, ciState, reviewDecision string, threads []map[string]any) {
	t.Helper()
	alias := "pr" + strconv.Itoa(number)
	resp := map[string]any{"data": map[string]any{"repository": map[string]any{alias: graphQLPRPayload(number, branch, ciState, reviewDecision, threads)}, "rateLimit": map[string]any{"limit": 5000, "remaining": 4999, "resetAt": "2026-05-28T13:00:00Z"}}}
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		t.Fatal(err)
	}
}

func graphQLPRPayload(number int, branch, ciState, reviewDecision string, threads []map[string]any) map[string]any {
	if threads == nil {
		threads = []map[string]any{}
	}
	contexts := map[string]any{
		"nodes":    []map[string]any{{"__typename": "CheckRun", "name": "test", "status": "COMPLETED", "conclusion": "SUCCESS", "detailsUrl": "https://checks"}},
		"pageInfo": map[string]any{"hasNextPage": false},
	}
	rollup := map[string]any{"state": ciState, "contexts": contexts}
	commit := map[string]any{"statusCheckRollup": rollup}
	return map[string]any{
		"number":           number,
		"title":            "PR",
		"url":              "https://github.com/o/r/pull/" + strconv.Itoa(number),
		"state":            "OPEN",
		"isDraft":          false,
		"merged":           false,
		"closed":           false,
		"headRefName":      branch,
		"baseRefName":      "main",
		"headRefOid":       "sha",
		"additions":        1,
		"deletions":        2,
		"mergeable":        "MERGEABLE",
		"reviewDecision":   reviewDecision,
		"mergeStateStatus": "CLEAN",
		"commits":          map[string]any{"nodes": []map[string]any{{"commit": commit}}},
		"reviewThreads":    map[string]any{"nodes": threads},
	}
}
