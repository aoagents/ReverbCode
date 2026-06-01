package integration

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	scmgithub "github.com/aoagents/agent-orchestrator/backend/internal/adapters/scm/github"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/observe/scm"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/project"
)

// TestSCMPollerEndToEnd boots store + LCM + pr.Manager + the scm.Poller
// against an httptest GitHub stub, ticks once, and asserts:
//   - the poller resolved the PR URL via branch discovery
//   - pr.Manager persisted the PR row (PRWriter side of the bus)
//   - lifecycle.ApplyPRObservation fired the CI-failure nudge to the messenger
//
// This is the seam-by-seam validation that aa-37's spec describes: from
// SCM observation to PR row to agent nudge, with every dependency the
// daemon wires in production.
func TestSCMPollerEndToEnd(t *testing.T) {
	ctx := context.Background()
	st := newStack(t)

	if err := st.store.Upsert(ctx, project.Row{ID: "acme", Path: "/repo/acme", RepoOriginURL: "https://github.com/acme/repo.git", RegisteredAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	sess, err := st.sm.Spawn(ctx, ports.SpawnConfig{ProjectID: "acme", Kind: domain.KindWorker, Branch: "feat/x", Prompt: "fix CI"})
	if err != nil {
		t.Fatal(err)
	}

	// The PR URL the GitHub stub will report for branch acme:feat/x.
	prURL := "https://github.com/acme/repo/pull/77"

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/repos/acme/repo/pulls":
			if got := r.URL.Query().Get("head"); got != "acme:feat/x" {
				t.Errorf("pulls list head = %q, want acme:feat/x", got)
			}
			_ = json.NewEncoder(w).Encode([]map[string]any{
				{"number": 77, "state": "open", "html_url": prURL, "updated_at": "2026-05-15T10:00:00Z"},
			})
		case r.Method == http.MethodGet && r.URL.Path == "/repos/acme/repo/pulls/77":
			w.Header().Set("ETag", `W/"v1"`)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"number":             77,
				"state":              "open",
				"draft":              false,
				"merged":             false,
				"merged_at":          nil,
				"html_url":           prURL,
				"head":               map[string]any{"ref": "feat/x", "sha": "deadbeef"},
				"base":               map[string]any{"ref": "main"},
				"mergeable":          false,
				"rebaseable":         true,
				"mergeable_state":    "blocked",
				"merge_state_status": "BLOCKED",
			})
		case r.Method == http.MethodPost && r.URL.Path == "/graphql":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data": map[string]any{
					"repository": map[string]any{
						"pullRequest": map[string]any{
							"number":           77,
							"url":              prURL,
							"state":            "OPEN",
							"isDraft":          false,
							"merged":           false,
							"closed":           false,
							"mergeable":        "MERGEABLE",
							"mergeStateStatus": "BLOCKED",
							"reviewDecision":   "REVIEW_REQUIRED",
							"headRefOid":       "deadbeef",
							"commits": map[string]any{"nodes": []any{
								map[string]any{"commit": map[string]any{
									"oid": "deadbeef",
									"statusCheckRollup": map[string]any{
										"state": "FAILURE",
										"contexts": map[string]any{
											"nodes": []any{
												map[string]any{
													"__typename": "CheckRun",
													"name":       "build",
													"status":     "COMPLETED",
													"conclusion": "FAILURE",
													"detailsUrl": "https://github.com/acme/repo/runs/9001",
													"databaseId": float64(9001),
												},
											},
											"pageInfo": map[string]any{"hasNextPage": false},
										},
									},
								}},
							}},
							"reviewThreads": map[string]any{"nodes": []any{}},
						},
					},
				},
			})
		case r.Method == http.MethodGet && r.URL.Path == "/repos/acme/repo/actions/jobs/9001/logs":
			w.Header().Set("Content-Type", "text/plain")
			_, _ = w.Write([]byte("FAIL TestX\nFAIL TestY\n"))
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			http.Error(w, "no handler", http.StatusNotImplemented)
		}
	}))
	t.Cleanup(server.Close)

	provider, err := scmgithub.NewProvider(scmgithub.ProviderOptions{
		Token:      scmgithub.StaticTokenSource("tkn"),
		HTTPClient: server.Client(),
		RESTBase:   server.URL,
		GraphQLURL: server.URL + "/graphql",
	})
	if err != nil {
		t.Fatal(err)
	}

	projects := project.NewManager(st.store)
	poller := scm.New(scm.Deps{
		Provider:       provider,
		Branches:       provider,
		Sessions:       st.store,
		Projects:       projects,
		PR:             st.prm,
		Interval:       time.Hour, // ticker won't fire — we call Tick directly
		ObserveTimeout: 5 * time.Second,
		RemoteResolver: func(context.Context, string) (string, error) {
			// The project Row.RepoOriginURL is set above, so this fallback
			// should never be called; failing loudly catches a regression
			// where the poller silently shells out instead of using
			// project.Repo.
			t.Fatalf("remote resolver should not be invoked when project.Repo is set")
			return "", nil
		},
	})

	if err := poller.Tick(ctx); err != nil {
		t.Fatalf("poller.Tick: %v", err)
	}

	got, ok, err := st.store.GetPR(ctx, prURL)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatalf("pr row not written for %s", prURL)
	}
	if got.SessionID != sess.ID {
		t.Errorf("pr.SessionID = %q, want %q", got.SessionID, sess.ID)
	}
	if got.CI != domain.CIFailing {
		t.Errorf("pr.CI = %q, want %q", got.CI, domain.CIFailing)
	}
	checks, err := st.store.ListChecks(ctx, prURL)
	if err != nil {
		t.Fatal(err)
	}
	if len(checks) != 1 || checks[0].Status != domain.PRCheckFailed {
		t.Fatalf("checks = %+v", checks)
	}

	if len(st.msg.msgs) != 1 {
		t.Fatalf("expected exactly 1 lifecycle nudge, got %d (a double-nudge would regress sendOnce)", len(st.msg.msgs))
	}
	if !strings.Contains(st.msg.msgs[0], "CI is failing") {
		t.Errorf("messenger did not receive CI-failure body; got %q", st.msg.msgs[0])
	}
	if !strings.Contains(st.msg.msgs[0], "FAIL TestX") {
		t.Errorf("messenger did not receive log-tail body; got %q", st.msg.msgs[0])
	}
}
