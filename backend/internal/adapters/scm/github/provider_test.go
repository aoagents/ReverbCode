package github

import (
	"context"
	"encoding/json"
	"errors"
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

func TestGHTokenSourceMemoizesAndIgnoresGithubTokenEnv(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "env-token")
	var calls atomic.Int32
	src := &GHTokenSource{Command: func(context.Context) ([]byte, error) {
		calls.Add(1)
		return []byte("gh-token\n"), nil
	}}
	tok, err := src.Token(context.Background())
	if err != nil || tok != "gh-token" {
		t.Fatalf("token=%q err=%v", tok, err)
	}
	tok, err = src.Token(context.Background())
	if err != nil || tok != "gh-token" {
		t.Fatalf("second token=%q err=%v", tok, err)
	}
	if calls.Load() != 1 {
		t.Fatalf("gh auth token calls=%d, want 1", calls.Load())
	}
}

func TestAuthFailureIsNormalized(t *testing.T) {
	src := &GHTokenSource{Command: func(context.Context) ([]byte, error) { return nil, ErrNoToken }}
	c := NewClient(ClientOptions{Token: src, RESTBase: "http://127.0.0.1:1"})
	_, err := c.DoREST(context.Background(), http.MethodGet, "/x", nil, nil, "", "test")
	var scmErr *domain.SCMError
	if !errors.As(err, &scmErr) || scmErr.Kind != domain.SCMErrorAuthFailed {
		t.Fatalf("err=%T %[1]v", err)
	}
}

func TestRotatedETagOn304UpdatesCacheOnly(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("If-None-Match"); got != `"old"` {
			t.Fatalf("If-None-Match=%q", got)
		}
		w.Header().Set("ETag", `"new"`)
		w.WriteHeader(http.StatusNotModified)
	}))
	defer ts.Close()
	ctx := context.Background()
	st := store.NewMemoryStore()
	scope := domain.SCMProviderCacheScope{Provider: domain.SCMProviderGitHub, Host: "github.com", Repo: "o/r", CredentialHash: "cred"}
	key := domain.SCMProviderCacheKey{SCMProviderCacheScope: scope, Namespace: cachePRList, Key: "open-guard"}
	if err := st.PutProviderCache(ctx, domain.SCMProviderCacheEntry{Key: key, ETag: `"old"`, Value: []byte(`[{"number":1}]`)}); err != nil {
		t.Fatal(err)
	}
	p := NewProvider(ProviderOptions{RESTBase: ts.URL, GraphQLURL: ts.URL + "/graphql", Token: StaticTokenSource("token")})
	changed, _, _, err := p.checkOpenPullListChanged(ctx, st, scope, "o", "r", time.Now())
	if err != nil || changed {
		t.Fatalf("changed=%v err=%v", changed, err)
	}
	got, ok, err := st.GetProviderCache(ctx, key)
	if err != nil || !ok {
		t.Fatalf("cache ok=%v err=%v", ok, err)
	}
	if got.ETag != `"new"` || string(got.Value) != `[{"number":1}]` {
		t.Fatalf("cache=%+v body=%s", got, string(got.Value))
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

func TestBranchDiscoveryExactFallbackAndNoNegativeCache(t *testing.T) {
	var exactCalls atomic.Int32
	var discoveryCalls atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/repos/o/r/pulls" && r.URL.Query().Get("head") == "":
			discoveryCalls.Add(1)
			_ = json.NewEncoder(w).Encode([]map[string]any{})
		case r.Method == http.MethodGet && r.URL.Path == "/repos/o/r/pulls" && r.URL.Query().Get("head") == "o:feat/old":
			exactCalls.Add(1)
			_ = json.NewEncoder(w).Encode([]map[string]any{{"number": 9, "html_url": "https://github.com/o/r/pull/9", "head": map[string]any{"ref": "feat/old"}, "base": map[string]any{"ref": "main"}}})
		case r.Method == http.MethodPost && r.URL.Path == "/graphql":
			writeGraphQLPR(t, w, 9, "feat/old", "SUCCESS", "APPROVED", nil)
		case strings.Contains(r.URL.Path, "/comments"):
			_, _ = w.Write([]byte(`[]`))
		default:
			t.Fatalf("unexpected %s %s?%s", r.Method, r.URL.Path, r.URL.RawQuery)
		}
	}))
	defer ts.Close()
	p := NewProvider(ProviderOptions{RESTBase: ts.URL, GraphQLURL: ts.URL + "/graphql", Token: StaticTokenSource("token")})
	subj := domain.SCMSubject{SessionID: "s1", ProjectID: "p1", Provider: domain.SCMProviderGitHub, Host: "github.com", Repo: "o/r", Branch: "feat/old"}
	res, err := p.ObserveSessions(context.Background(), observeReq(subj), store.NewMemoryStore())
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Subjects) != 1 || res.Subjects[0].PRNumber != 9 {
		t.Fatalf("subjects=%+v", res.Subjects)
	}
	if exactCalls.Load() != 1 || discoveryCalls.Load() != 1 {
		t.Fatalf("discovery=%d exact=%d", discoveryCalls.Load(), exactCalls.Load())
	}
}

func TestBranchDiscoveryDoesNotCacheMisses(t *testing.T) {
	var exactCalls atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/repos/o/r/pulls" && r.URL.Query().Get("head") == "":
			_ = json.NewEncoder(w).Encode([]map[string]any{})
		case r.Method == http.MethodGet && r.URL.Path == "/repos/o/r/pulls" && r.URL.Query().Get("head") == "o:feat/missing":
			exactCalls.Add(1)
			_ = json.NewEncoder(w).Encode([]map[string]any{})
		default:
			t.Fatalf("unexpected %s %s?%s", r.Method, r.URL.Path, r.URL.RawQuery)
		}
	}))
	defer ts.Close()
	p := NewProvider(ProviderOptions{RESTBase: ts.URL, GraphQLURL: ts.URL + "/graphql", Token: StaticTokenSource("token")})
	st := store.NewMemoryStore()
	subj := domain.SCMSubject{SessionID: "s1", ProjectID: "p1", Provider: domain.SCMProviderGitHub, Host: "github.com", Repo: "o/r", Branch: "feat/missing"}
	for i := 0; i < 2; i++ {
		res, err := p.ObserveSessions(context.Background(), observeReq(subj), st)
		if err != nil {
			t.Fatal(err)
		}
		if len(res.Subjects) != 1 || res.Subjects[0].PRNumber != 0 {
			t.Fatalf("subjects=%+v", res.Subjects)
		}
	}
	if exactCalls.Load() != 2 {
		t.Fatalf("exact fallback calls=%d, want 2", exactCalls.Load())
	}
}

