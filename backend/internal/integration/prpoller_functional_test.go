package integration

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/scm/github"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/observe/prpoller"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// staticRepos resolves every project to one fixed github owner/repo, standing
// in for the daemon's git-remote resolver so the functional test does not need
// a repo on disk. Repo resolution itself is unit-tested in the daemon package.
type staticRepos struct{ owner, repo string }

func (s staticRepos) RepoIdent(context.Context, domain.ProjectID) (string, string, error) {
	return s.owner, s.repo, nil
}

// fakeGitHub serves the exact REST + GraphQL routes the PR engine touches for a
// single CI-failing PR (octocat/hello#42 on branch feat/x), so the real
// github.Provider exercises its real network code against it.
func fakeGitHub(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()

	// FindPRForBranch: list open PRs for the branch head.
	mux.HandleFunc("/repos/octocat/hello/pulls", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode([]map[string]any{
			{"html_url": "https://github.com/octocat/hello/pull/42", "url": "https://api.github.com/repos/octocat/hello/pulls/42"},
		})
	})

	// Observe: REST pull detail.
	mux.HandleFunc("/repos/octocat/hello/pulls/42", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("ETag", `W/"v1"`)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"number": 42, "title": "Found a bug", "state": "open",
			"draft": false, "merged": false, "merged_at": nil,
			"html_url":           "https://github.com/octocat/hello/pull/42",
			"head":               map[string]any{"ref": "feat/x", "sha": "deadbeef"},
			"base":               map[string]any{"ref": "main"},
			"mergeable":          true,
			"mergeable_state":    "blocked",
			"merge_state_status": "BLOCKED",
		})
	})

	// Observe: GraphQL rollup with a FAILED check carrying a databaseId so the
	// provider fetches the job log tail.
	mux.HandleFunc("/graphql", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": map[string]any{"repository": map[string]any{"pullRequest": map[string]any{
				"number": 42, "url": "https://github.com/octocat/hello/pull/42",
				"state": "OPEN", "isDraft": false, "merged": false, "closed": false,
				"mergeable": "MERGEABLE", "mergeStateStatus": "BLOCKED",
				"reviewDecision": "REVIEW_REQUIRED", "headRefOid": "deadbeef",
				"commits": map[string]any{"nodes": []any{map[string]any{"commit": map[string]any{
					"oid": "deadbeef",
					"statusCheckRollup": map[string]any{
						"state": "FAILURE",
						"contexts": map[string]any{
							"nodes": []any{map[string]any{
								"__typename": "CheckRun", "name": "build", "status": "COMPLETED",
								"conclusion": "FAILURE",
								"detailsUrl": "https://github.com/octocat/hello/runs/9001",
								"databaseId": float64(9001),
							}},
							"pageInfo": map[string]any{"hasNextPage": false},
						},
					},
				}}}},
				"reviewThreads": map[string]any{"nodes": []any{}},
			}}},
		})
	})

	// Observe: failing job log, for the nudge's LogTail.
	mux.HandleFunc("/repos/octocat/hello/actions/jobs/9001/logs", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte(strings.Repeat("setup\n", 5) + "FAILED: build broke\n"))
	})

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// TestPRPoller_FunctionalEndToEnd drives the whole PR engine the daemon wires:
// a real github.Provider (discovery + observation) against a fake GitHub, a real
// sqlite store, a real Lifecycle Manager, and the real PR service. It spawns a
// session with a branch, runs one poller tick, and asserts the CI-failing PR
// flows discovery -> observe -> persist -> session status -> lifecycle nudge.
func TestPRPoller_FunctionalEndToEnd(t *testing.T) {
	ctx := context.Background()
	st := newStack(t)
	gh := fakeGitHub(t)

	provider, err := github.NewProvider(github.ProviderOptions{
		Token:      github.StaticTokenSource("tkn-test"),
		HTTPClient: gh.Client(),
		RESTBase:   gh.URL,
		GraphQLURL: gh.URL + "/graphql",
		UserAgent:  "ao-prpoller-functional-test",
	})
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}

	sess, err := st.sm.Spawn(ctx, ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindWorker, Branch: "feat/x", Prompt: "do it"})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}

	poller := prpoller.New(st.store, provider, provider, st.prm, staticRepos{owner: "octocat", repo: "hello"}, prpoller.Config{})
	if err := poller.Tick(ctx); err != nil {
		t.Fatalf("Tick: %v", err)
	}

	// The PR row was persisted from the observation.
	pr, ok, err := st.store.GetDisplayPRFactsForSession(ctx, sess.ID)
	if err != nil || !ok {
		t.Fatalf("GetDisplayPRFactsForSession: ok=%v err=%v", ok, err)
	}
	if pr.URL != "https://github.com/octocat/hello/pull/42" || pr.Number != 42 {
		t.Fatalf("PR not persisted: %+v", pr)
	}
	if pr.CI != domain.CIFailing {
		t.Fatalf("PR CI = %q, want failing", pr.CI)
	}

	// The derived session status reflects the failing CI.
	got, err := st.sm.Get(ctx, sess.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Status != domain.StatusCIFailed {
		t.Fatalf("status = %q, want ci_failed", got.Status)
	}

	// The lifecycle nudged the agent's pane via the messenger, carrying the log tail.
	if len(st.msg.msgs) != 1 {
		t.Fatalf("messenger got %d msgs, want 1: %v", len(st.msg.msgs), st.msg.msgs)
	}
	if !strings.Contains(st.msg.msgs[0], "CI is failing") || !strings.Contains(st.msg.msgs[0], "FAILED: build broke") {
		t.Fatalf("nudge missing CI-failure content: %q", st.msg.msgs[0])
	}
}

// TestPRPoller_NoOpenPRIsQuiet proves the common pre-PR state: a fresh session
// whose branch has no open PR yet produces no persisted PR and no nudge, and the
// tick still succeeds.
func TestPRPoller_NoOpenPRIsQuiet(t *testing.T) {
	ctx := context.Background()
	st := newStack(t)

	mux := http.NewServeMux()
	mux.HandleFunc("/repos/octocat/hello/pulls", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte("[]"))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	provider, err := github.NewProvider(github.ProviderOptions{
		Token:      github.StaticTokenSource("tkn-test"),
		HTTPClient: srv.Client(),
		RESTBase:   srv.URL,
		GraphQLURL: srv.URL + "/graphql",
		UserAgent:  "ao-prpoller-functional-test",
	})
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}

	sess, err := st.sm.Spawn(ctx, ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindWorker, Branch: "feat/x", Prompt: "do it"})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}

	poller := prpoller.New(st.store, provider, provider, st.prm, staticRepos{owner: "octocat", repo: "hello"}, prpoller.Config{})
	if err := poller.Tick(ctx); err != nil {
		t.Fatalf("Tick: %v", err)
	}

	if _, ok, _ := st.store.GetDisplayPRFactsForSession(ctx, sess.ID); ok {
		t.Fatalf("no PR should be persisted for a branch with no open PR")
	}
	if len(st.msg.msgs) != 0 {
		t.Fatalf("no nudge expected, got %v", st.msg.msgs)
	}
}