func TestBranchDiscoverySkipsExactFallbackWhenPRListUnchanged(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/repos/o/r/pulls" && r.URL.Query().Get("head") == "":
			if got := r.Header.Get("If-None-Match"); got != `"pulls"` {
				t.Fatalf("If-None-Match=%q", got)
			}
			w.Header().Set("ETag", `"pulls"`)
			w.WriteHeader(http.StatusNotModified)
		case r.Method == http.MethodGet && r.URL.Path == "/repos/o/r/pulls" && r.URL.Query().Get("head") != "":
			t.Fatalf("exact fallback should be skipped on unchanged PR list")
		default:
			t.Fatalf("unexpected %s %s?%s", r.Method, r.URL.Path, r.URL.RawQuery)
		}
	}))
	defer ts.Close()
	ctx := context.Background()
	st := store.NewMemoryStore()
	subj := domain.SCMSubject{SessionID: "s1", ProjectID: "p1", Provider: domain.SCMProviderGitHub, Host: "github.com", Repo: "o/r", Branch: "feat/missing", CredentialHash: "cred"}
	key := domain.SCMProviderCacheKey{SCMProviderCacheScope: subj.CacheScope(), Namespace: cachePRList, Key: "open-discovery"}
	if err := st.PutProviderCache(ctx, domain.SCMProviderCacheEntry{Key: key, ETag: `"pulls"`, Value: []byte(`[]`)}); err != nil {
		t.Fatal(err)
	}
	p := NewProvider(ProviderOptions{RESTBase: ts.URL, GraphQLURL: ts.URL + "/graphql", Token: StaticTokenSource("token")})
	res, err := p.ObserveSessions(ctx, observeReq(subj), st)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Subjects) != 1 || res.Subjects[0].PRNumber != 0 {
		t.Fatalf("subjects=%+v", res.Subjects)
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
			if strings.Contains(req.Query, "reviewThreads") {
				threads := []map[string]any{
					{"id": "thread-human", "isResolved": false, "comments": map[string]any{"nodes": []map[string]any{{"id": "c1", "body": "please change", "url": "https://example/comment", "path": "main.go", "line": 12, "author": map[string]any{"login": "alice", "__typename": "User"}}}}},
					{"id": "thread-bot", "isResolved": false, "comments": map[string]any{"nodes": []map[string]any{{"id": "c2", "body": "lint", "url": "https://example/bot", "path": "lint.go", "line": 1, "author": map[string]any{"login": "review-bot", "__typename": "Bot"}}}}},
					{"id": "thread-resolved", "isResolved": true, "comments": map[string]any{"nodes": []map[string]any{{"id": "c3", "body": "done", "url": "https://example/done", "path": "done.go", "line": 1, "author": map[string]any{"login": "alice", "__typename": "User"}}}}},
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{
					"repository": map[string]any{"pullRequest": map[string]any{"reviewThreads": map[string]any{"nodes": threads}}},
					"rateLimit":  map[string]any{"limit": 5000, "remaining": 4999, "resetAt": "2026-05-28T13:00:00Z"},
				}})
				return
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

func TestCompleteGraphQLCIContextsAvoidFullCheckRunsAndJobLogs(t *testing.T) {
	var checkRunsCalls atomic.Int32
	var logCalls atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/graphql":
			payload := graphQLPRPayload(5, "feat/27", "FAILURE", "APPROVED", nil)
			withCheckContexts(payload, "FAILURE", map[string]any{
				"nodes":    []map[string]any{{"__typename": "CheckRun", "name": "lint", "status": "COMPLETED", "conclusion": "FAILURE", "detailsUrl": "https://github.com/o/r/actions/runs/10/job/20"}},
				"pageInfo": map[string]any{"hasNextPage": false},
			})
			_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"repository": map[string]any{"pr5": payload}}})
		case r.Method == http.MethodGet && r.URL.Path == "/repos/o/r/actions/jobs/20/logs":
			logCalls.Add(1)
			t.Fatalf("job logs should not be fetched from the SCM polling hot path")
		case strings.Contains(r.URL.Path, "/check-runs"):
			checkRunsCalls.Add(1)
			t.Fatalf("full check-runs should not be fetched when GraphQL contexts are complete")
		case strings.Contains(r.URL.Path, "/comments"):
			_, _ = w.Write([]byte(`[]`))
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
	if checkRunsCalls.Load() != 0 || logCalls.Load() != 0 {
		t.Fatalf("checkRuns=%d logs=%d", checkRunsCalls.Load(), logCalls.Load())
	}
	if got := res.Snapshots[0].CI.FailureLogTail; got != "" {
		t.Fatalf("failure log tail should stay empty when only GraphQL check contexts were needed: %q", got)
	}
}

func TestTruncatedGraphQLCIContextsFetchFullCheckRuns(t *testing.T) {
	var checkRunsCalls atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/graphql":
			payload := graphQLPRPayload(5, "feat/27", "FAILURE", "APPROVED", nil)
			withCheckContexts(payload, "FAILURE", map[string]any{
				"nodes":    []map[string]any{{"__typename": "CheckRun", "name": "first", "status": "COMPLETED", "conclusion": "SUCCESS", "detailsUrl": "https://checks/first"}},
				"pageInfo": map[string]any{"hasNextPage": true},
			})
			_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"repository": map[string]any{"pr5": payload}}})
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/check-runs"):
			if got := r.URL.Query().Get("per_page"); got != "100" {
				t.Fatalf("check-runs per_page=%q", got)
			}
			checkRunsCalls.Add(1)
			_, _ = w.Write([]byte(`{"check_runs":[{"name":"late-failure","status":"completed","conclusion":"failure","html_url":"https://checks/late"}]}`))
		case strings.Contains(r.URL.Path, "/comments"):
			_, _ = w.Write([]byte(`[]`))
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
	if checkRunsCalls.Load() != 1 {
		t.Fatalf("check-runs calls=%d", checkRunsCalls.Load())
	}
	if got := res.Snapshots[0].CI.Checks[0].Name; got != "late-failure" {
		t.Fatalf("check name=%q", got)
	}
}

func TestFailingCIWithoutFailedContextFetchesFullCheckRuns(t *testing.T) {
	var checkRunsCalls atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/graphql":
			payload := graphQLPRPayload(5, "feat/27", "FAILURE", "APPROVED", nil)
			withCheckContexts(payload, "FAILURE", map[string]any{
				"nodes":    []map[string]any{{"__typename": "CheckRun", "name": "green", "status": "COMPLETED", "conclusion": "SUCCESS", "detailsUrl": "https://checks/green"}},
				"pageInfo": map[string]any{"hasNextPage": false},
			})
			_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"repository": map[string]any{"pr5": payload}}})
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/check-runs"):
			if got := r.URL.Query().Get("per_page"); got != "100" {
				t.Fatalf("check-runs per_page=%q", got)
			}
			checkRunsCalls.Add(1)
			_, _ = w.Write([]byte(`{"check_runs":[{"name":"hidden-failure","status":"completed","conclusion":"failure","html_url":"https://checks/hidden"}]}`))
		case strings.Contains(r.URL.Path, "/comments"):
			_, _ = w.Write([]byte(`[]`))
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
	if checkRunsCalls.Load() != 1 || res.Snapshots[0].CI.Checks[0].Name != "hidden-failure" {
		t.Fatalf("checkRuns=%d checks=%+v", checkRunsCalls.Load(), res.Snapshots[0].CI.Checks)
	}
}

func TestETagGuardsReuseLatestSnapshotAndSkipGraphQL(t *testing.T) {
	var graphQLCalls atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/repos/o/r/pulls":
			if got := r.URL.Query().Get("per_page"); got != "1" {
				t.Fatalf("pr-list guard per_page=%q", got)
			}
			if got := r.Header.Get("If-None-Match"); got != `"pulls"` {
				t.Fatalf("pr-list If-None-Match = %q", got)
			}
			w.Header().Set("ETag", `"pulls"`)
			w.WriteHeader(http.StatusNotModified)
		case r.Method == http.MethodGet && r.URL.Path == "/repos/o/r/pulls/5":
			if got := r.Header.Get("If-None-Match"); got != `"pr"` {
				t.Fatalf("pr-state If-None-Match = %q", got)
			}
			w.Header().Set("ETag", `"pr"`)
			w.WriteHeader(http.StatusNotModified)
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/check-runs"):
			if got := r.Header.Get("If-None-Match"); got != `"checks"` {
				t.Fatalf("check guard If-None-Match = %q", got)
			}
			w.Header().Set("ETag", `"checks"`)
			w.WriteHeader(http.StatusNotModified)
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/comments"):
			if got := r.Header.Get("If-None-Match"); got != `"reviews"` {
				t.Fatalf("review If-None-Match = %q", got)
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
	snap := domain.SCMSnapshot{SessionID: "s1", Subject: subj, Freshness: domain.SCMFreshnessFresh, ObservedAt: time.Date(2026, 5, 28, 11, 0, 0, 0, time.UTC), PR: &domain.SCMPullRequest{Number: 5, URL: "https://github.com/o/r/pull/5", State: domain.PROpen, HeadSHA: "sha"}, CI: domain.SCMCI{Summary: "passing"}, Review: domain.SCMReview{UnresolvedThreads: []domain.SCMReviewThread{{ID: "thread-1", Comments: []domain.SCMReviewComment{{ID: "c1", Author: "alice"}}}}}}
	if _, _, err := st.SaveSnapshot(ctx, snap); err != nil {
		t.Fatal(err)
	}
	pullsBody := []byte(`[{"number":5,"html_url":"https://github.com/o/r/pull/5","head":{"ref":"feat/27"},"base":{"ref":"main"}}]`)
	if err := st.PutProviderCache(ctx, domain.SCMProviderCacheEntry{Key: domain.SCMProviderCacheKey{SCMProviderCacheScope: subj.CacheScope(), Namespace: cachePRList, Key: "open-guard"}, ETag: `"pulls"`, Value: pullsBody, UpdatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if err := st.PutProviderCache(ctx, domain.SCMProviderCacheEntry{Key: domain.SCMProviderCacheKey{SCMProviderCacheScope: subj.CacheScope(), Namespace: cacheCheckGuard, Key: "sha"}, ETag: `"checks"`, UpdatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if err := st.PutProviderCache(ctx, domain.SCMProviderCacheEntry{Key: domain.SCMProviderCacheKey{SCMProviderCacheScope: subj.CacheScope(), Namespace: cachePRState, Key: "5"}, ETag: `"pr"`}); err != nil {
		t.Fatal(err)
	}
	if err := st.PutProviderCache(ctx, domain.SCMProviderCacheEntry{Key: domain.SCMProviderCacheKey{SCMProviderCacheScope: subj.CacheScope(), Namespace: cacheReviews, Key: "5"}, ETag: `"reviews"`, Value: []byte(`[{"id":1}]`)}); err != nil {
		t.Fatal(err)
	}
	putCachedReviewDetails(ctx, st, subj, "none", snap.Review.UnresolvedThreads, time.Date(2026, 5, 28, 11, 59, 0, 0, time.UTC))

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
	if got := len(res.Snapshots[0].Review.UnresolvedThreads); got != 1 {
		t.Fatalf("reused review threads=%d", got)
	}
}

func TestBoundPRStateGuardDetectsTerminalPRWhenOpenListUnchanged(t *testing.T) {
	var graphQLCalls atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/repos/o/r/pulls":
			w.Header().Set("ETag", `"pulls"`)
			w.WriteHeader(http.StatusNotModified)
		case r.Method == http.MethodGet && r.URL.Path == "/repos/o/r/pulls/5":
			w.Header().Set("ETag", `"pr-closed"`)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"number":   5,
				"state":    "closed",
				"merged":   true,
				"html_url": "https://github.com/o/r/pull/5",
				"head":     map[string]any{"ref": "feat/27", "sha": "sha"},
				"base":     map[string]any{"ref": "main"},
			})
		case r.Method == http.MethodPost && r.URL.Path == "/graphql":
			graphQLCalls.Add(1)
			t.Fatalf("terminal PR state should be detected before GraphQL reuse/fetch")
		default:
			t.Fatalf("unexpected %s %s", r.Method, r.URL.Path)
		}
	}))
	defer ts.Close()
	ctx := context.Background()
	st := store.NewMemoryStore()
	subj := domain.SCMSubject{SessionID: "s1", ProjectID: "p1", Provider: domain.SCMProviderGitHub, Host: "github.com", Repo: "o/r", Branch: "feat/27", PRNumber: 5, CredentialHash: "cred"}
	snap := domain.SCMSnapshot{SessionID: "s1", Subject: subj, Freshness: domain.SCMFreshnessFresh, PR: &domain.SCMPullRequest{Number: 5, URL: "https://github.com/o/r/pull/5", State: domain.PROpen, HeadSHA: "sha"}, CI: domain.SCMCI{Summary: "passing"}}
	if _, _, err := st.SaveSnapshot(ctx, snap); err != nil {
		t.Fatal(err)
	}
	if err := st.PutProviderCache(ctx, domain.SCMProviderCacheEntry{Key: domain.SCMProviderCacheKey{SCMProviderCacheScope: subj.CacheScope(), Namespace: cachePRList, Key: "open-guard"}, ETag: `"pulls"`, Value: []byte(`[{"number":5}]`)}); err != nil {
		t.Fatal(err)
	}
	p := NewProvider(ProviderOptions{RESTBase: ts.URL, GraphQLURL: ts.URL + "/graphql", Token: StaticTokenSource("token")})
	res, err := p.ObserveSessions(ctx, observeReq(subj), st)
	if err != nil {
		t.Fatal(err)
	}
	if graphQLCalls.Load() != 0 {
		t.Fatalf("GraphQL calls=%d", graphQLCalls.Load())
	}
	if len(res.Snapshots) != 1 || res.Snapshots[0].PR == nil || res.Snapshots[0].PR.State != domain.PRMerged {
		t.Fatalf("expected merged terminal snapshot, got %+v", res.Snapshots)
	}
}

func TestReviewDetailsChecksETagEveryPollAndReusesCacheOn304(t *testing.T) {
	var graphQLCalls atomic.Int32
	var commentCalls atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/repos/o/r/pulls":
			w.Header().Set("ETag", `"pulls"`)
			w.WriteHeader(http.StatusNotModified)
		case r.Method == http.MethodGet && r.URL.Path == "/repos/o/r/pulls/5":
			w.Header().Set("ETag", `"pr"`)
			w.WriteHeader(http.StatusNotModified)
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/check-runs"):
			w.Header().Set("ETag", `"checks"`)
			w.WriteHeader(http.StatusNotModified)
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/comments"):
			commentCalls.Add(1)
			if got := r.Header.Get("If-None-Match"); got != `"reviews"` {
				t.Fatalf("review If-None-Match=%q", got)
			}
			w.Header().Set("ETag", `"reviews"`)
			w.WriteHeader(http.StatusNotModified)
		case r.Method == http.MethodPost && r.URL.Path == "/graphql":
			graphQLCalls.Add(1)
			t.Fatalf("GraphQL should be skipped")
		default:
			t.Fatalf("unexpected %s %s", r.Method, r.URL.Path)
		}
	}))
	defer ts.Close()
	ctx := context.Background()
	st := store.NewMemoryStore()
	now := time.Date(2026, 5, 28, 12, 0, 0, 0, time.UTC)
	subj := domain.SCMSubject{SessionID: "s1", ProjectID: "p1", Provider: domain.SCMProviderGitHub, Host: "github.com", Repo: "o/r", Branch: "feat/27", PRNumber: 5, CredentialHash: "cred"}
	threads := []domain.SCMReviewThread{{ID: "thread-1", Comments: []domain.SCMReviewComment{{ID: "c1", Author: "alice"}}}}
	snap := domain.SCMSnapshot{SessionID: "s1", Subject: subj, Freshness: domain.SCMFreshnessFresh, ObservedAt: now.Add(-time.Minute), PR: &domain.SCMPullRequest{Number: 5, State: domain.PROpen, HeadSHA: "sha"}, CI: domain.SCMCI{Summary: "passing"}, Review: domain.SCMReview{UnresolvedThreads: threads}}
	if _, _, err := st.SaveSnapshot(ctx, snap); err != nil {
		t.Fatal(err)
	}
	for _, entry := range []domain.SCMProviderCacheEntry{
		{Key: domain.SCMProviderCacheKey{SCMProviderCacheScope: subj.CacheScope(), Namespace: cachePRList, Key: "open-guard"}, ETag: `"pulls"`, Value: []byte(`[{"number":5}]`)},
		{Key: domain.SCMProviderCacheKey{SCMProviderCacheScope: subj.CacheScope(), Namespace: cachePRState, Key: "5"}, ETag: `"pr"`},
		{Key: domain.SCMProviderCacheKey{SCMProviderCacheScope: subj.CacheScope(), Namespace: cacheCheckGuard, Key: "sha"}, ETag: `"checks"`},
		{Key: domain.SCMProviderCacheKey{SCMProviderCacheScope: subj.CacheScope(), Namespace: cacheReviews, Key: "5"}, ETag: `"reviews"`, Value: []byte(`[{"id":1}]`)},
	} {
		if err := st.PutProviderCache(ctx, entry); err != nil {
			t.Fatal(err)
		}
	}
	putCachedReviewDetails(ctx, st, subj, "none", threads, now.Add(-time.Minute))
	p := NewProvider(ProviderOptions{RESTBase: ts.URL, GraphQLURL: ts.URL + "/graphql", Token: StaticTokenSource("token")})
	res, err := p.ObserveSessions(ctx, ports.SCMObserveRequest{Subjects: []domain.SCMSubject{subj}, Now: now}, st)
	if err != nil {
		t.Fatal(err)
	}
	if graphQLCalls.Load() != 0 || commentCalls.Load() != 1 || len(res.Snapshots) != 1 || len(res.Snapshots[0].Review.UnresolvedThreads) != 1 {
		t.Fatalf("graphQLCalls=%d commentCalls=%d snapshots=%+v", graphQLCalls.Load(), commentCalls.Load(), res.Snapshots)
	}
}

func TestReviewDetailsChecksETagAndReusesOn304(t *testing.T) {
	var commentCalls atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/repos/o/r/pulls":
			w.Header().Set("ETag", `"pulls"`)
			w.WriteHeader(http.StatusNotModified)
		case r.Method == http.MethodGet && r.URL.Path == "/repos/o/r/pulls/5":
			w.Header().Set("ETag", `"pr"`)
			w.WriteHeader(http.StatusNotModified)
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/check-runs"):
			w.Header().Set("ETag", `"checks"`)
			w.WriteHeader(http.StatusNotModified)
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/comments"):
			commentCalls.Add(1)
			if got := r.Header.Get("If-None-Match"); got != `"reviews"` {
				t.Fatalf("review If-None-Match=%q", got)
			}
			w.Header().Set("ETag", `"reviews"`)
			w.WriteHeader(http.StatusNotModified)
		case r.Method == http.MethodPost && r.URL.Path == "/graphql":
			t.Fatalf("reviewThreads GraphQL should be skipped on 304")
		default:
			t.Fatalf("unexpected %s %s", r.Method, r.URL.Path)
		}
	}))
	defer ts.Close()
	ctx := context.Background()
	st := store.NewMemoryStore()
	now := time.Date(2026, 5, 28, 12, 0, 0, 0, time.UTC)
	subj := domain.SCMSubject{SessionID: "s1", ProjectID: "p1", Provider: domain.SCMProviderGitHub, Host: "github.com", Repo: "o/r", Branch: "feat/27", PRNumber: 5, CredentialHash: "cred"}
	threads := []domain.SCMReviewThread{{ID: "thread-1", Comments: []domain.SCMReviewComment{{ID: "c1", Author: "alice"}}}}
	snap := domain.SCMSnapshot{SessionID: "s1", Subject: subj, Freshness: domain.SCMFreshnessFresh, PR: &domain.SCMPullRequest{Number: 5, State: domain.PROpen, HeadSHA: "sha"}, CI: domain.SCMCI{Summary: "passing"}, Review: domain.SCMReview{UnresolvedThreads: threads}}
	if _, _, err := st.SaveSnapshot(ctx, snap); err != nil {
		t.Fatal(err)
	}
	for _, entry := range []domain.SCMProviderCacheEntry{
		{Key: domain.SCMProviderCacheKey{SCMProviderCacheScope: subj.CacheScope(), Namespace: cachePRList, Key: "open-guard"}, ETag: `"pulls"`, Value: []byte(`[{"number":5}]`)},
		{Key: domain.SCMProviderCacheKey{SCMProviderCacheScope: subj.CacheScope(), Namespace: cachePRState, Key: "5"}, ETag: `"pr"`},
		{Key: domain.SCMProviderCacheKey{SCMProviderCacheScope: subj.CacheScope(), Namespace: cacheCheckGuard, Key: "sha"}, ETag: `"checks"`},
		{Key: domain.SCMProviderCacheKey{SCMProviderCacheScope: subj.CacheScope(), Namespace: cacheReviews, Key: "5"}, ETag: `"reviews"`, Value: []byte(`[{"id":1}]`)},
	} {
		if err := st.PutProviderCache(ctx, entry); err != nil {
			t.Fatal(err)
		}
	}
	putCachedReviewDetails(ctx, st, subj, "none", threads, now.Add(-3*time.Minute))
	p := NewProvider(ProviderOptions{RESTBase: ts.URL, GraphQLURL: ts.URL + "/graphql", Token: StaticTokenSource("token")})
	res, err := p.ObserveSessions(ctx, ports.SCMObserveRequest{Subjects: []domain.SCMSubject{subj}, Now: now}, st)
	if err != nil {
		t.Fatal(err)
	}
	if commentCalls.Load() != 1 || len(res.Snapshots[0].Review.UnresolvedThreads) != 1 {
		t.Fatalf("commentCalls=%d snapshots=%+v", commentCalls.Load(), res.Snapshots)
	}
	_, entry, ok := getCachedReviewDetails(ctx, st, subj)
	if !ok || !entry.UpdatedAt.Equal(now) {
		t.Fatalf("review details cache not touched: ok=%v entry=%+v", ok, entry)
	}
}

func TestReviewCommentsChangeClearsCachedThreadsImmediately(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/repos/o/r/pulls":
			w.Header().Set("ETag", `"pulls"`)
			w.WriteHeader(http.StatusNotModified)
		case r.Method == http.MethodGet && r.URL.Path == "/repos/o/r/pulls/5":
			w.Header().Set("ETag", `"pr"`)
			w.WriteHeader(http.StatusNotModified)
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/check-runs"):
			w.Header().Set("ETag", `"checks"`)
			w.WriteHeader(http.StatusNotModified)
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/comments"):
			if got := r.Header.Get("If-None-Match"); got != `"reviews-old"` {
				t.Fatalf("review If-None-Match=%q", got)
			}
			w.Header().Set("ETag", `"reviews-empty"`)
			_, _ = w.Write([]byte(`[]`))
		case r.Method == http.MethodPost && r.URL.Path == "/graphql":
			t.Fatalf("GraphQL should not be needed when review comments become empty")
		default:
			t.Fatalf("unexpected %s %s", r.Method, r.URL.Path)
		}
	}))
	defer ts.Close()
	ctx := context.Background()
	st := store.NewMemoryStore()
	now := time.Date(2026, 5, 28, 12, 0, 0, 0, time.UTC)
	subj := domain.SCMSubject{SessionID: "s1", ProjectID: "p1", Provider: domain.SCMProviderGitHub, Host: "github.com", Repo: "o/r", Branch: "feat/27", PRNumber: 5, CredentialHash: "cred"}
	threads := []domain.SCMReviewThread{{ID: "thread-old", Comments: []domain.SCMReviewComment{{ID: "c1", Author: "alice"}}}}
	snap := domain.SCMSnapshot{SessionID: "s1", Subject: subj, Freshness: domain.SCMFreshnessFresh, PR: &domain.SCMPullRequest{Number: 5, State: domain.PROpen, HeadSHA: "sha"}, CI: domain.SCMCI{Summary: "passing"}, Review: domain.SCMReview{UnresolvedThreads: threads}}
	if _, _, err := st.SaveSnapshot(ctx, snap); err != nil {
		t.Fatal(err)
	}
	for _, entry := range []domain.SCMProviderCacheEntry{
		{Key: domain.SCMProviderCacheKey{SCMProviderCacheScope: subj.CacheScope(), Namespace: cachePRList, Key: "open-guard"}, ETag: `"pulls"`, Value: []byte(`[{"number":5}]`)},
		{Key: domain.SCMProviderCacheKey{SCMProviderCacheScope: subj.CacheScope(), Namespace: cachePRState, Key: "5"}, ETag: `"pr"`},
		{Key: domain.SCMProviderCacheKey{SCMProviderCacheScope: subj.CacheScope(), Namespace: cacheCheckGuard, Key: "sha"}, ETag: `"checks"`},
		{Key: domain.SCMProviderCacheKey{SCMProviderCacheScope: subj.CacheScope(), Namespace: cacheReviews, Key: "5"}, ETag: `"reviews-old"`, Value: []byte(`[{"id":1}]`)},
	} {
		if err := st.PutProviderCache(ctx, entry); err != nil {
			t.Fatal(err)
		}
	}
	putCachedReviewDetails(ctx, st, subj, "none", threads, now.Add(-time.Minute))
	p := NewProvider(ProviderOptions{RESTBase: ts.URL, GraphQLURL: ts.URL + "/graphql", Token: StaticTokenSource("token")})
	res, err := p.ObserveSessions(ctx, ports.SCMObserveRequest{Subjects: []domain.SCMSubject{subj}, Now: now}, st)
	if err != nil {
		t.Fatal(err)
	}
	if got := len(res.Snapshots[0].Review.UnresolvedThreads); got != 0 {
		t.Fatalf("review threads should have cleared immediately, got %d", got)
	}
}

func TestReviewDecisionChangeBypassesReviewThrottle(t *testing.T) {
	var reviewGraphQLCalls atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/repos/o/r/pulls":
			w.Header().Set("ETag", `"new-pulls"`)
			_, _ = w.Write([]byte(`[{"number":5}]`))
		case r.Method == http.MethodPost && r.URL.Path == "/graphql":
			var req struct {
				Query string `json:"query"`
			}
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Fatal(err)
			}
			if strings.Contains(req.Query, "reviewThreads") {
				reviewGraphQLCalls.Add(1)
				threads := []map[string]any{{"id": "thread-human", "isResolved": false, "comments": map[string]any{"nodes": []map[string]any{{"id": "c1", "body": "fix", "url": "https://example/comment", "path": "main.go", "line": 7, "author": map[string]any{"login": "alice", "__typename": "User"}}}}}}
				_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"repository": map[string]any{"pullRequest": map[string]any{"reviewThreads": map[string]any{"nodes": threads}}}}})
				return
			}
			writeGraphQLPR(t, w, 5, "feat/27", "SUCCESS", "CHANGES_REQUESTED", nil)
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/comments"):
			t.Fatalf("review decision change should force GraphQL directly")
		default:
			t.Fatalf("unexpected %s %s", r.Method, r.URL.Path)
		}
	}))
	defer ts.Close()
	ctx := context.Background()
	st := store.NewMemoryStore()
	now := time.Date(2026, 5, 28, 12, 0, 0, 0, time.UTC)
	subj := domain.SCMSubject{SessionID: "s1", ProjectID: "p1", Provider: domain.SCMProviderGitHub, Host: "github.com", Repo: "o/r", Branch: "feat/27", PRNumber: 5, CredentialHash: "cred"}
	prev := domain.SCMSnapshot{SessionID: "s1", Subject: subj, Freshness: domain.SCMFreshnessFresh, PR: &domain.SCMPullRequest{Number: 5, State: domain.PROpen, HeadSHA: "sha"}, CI: domain.SCMCI{Summary: "passing"}, Review: domain.SCMReview{Decision: "none"}}
	if _, _, err := st.SaveSnapshot(ctx, prev); err != nil {
		t.Fatal(err)
	}
	key := domain.SCMProviderCacheKey{SCMProviderCacheScope: subj.CacheScope(), Namespace: cachePRList, Key: "open-guard"}
	if err := st.PutProviderCache(ctx, domain.SCMProviderCacheEntry{Key: key, ETag: `"old"`, Value: []byte(`[{"number":5}]`)}); err != nil {
		t.Fatal(err)
	}
	putCachedReviewDetails(ctx, st, subj, "none", nil, now.Add(-time.Minute))
	p := NewProvider(ProviderOptions{RESTBase: ts.URL, GraphQLURL: ts.URL + "/graphql", Token: StaticTokenSource("token")})
	res, err := p.ObserveSessions(ctx, ports.SCMObserveRequest{Subjects: []domain.SCMSubject{subj}, Now: now}, st)
	if err != nil {
		t.Fatal(err)
	}
	if reviewGraphQLCalls.Load() != 1 || len(res.Snapshots[0].Review.HumanComments) != 1 {
		t.Fatalf("reviewGraphQLCalls=%d snapshot=%+v", reviewGraphQLCalls.Load(), res.Snapshots[0].Review)
	}
}

func TestReviewThreadsFetchAllCommentsForClassification(t *testing.T) {
	var reviewGraphQLCalls atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/graphql":
			var req struct {
				Query string `json:"query"`
			}
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Fatal(err)
			}
			if strings.Contains(req.Query, "reviewThreads") {
				reviewGraphQLCalls.Add(1)
				if !strings.Contains(req.Query, "comments(first:100)") {
					t.Fatalf("review thread query should fetch all bounded comments: %s", req.Query)
				}
				threads := []map[string]any{{
					"id":         "thread-mixed",
					"isResolved": false,
					"comments": map[string]any{"nodes": []map[string]any{
						{"id": "bot-c", "body": "automated hint", "url": "https://example/bot", "path": "main.go", "line": 7, "author": map[string]any{"login": "lint-bot", "__typename": "Bot"}},
						{"id": "human-c", "body": "please fix this too", "url": "https://example/human", "path": "main.go", "line": 8, "author": map[string]any{"login": "alice", "__typename": "User"}},
					}},
				}}
				_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"repository": map[string]any{"pullRequest": map[string]any{"reviewThreads": map[string]any{"nodes": threads}}}}})
				return
			}
			writeGraphQLPR(t, w, 5, "feat/27", "SUCCESS", "CHANGES_REQUESTED", nil)
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/comments"):
			_, _ = w.Write([]byte(`[{"id":1}]`))
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
	if reviewGraphQLCalls.Load() != 1 {
		t.Fatalf("review GraphQL calls=%d", reviewGraphQLCalls.Load())
	}
	review := res.Snapshots[0].Review
	if len(review.HumanComments) != 1 || len(review.BotComments) != 0 || len(review.UnresolvedThreads[0].Comments) != 2 {
		t.Fatalf("mixed thread should be human with both comments preserved: %+v", review)
	}
}

func TestGraphQLFailureFallsBackToTerminalPRState(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/graphql":
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"message":"down"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/repos/o/r/pulls/5":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"number":   5,
				"state":    "closed",
				"merged":   true,
				"html_url": "https://github.com/o/r/pull/5",
				"head":     map[string]any{"ref": "feat/27", "sha": "sha"},
				"base":     map[string]any{"ref": "main"},
			})
		default:
			t.Fatalf("unexpected %s %s", r.Method, r.URL.Path)
		}
	}))
	defer ts.Close()
	p := NewProvider(ProviderOptions{RESTBase: ts.URL, GraphQLURL: ts.URL + "/graphql", Token: StaticTokenSource("token")})
	subj := domain.SCMSubject{SessionID: "s1", ProjectID: "p1", Provider: domain.SCMProviderGitHub, Host: "github.com", Repo: "o/r", Branch: "feat/27", PRNumber: 5}
	res, err := p.ObserveSessions(context.Background(), observeReq(subj), store.NewMemoryStore())
	if err == nil {
		t.Fatal("expected GraphQL error to be reported")
	}
	if len(res.Snapshots) != 1 || res.Snapshots[0].PR == nil || res.Snapshots[0].PR.State != domain.PRMerged {
		t.Fatalf("expected merged fallback snapshot, got %+v", res.Snapshots)
	}
}

func TestPRListGuardETagIsNotAdvancedWhenGraphQLFails(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/repos/o/r/pulls":
			if got := r.Header.Get("If-None-Match"); got != `"old"` {
				t.Fatalf("If-None-Match=%q", got)
			}
			w.Header().Set("ETag", `"new"`)
			_, _ = w.Write([]byte(`[{"number":5}]`))
		case r.Method == http.MethodPost && r.URL.Path == "/graphql":
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"message":"boom"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/repos/o/r/pulls/5":
			_ = json.NewEncoder(w).Encode(map[string]any{"number": 5, "state": "open", "html_url": "https://github.com/o/r/pull/5", "head": map[string]any{"ref": "feat/27", "sha": "sha"}, "base": map[string]any{"ref": "main"}})
		default:
			t.Fatalf("unexpected %s %s", r.Method, r.URL.Path)
		}
	}))
	defer ts.Close()
	ctx := context.Background()
	st := store.NewMemoryStore()
	subj := domain.SCMSubject{SessionID: "s1", ProjectID: "p1", Provider: domain.SCMProviderGitHub, Host: "github.com", Repo: "o/r", Branch: "feat/27", PRNumber: 5, CredentialHash: "cred"}
	snap := domain.SCMSnapshot{SessionID: "s1", Subject: subj, Freshness: domain.SCMFreshnessFresh, PR: &domain.SCMPullRequest{Number: 5, State: domain.PROpen, HeadSHA: "sha"}, CI: domain.SCMCI{Summary: "passing"}}
	if _, _, err := st.SaveSnapshot(ctx, snap); err != nil {
		t.Fatal(err)
	}
	key := domain.SCMProviderCacheKey{SCMProviderCacheScope: subj.CacheScope(), Namespace: cachePRList, Key: "open-guard"}
	if err := st.PutProviderCache(ctx, domain.SCMProviderCacheEntry{Key: key, ETag: `"old"`, Value: []byte(`[{"number":5}]`)}); err != nil {
		t.Fatal(err)
	}
	p := NewProvider(ProviderOptions{RESTBase: ts.URL, GraphQLURL: ts.URL + "/graphql", Token: StaticTokenSource("token")})
	if _, err := p.ObserveSessions(ctx, observeReq(subj), st); err == nil {
		t.Fatal("expected GraphQL error")
	}
	got, ok, err := st.GetProviderCache(ctx, key)
	if err != nil || !ok {
		t.Fatalf("cache ok=%v err=%v", ok, err)
	}
	if got.ETag != `"old"` {
		t.Fatalf("etag advanced on failed GraphQL: %q", got.ETag)
	}
}

func TestReviewGuardETagIsNotAdvancedWhenReviewThreadsFail(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/repos/o/r/pulls":
			w.Header().Set("ETag", `"pulls"`)
			w.WriteHeader(http.StatusNotModified)
		case r.Method == http.MethodGet && r.URL.Path == "/repos/o/r/pulls/5":
			w.Header().Set("ETag", `"pr"`)
			w.WriteHeader(http.StatusNotModified)
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/check-runs"):
			w.Header().Set("ETag", `"checks"`)
			w.WriteHeader(http.StatusNotModified)
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/comments"):
			w.Header().Set("ETag", `"reviews-new"`)
			_, _ = w.Write([]byte(`[{"id":1}]`))
		case r.Method == http.MethodPost && r.URL.Path == "/graphql":
			var req struct {
				Query string `json:"query"`
			}
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Fatal(err)
			}
			if strings.Contains(req.Query, "reviewThreads") {
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = w.Write([]byte(`{"message":"review down"}`))
				return
			}
			writeGraphQLPR(t, w, 5, "feat/27", "SUCCESS", "APPROVED", nil)
		default:
			t.Fatalf("unexpected %s %s", r.Method, r.URL.Path)
		}
	}))
	defer ts.Close()
	ctx := context.Background()
	st := store.NewMemoryStore()
	subj := domain.SCMSubject{SessionID: "s1", ProjectID: "p1", Provider: domain.SCMProviderGitHub, Host: "github.com", Repo: "o/r", Branch: "feat/27", PRNumber: 5, CredentialHash: "cred"}
	snap := domain.SCMSnapshot{SessionID: "s1", Subject: subj, Freshness: domain.SCMFreshnessFresh, PR: &domain.SCMPullRequest{Number: 5, State: domain.PROpen, HeadSHA: "sha"}, CI: domain.SCMCI{Summary: "passing"}, Review: domain.SCMReview{UnresolvedThreads: []domain.SCMReviewThread{{ID: "old"}}}}
	if _, _, err := st.SaveSnapshot(ctx, snap); err != nil {
		t.Fatal(err)
	}
	for _, entry := range []domain.SCMProviderCacheEntry{
		{Key: domain.SCMProviderCacheKey{SCMProviderCacheScope: subj.CacheScope(), Namespace: cachePRList, Key: "open-guard"}, ETag: `"pulls"`, Value: []byte(`[{"number":5}]`)},
		{Key: domain.SCMProviderCacheKey{SCMProviderCacheScope: subj.CacheScope(), Namespace: cachePRState, Key: "5"}, ETag: `"pr"`},
		{Key: domain.SCMProviderCacheKey{SCMProviderCacheScope: subj.CacheScope(), Namespace: cacheCheckGuard, Key: "sha"}, ETag: `"checks"`},
		{Key: domain.SCMProviderCacheKey{SCMProviderCacheScope: subj.CacheScope(), Namespace: cacheReviews, Key: "5"}, ETag: `"reviews-old"`, Value: []byte(`[{"id":0}]`)},
		{Key: domain.SCMProviderCacheKey{SCMProviderCacheScope: subj.CacheScope(), Namespace: cacheReviewDetails, Key: "5"}, Value: []byte(`{"threads":[{"id":"old"}]}`), UpdatedAt: time.Date(2026, 5, 28, 11, 0, 0, 0, time.UTC)},
	} {
		if err := st.PutProviderCache(ctx, entry); err != nil {
			t.Fatal(err)
		}
	}
	p := NewProvider(ProviderOptions{RESTBase: ts.URL, GraphQLURL: ts.URL + "/graphql", Token: StaticTokenSource("token")})
	res, err := p.ObserveSessions(ctx, observeReq(subj), st)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Snapshots) != 1 || len(res.Snapshots[0].Review.UnresolvedThreads) != 1 || res.Snapshots[0].Review.UnresolvedThreads[0].ID != "old" {
		t.Fatalf("expected previous review threads, got %+v", res.Snapshots)
	}
	key := domain.SCMProviderCacheKey{SCMProviderCacheScope: subj.CacheScope(), Namespace: cacheReviews, Key: "5"}
	got, ok, err := st.GetProviderCache(ctx, key)
	if err != nil || !ok {
		t.Fatalf("cache ok=%v err=%v", ok, err)
	}
	if got.ETag != `"reviews-old"` {
		t.Fatalf("review etag advanced on failed reviewThreads: %q", got.ETag)
	}
}

func TestDiscoveryParseFailureDoesNotAdvanceCache(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/repos/o/r/pulls" {
			t.Fatalf("unexpected %s %s", r.Method, r.URL.Path)
		}
		w.Header().Set("ETag", `"new"`)
		_, _ = w.Write([]byte(`not-json`))
	}))
	defer ts.Close()
	ctx := context.Background()
	st := store.NewMemoryStore()
	scope := domain.SCMProviderCacheScope{Provider: domain.SCMProviderGitHub, Host: "github.com", Repo: "o/r", CredentialHash: "cred"}
	key := domain.SCMProviderCacheKey{SCMProviderCacheScope: scope, Namespace: cachePRList, Key: "open-discovery"}
	if err := st.PutProviderCache(ctx, domain.SCMProviderCacheEntry{Key: key, ETag: `"old"`, Value: []byte(`[]`)}); err != nil {
		t.Fatal(err)
	}
	p := NewProvider(ProviderOptions{RESTBase: ts.URL, GraphQLURL: ts.URL + "/graphql", Token: StaticTokenSource("token")})
	subj := domain.SCMSubject{SessionID: "s1", ProjectID: "p1", Provider: domain.SCMProviderGitHub, Host: "github.com", Repo: "o/r", Branch: "feat/27", CredentialHash: "cred"}
	if _, err := p.ObserveSessions(ctx, observeReq(subj), st); err == nil {
		t.Fatal("expected parse error")
	}
	got, ok, err := st.GetProviderCache(ctx, key)
	if err != nil || !ok {
		t.Fatalf("cache ok=%v err=%v", ok, err)
	}
	if got.ETag != `"old"` {
		t.Fatalf("etag advanced on parse failure: %q", got.ETag)
	}
}

func TestPollStateConsecutiveFailuresIncrementAndReset(t *testing.T) {
	var fail atomic.Bool
	fail.Store(true)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/graphql" && fail.Load():
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"message":"down"}`))
			return
		case r.Method == http.MethodGet && r.URL.Path == "/repos/o/r/pulls/5" && fail.Load():
			_ = json.NewEncoder(w).Encode(map[string]any{"number": 5, "state": "open", "html_url": "https://github.com/o/r/pull/5", "head": map[string]any{"ref": "feat/27", "sha": "sha"}, "base": map[string]any{"ref": "main"}})
		case r.Method == http.MethodPost && r.URL.Path == "/graphql":
			writeGraphQLPR(t, w, 5, "feat/27", "SUCCESS", "APPROVED", nil)
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/comments"):
			_, _ = w.Write([]byte(`[]`))
		default:
			t.Fatalf("unexpected %s %s", r.Method, r.URL.Path)
		}
	}))
	defer ts.Close()
	ctx := context.Background()
	st := store.NewMemoryStore()
	p := NewProvider(ProviderOptions{RESTBase: ts.URL, GraphQLURL: ts.URL + "/graphql", Token: StaticTokenSource("token")})
	subj := domain.SCMSubject{SessionID: "s1", ProjectID: "p1", Provider: domain.SCMProviderGitHub, Host: "github.com", Repo: "o/r", Branch: "feat/27", PRNumber: 5}
	for want := 1; want <= 2; want++ {
		res, err := p.ObserveSessions(ctx, observeReq(subj), st)
		if err == nil {
			t.Fatal("expected failure")
		}
		if got := res.PollStates[0].ConsecutiveFail; got != want {
			t.Fatalf("failure count=%d want=%d", got, want)
		}
		if err := st.PutPollState(ctx, res.PollStates[0]); err != nil {
			t.Fatal(err)
		}
	}
	fail.Store(false)
	res, err := p.ObserveSessions(ctx, observeReq(subj), st)
	if err != nil {
		t.Fatal(err)
	}
	if got := res.PollStates[0].ConsecutiveFail; got != 0 {
		t.Fatalf("failure count after success=%d", got)
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

func TestCommandCacheInvalidationsAreGitHubSpecific(t *testing.T) {
	subj := domain.SCMSubject{SessionID: "s1", ProjectID: "p1", Provider: domain.SCMProviderGitHub, Host: "github.com", Repo: "o/r", Branch: "feat/27", PRNumber: 7}
	prefixes := NewProvider(ProviderOptions{Token: StaticTokenSource("token")}).
		CacheInvalidationPrefixes(subj, ports.SCMCommandMerge)
	got := map[string]bool{}
	for _, prefix := range prefixes {
		got[prefix.Namespace] = true
		if prefix.Provider != domain.SCMProviderGitHub || prefix.Repo != "o/r" {
			t.Fatalf("bad prefix scope: %+v", prefix)
		}
	}
	for _, namespace := range []string{cachePRList, cacheBranchMap, cachePRState, cacheChecks, cacheCheckGuard, cacheReviews, cacheReviewDetails} {
		if !got[namespace] {
			t.Fatalf("missing invalidation namespace %q in %+v", namespace, prefixes)
		}
	}
	if got := NewProvider(ProviderOptions{Token: StaticTokenSource("token")}).
		CacheInvalidationPrefixes(subj, ports.SCMCommandCheckout); got != nil {
		t.Fatalf("checkout should not invalidate github caches: %+v", got)
	}
}

func TestMergeabilityIsConservativeForGitHubBlockers(t *testing.T) {
	cases := []struct {
		name       string
		raw        string
		mergeState string
		draft      bool
		blocker    string
	}{
		{name: "unknown", raw: "UNKNOWN", mergeState: "UNKNOWN", blocker: "merge status unknown (GitHub is computing)"},
		{name: "empty", raw: "", mergeState: "", blocker: "merge status unknown (GitHub is computing)"},
		{name: "blocked", raw: "MERGEABLE", mergeState: "BLOCKED", blocker: "merge blocked by branch protection"},
		{name: "unstable", raw: "MERGEABLE", mergeState: "UNSTABLE", blocker: "required checks are failing"},
		{name: "behind", raw: "MERGEABLE", mergeState: "BEHIND", blocker: "branch is behind base"},
		{name: "conflict", raw: "CONFLICTING", mergeState: "DIRTY", blocker: "merge conflicts"},
		{name: "draft", raw: "MERGEABLE", mergeState: "CLEAN", draft: true, blocker: "PR is still a draft"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pr := map[string]any{"mergeable": tc.raw, "mergeStateStatus": tc.mergeState, "isDraft": tc.draft}
			got := mergeabilityFromGraphQL(pr, domain.SCMCI{Summary: "passing"}, domain.SCMReview{Decision: "approved"})
			if got.Mergeable {
				t.Fatalf("expected not mergeable: %+v", got)
			}
			if !containsString(got.Blockers, tc.blocker) {
				t.Fatalf("missing blocker %q in %+v", tc.blocker, got.Blockers)
			}
		})
	}
	got := mergeabilityFromGraphQL(map[string]any{"mergeable": "MERGEABLE", "mergeStateStatus": "CLEAN"}, domain.SCMCI{Summary: "passing"}, domain.SCMReview{Decision: "approved"})
	if !got.Mergeable || len(got.Blockers) != 0 {
		t.Fatalf("expected clean mergeable: %+v", got)
	}
}

func TestFinalizeMergeabilityRebuildsBlockersAfterRESTCheckRuns(t *testing.T) {
	snap := domain.SCMSnapshot{
		PR:           &domain.SCMPullRequest{Number: 5, State: domain.PROpen},
		CI:           domain.SCMCI{Summary: "failing", Checks: []domain.SCMCheck{{Name: "test", Status: "completed", Conclusion: "failure"}}},
		Review:       domain.SCMReview{Decision: "approved"},
		Mergeability: domain.SCMMergeability{RawState: "MERGEABLE", MergeState: "CLEAN", NoConflicts: true, CIPassing: true, Approved: true, Mergeable: true},
	}
	finalizeMergeability(&snap)
	if snap.Mergeability.Mergeable || snap.Mergeability.CIPassing || !containsString(snap.Mergeability.Blockers, "CI failing") {
		t.Fatalf("mergeability was not rebuilt from final CI state: %+v", snap.Mergeability)
	}
}

func TestSkippedNeutralAndStaleChecksAreExplicitlyNonFailing(t *testing.T) {
	for _, summary := range []string{"skipped", "neutral", "stale"} {
		if got := domain.NormalizeSCMCI(summary); got != "none" {
			t.Fatalf("NormalizeSCMCI(%q)=%q, want none", summary, got)
		}
	}
	checks := []domain.SCMCheck{
		{Name: "skipped", Status: "completed", Conclusion: "skipped"},
		{Name: "neutral", Status: "completed", Conclusion: "neutral"},
		{Name: "stale", Status: "completed", Conclusion: "stale"},
	}
	if got := summarizeChecks(checks); got != "none" {
		t.Fatalf("summarizeChecks=%q, want none", got)
	}
	for _, check := range checks {
		if failedCheck(check) {
			t.Fatalf("check should not fail: %+v", check)
		}
	}
}

func TestBotAuthorDetectionDoesNotUseLooseSubstring(t *testing.T) {
	for _, login := range []string{"robothon", "bigbob", "lambot123"} {
		if isBotAuthor(login, "User") {
			t.Fatalf("%q should not be classified as a bot", login)
		}
	}
	for _, login := range []string{"dependabot[bot]", "review-bot", "github-actions"} {
		if !isBotAuthor(login, "User") {
			t.Fatalf("%q should be classified as a bot", login)
		}
	}
	if !isBotAuthor("anything", "Bot") {
		t.Fatal("GitHub Bot typename should classify as bot")
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

func withCheckContexts(payload map[string]any, state string, contexts map[string]any) {
	payload["commits"] = map[string]any{"nodes": []map[string]any{{"commit": map[string]any{"statusCheckRollup": map[string]any{"state": state, "contexts": contexts}}}}}
}

func numberedLines(n int) string {
	var b strings.Builder
	for i := 1; i <= n; i++ {
		fmt.Fprintf(&b, "line-%02d\n", i)
	}
	return b.String()
}
