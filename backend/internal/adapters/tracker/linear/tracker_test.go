package linear

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// recordedReq captures one inbound GraphQL POST so tests can assert against
// the exact query and variables the adapter sent.
type recordedReq struct {
	Query     string
	Variables map[string]any
}

// graphqlBody is the wire shape of every request the adapter sends to
// Linear: standard {query, variables} envelope. Tests use it to route by
// inspecting which top-level field the query references.
type graphqlBody struct {
	Query     string         `json:"query"`
	Variables map[string]any `json:"variables"`
}

// fakeLinear is a programmable httptest.Server that always answers POST
// /graphql. It routes requests via a user-supplied router that inspects the
// parsed body. Unrouted requests fail the test loudly — same loud-failure
// discipline as the github adapter's tests.
type fakeLinear struct {
	t        *testing.T
	server   *httptest.Server
	mu       sync.Mutex
	requests []recordedReq
	router   func(t *testing.T, w http.ResponseWriter, body graphqlBody)

	lastAuthHeader string
}

func newFakeLinear(t *testing.T, router func(t *testing.T, w http.ResponseWriter, body graphqlBody)) *fakeLinear {
	t.Helper()
	f := &fakeLinear{t: t, router: router}
	f.server = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.server.Close)
	return f
}

func (f *fakeLinear) serve(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		f.t.Errorf("unexpected method: %s", r.Method)
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	if r.URL.Path != "/graphql" {
		f.t.Errorf("unexpected path: %s", r.URL.Path)
		http.Error(w, "wrong path", http.StatusNotFound)
		return
	}
	raw, _ := io.ReadAll(r.Body)
	var body graphqlBody
	if err := json.Unmarshal(raw, &body); err != nil {
		f.t.Errorf("decode body: %v; raw=%s", err, raw)
		http.Error(w, "bad body", http.StatusBadRequest)
		return
	}
	f.mu.Lock()
	f.requests = append(f.requests, recordedReq{Query: body.Query, Variables: body.Variables})
	f.lastAuthHeader = r.Header.Get("Authorization")
	f.mu.Unlock()
	f.router(f.t, w, body)
}

func (f *fakeLinear) calls() []recordedReq {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]recordedReq, len(f.requests))
	copy(out, f.requests)
	return out
}

func (f *fakeLinear) authHeader() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.lastAuthHeader
}

// newTrackerForTest points an adapter at the fake server with a static
// token. Production code uses EnvTokenSource; tests skip that to keep the
// surface tiny.
func newTrackerForTest(t *testing.T, f *fakeLinear) *Tracker {
	t.Helper()
	tr, err := New(Options{
		BaseURL:    f.server.URL,
		Token:      StaticTokenSource("lin_api_test"),
		HTTPClient: f.server.Client(),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return tr
}

func ctx() context.Context { return context.Background() }

// writeJSON writes a JSON body with the given HTTP status. Tests use it as
// the single place that sets Content-Type so they stay focused on payload
// shape.
func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func TestNewRejectsMissingToken(t *testing.T) {
	if _, err := New(Options{Token: StaticTokenSource("")}); !errors.Is(err, ErrNoToken) {
		t.Fatalf("New empty token = %v, want ErrNoToken", err)
	}
	if _, err := New(Options{}); !errors.Is(err, ErrNoToken) {
		t.Fatalf("New no source = %v, want ErrNoToken", err)
	}
}

// TestAuthHeader_NoBearerPrefix pins the single easiest bug to introduce on
// this adapter: Linear personal API keys are sent as raw "Authorization:
// <key>", NOT "Authorization: Bearer <key>". OAuth tokens DO use Bearer but
// v1 only supports personal keys, so an accidental "Bearer "+tok would
// always 401. This guard catches the regression on every test that exercises
// the wire format.
func TestAuthHeader_NoBearerPrefix(t *testing.T) {
	f := newFakeLinear(t, func(t *testing.T, w http.ResponseWriter, body graphqlBody) {
		writeJSON(w, http.StatusOK, map[string]any{"data": map[string]any{"viewer": map[string]any{"id": "u1"}}})
	})
	tr := newTrackerForTest(t, f)
	if err := tr.Preflight(ctx()); err != nil {
		t.Fatalf("Preflight: %v", err)
	}
	if got := f.authHeader(); got != "lin_api_test" {
		t.Fatalf("Authorization = %q, want bare token (NO 'Bearer ' prefix)", got)
	}
}

// ---------------------------------------------------------------------------
// Preflight
// ---------------------------------------------------------------------------

func TestPreflight_HappyPath(t *testing.T) {
	f := newFakeLinear(t, func(t *testing.T, w http.ResponseWriter, body graphqlBody) {
		if !strings.Contains(body.Query, "viewer") {
			t.Errorf("Preflight query should reference viewer; got %q", body.Query)
		}
		writeJSON(w, http.StatusOK, map[string]any{"data": map[string]any{"viewer": map[string]any{"id": "u1"}}})
	})
	tr := newTrackerForTest(t, f)
	if err := tr.Preflight(ctx()); err != nil {
		t.Fatalf("Preflight: %v", err)
	}
}

func TestPreflight_InvalidToken_HTTP401(t *testing.T) {
	f := newFakeLinear(t, func(t *testing.T, w http.ResponseWriter, body graphqlBody) {
		writeJSON(w, http.StatusUnauthorized, map[string]any{"errors": []any{map[string]any{"message": "Authentication required"}}})
	})
	tr := newTrackerForTest(t, f)
	if err := tr.Preflight(ctx()); !errors.Is(err, ErrAuthFailed) {
		t.Fatalf("err = %v, want ErrAuthFailed", err)
	}
}

// TestPreflight_GraphQLAuthError covers the more common Linear failure
// mode: HTTP 200 with errors[].extensions.type = "authentication error".
// The SM relies on errors.Is to route, not on HTTP status.
func TestPreflight_GraphQLAuthError(t *testing.T) {
	f := newFakeLinear(t, func(t *testing.T, w http.ResponseWriter, body graphqlBody) {
		writeJSON(w, http.StatusOK, map[string]any{
			"errors": []any{map[string]any{
				"message":    "auth required",
				"extensions": map[string]any{"type": "authentication error"},
			}},
		})
	})
	tr := newTrackerForTest(t, f)
	if err := tr.Preflight(ctx()); !errors.Is(err, ErrAuthFailed) {
		t.Fatalf("err = %v, want ErrAuthFailed", err)
	}
}

func TestPreflight_CachesSuccess(t *testing.T) {
	f := newFakeLinear(t, func(t *testing.T, w http.ResponseWriter, body graphqlBody) {
		writeJSON(w, http.StatusOK, map[string]any{"data": map[string]any{"viewer": map[string]any{"id": "u1"}}})
	})
	tr := newTrackerForTest(t, f)
	for i := 0; i < 5; i++ {
		if err := tr.Preflight(ctx()); err != nil {
			t.Fatalf("Preflight #%d: %v", i, err)
		}
	}
	if got := len(f.calls()); got != 1 {
		t.Fatalf("HTTP calls = %d, want 1", got)
	}
}

func TestPreflight_RetriesAfterFailure(t *testing.T) {
	var calls int
	f := newFakeLinear(t, func(t *testing.T, w http.ResponseWriter, body graphqlBody) {
		calls++
		if calls == 1 {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"errors": []any{map[string]any{"message": "boom"}}})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"data": map[string]any{"viewer": map[string]any{"id": "u1"}}})
	})
	tr := newTrackerForTest(t, f)
	if err := tr.Preflight(ctx()); err == nil {
		t.Fatalf("first Preflight expected to fail")
	}
	if err := tr.Preflight(ctx()); err != nil {
		t.Fatalf("second Preflight: %v", err)
	}
	if got := len(f.calls()); got != 2 {
		t.Fatalf("HTTP calls = %d, want 2 (fail not cached)", got)
	}
}

// ---------------------------------------------------------------------------
// Get
// ---------------------------------------------------------------------------

// linearIssuePayload helps build the data.issue body in test handlers.
func linearIssuePayload(identifier, title, body, stateType, url string, labels []string, assignees []string) map[string]any {
	labelNodes := make([]map[string]any, len(labels))
	for i, l := range labels {
		labelNodes[i] = map[string]any{"name": l}
	}
	assigneeNodes := make([]map[string]any, len(assignees))
	for i, a := range assignees {
		assigneeNodes[i] = map[string]any{"name": a}
	}
	p := map[string]any{
		"identifier":  identifier,
		"title":       title,
		"description": body,
		"url":         url,
		"state":       map[string]any{"type": stateType},
		"labels":      map[string]any{"nodes": labelNodes},
		"assignees":   map[string]any{"nodes": assigneeNodes},
	}
	return p
}

// TestGet_HappyPath_ShortID confirms the adapter passes the opaque
// "TEAMKEY-NUMBER" short id straight through to issue(id:) without parsing
// or rewriting. Linear accepts both UUID and short id at that argument.
func TestGet_HappyPath_ShortID(t *testing.T) {
	f := newFakeLinear(t, func(t *testing.T, w http.ResponseWriter, body graphqlBody) {
		if !strings.Contains(body.Query, "issue(") {
			t.Errorf("query should fetch issue; got %q", body.Query)
		}
		if got, _ := body.Variables["id"].(string); got != "ABC-123" {
			t.Errorf("variables.id = %v, want ABC-123", body.Variables["id"])
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"data": map[string]any{
				"issue": linearIssuePayload("ABC-123", "title", "body", "started",
					"https://linear.app/ws/issue/ABC-123",
					[]string{"bug"}, []string{"alice"}),
			},
		})
	})
	tr := newTrackerForTest(t, f)
	issue, err := tr.Get(ctx(), domain.TrackerID{Provider: domain.TrackerProviderLinear, Native: "ABC-123"})
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	want := domain.Issue{
		ID:        domain.TrackerID{Provider: domain.TrackerProviderLinear, Native: "ABC-123"},
		Title:     "title",
		Body:      "body",
		State:     domain.IssueInProgress,
		URL:       "https://linear.app/ws/issue/ABC-123",
		Labels:    []string{"bug"},
		Assignees: []string{"alice"},
	}
	if !reflect.DeepEqual(issue, want) {
		t.Fatalf("issue = %#v\nwant %#v", issue, want)
	}
}

// TestGet_HappyPath_UUID confirms the adapter equally accepts a UUID in
// Native — Linear's issue(id:) takes either. The Native echoed on the
// returned Issue is the same string the caller passed in.
func TestGet_HappyPath_UUID(t *testing.T) {
	const uuid = "5af3107a-4f6f-11ec-81d3-0242ac130003"
	f := newFakeLinear(t, func(t *testing.T, w http.ResponseWriter, body graphqlBody) {
		if got, _ := body.Variables["id"].(string); got != uuid {
			t.Errorf("variables.id = %v, want uuid", body.Variables["id"])
		}
		// Linear still echoes the canonical short identifier on the response.
		writeJSON(w, http.StatusOK, map[string]any{
			"data": map[string]any{
				"issue": linearIssuePayload("ABC-7", "t", "b", "completed", "u", nil, nil),
			},
		})
	})
	tr := newTrackerForTest(t, f)
	issue, err := tr.Get(ctx(), domain.TrackerID{Provider: domain.TrackerProviderLinear, Native: uuid})
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if issue.ID.Native != uuid {
		t.Fatalf("returned Native = %q, want %q (original Native should round-trip)", issue.ID.Native, uuid)
	}
}

// TestGet_StateMapping covers every documented Linear state.type value.
// "review" is intentionally NOT a v1 output — Linear has no native review
// type; teams using "In Review" set type=started, which we collapse to
// in_progress. See doc.go for the rationale.
func TestGet_StateMapping(t *testing.T) {
	cases := []struct {
		linearType string
		want       domain.NormalizedIssueState
	}{
		{"completed", domain.IssueDone},
		{"canceled", domain.IssueCancelled},
		{"started", domain.IssueInProgress},
		{"unstarted", domain.IssueOpen},
		{"triage", domain.IssueOpen},
		{"backlog", domain.IssueOpen},
		{"something_unknown", domain.IssueOpen},
		{"", domain.IssueOpen},
	}
	for _, tc := range cases {
		t.Run(tc.linearType, func(t *testing.T) {
			f := newFakeLinear(t, func(t *testing.T, w http.ResponseWriter, body graphqlBody) {
				writeJSON(w, http.StatusOK, map[string]any{
					"data": map[string]any{
						"issue": linearIssuePayload("A-1", "t", "b", tc.linearType, "u", nil, nil),
					},
				})
			})
			tr := newTrackerForTest(t, f)
			issue, err := tr.Get(ctx(), domain.TrackerID{Provider: domain.TrackerProviderLinear, Native: "A-1"})
			if err != nil {
				t.Fatalf("Get: %v", err)
			}
			if issue.State != tc.want {
				t.Fatalf("state = %q, want %q", issue.State, tc.want)
			}
		})
	}
}

// TestGet_NotFound_DataNull pins the contract: Linear returns HTTP 200
// with data.issue == null when the id isn't visible to the token. The
// adapter must surface this as ErrNotFound so the SM's not-found branch
// is reachable.
func TestGet_NotFound_DataNull(t *testing.T) {
	f := newFakeLinear(t, func(t *testing.T, w http.ResponseWriter, body graphqlBody) {
		writeJSON(w, http.StatusOK, map[string]any{"data": map[string]any{"issue": nil}})
	})
	tr := newTrackerForTest(t, f)
	_, err := tr.Get(ctx(), domain.TrackerID{Provider: domain.TrackerProviderLinear, Native: "A-1"})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestGet_RateLimited_GraphQLError(t *testing.T) {
	reset := strconv.FormatInt(time.Now().Add(2*time.Minute).Unix(), 10)
	f := newFakeLinear(t, func(t *testing.T, w http.ResponseWriter, body graphqlBody) {
		w.Header().Set("X-RateLimit-Requests-Reset", reset)
		w.Header().Set("Retry-After", "30")
		writeJSON(w, http.StatusOK, map[string]any{
			"errors": []any{map[string]any{
				"message":    "Too many requests",
				"extensions": map[string]any{"type": "ratelimited"},
			}},
		})
	})
	tr := newTrackerForTest(t, f)
	_, err := tr.Get(ctx(), domain.TrackerID{Provider: domain.TrackerProviderLinear, Native: "A-1"})
	if !errors.Is(err, ErrRateLimited) {
		t.Fatalf("err = %v, want ErrRateLimited", err)
	}
	var rle *RateLimitError
	if !errors.As(err, &rle) {
		t.Fatalf("err = %v, want *RateLimitError", err)
	}
	if rle.RetryAfter != 30*time.Second {
		t.Fatalf("RetryAfter = %v, want 30s", rle.RetryAfter)
	}
	wantReset, _ := strconv.ParseInt(reset, 10, 64)
	if rle.ResetAt.Unix() != wantReset {
		t.Fatalf("ResetAt = %v, want unix %d", rle.ResetAt, wantReset)
	}
}

// TestGet_RateLimited_HTTP429 covers Linear's other surface for rate
// limiting — some endpoints return 429 with headers and no recognized
// errors[].extensions.type. The classifier must still recognize this as
// ErrRateLimited AND parse both Retry-After and X-RateLimit-Requests-Reset
// off the headers so the SM can back off intelligently.
func TestGet_RateLimited_HTTP429(t *testing.T) {
	reset := time.Now().Add(90 * time.Second).Unix()
	f := newFakeLinear(t, func(t *testing.T, w http.ResponseWriter, body graphqlBody) {
		w.Header().Set("Retry-After", "5")
		w.Header().Set("X-RateLimit-Requests-Reset", strconv.FormatInt(reset, 10))
		writeJSON(w, http.StatusTooManyRequests, map[string]any{"errors": []any{map[string]any{"message": "rate limit"}}})
	})
	tr := newTrackerForTest(t, f)
	_, err := tr.Get(ctx(), domain.TrackerID{Provider: domain.TrackerProviderLinear, Native: "A-1"})
	if !errors.Is(err, ErrRateLimited) {
		t.Fatalf("err = %v, want ErrRateLimited", err)
	}
	var rle *RateLimitError
	if !errors.As(err, &rle) {
		t.Fatalf("err = %v, want *RateLimitError", err)
	}
	if rle.RetryAfter != 5*time.Second {
		t.Fatalf("RetryAfter = %v, want 5s", rle.RetryAfter)
	}
	if rle.ResetAt.Unix() != reset {
		t.Fatalf("ResetAt = %d, want %d", rle.ResetAt.Unix(), reset)
	}
}

func TestGet_AuthFailed_HTTP401(t *testing.T) {
	f := newFakeLinear(t, func(t *testing.T, w http.ResponseWriter, body graphqlBody) {
		writeJSON(w, http.StatusUnauthorized, map[string]any{"errors": []any{map[string]any{"message": "Authentication required"}}})
	})
	tr := newTrackerForTest(t, f)
	_, err := tr.Get(ctx(), domain.TrackerID{Provider: domain.TrackerProviderLinear, Native: "A-1"})
	if !errors.Is(err, ErrAuthFailed) {
		t.Fatalf("err = %v, want ErrAuthFailed", err)
	}
}

func TestGet_AuthFailed_GraphQLError(t *testing.T) {
	f := newFakeLinear(t, func(t *testing.T, w http.ResponseWriter, body graphqlBody) {
		writeJSON(w, http.StatusOK, map[string]any{
			"errors": []any{map[string]any{
				"message":    "Authentication required, not authenticated",
				"extensions": map[string]any{"type": "authentication error"},
			}},
		})
	})
	tr := newTrackerForTest(t, f)
	_, err := tr.Get(ctx(), domain.TrackerID{Provider: domain.TrackerProviderLinear, Native: "A-1"})
	if !errors.Is(err, ErrAuthFailed) {
		t.Fatalf("err = %v, want ErrAuthFailed", err)
	}
}

// TestGet_GraphQLAuthError_CaseInsensitive pins the extensions.type
// normalization: the classifier lower-cases before matching, so an
// upstream change that ships "Authentication Error" (or trailing
// whitespace) still routes to ErrAuthFailed instead of silently
// degrading to a generic graphql-error. Silent degradation here would
// break the SM's recovery branch.
func TestGet_GraphQLAuthError_CaseInsensitive(t *testing.T) {
	f := newFakeLinear(t, func(t *testing.T, w http.ResponseWriter, body graphqlBody) {
		writeJSON(w, http.StatusOK, map[string]any{
			"errors": []any{map[string]any{
				"message":    "auth required",
				"extensions": map[string]any{"type": "  Authentication Error  "},
			}},
		})
	})
	tr := newTrackerForTest(t, f)
	_, err := tr.Get(ctx(), domain.TrackerID{Provider: domain.TrackerProviderLinear, Native: "A-1"})
	if !errors.Is(err, ErrAuthFailed) {
		t.Fatalf("err = %v, want ErrAuthFailed", err)
	}
}

// TestGet_Forbidden_GraphQLError covers the second auth-class
// extensions.type ("forbidden"). The Linear SDK ships both
// AuthenticationLinearError and ForbiddenLinearError as distinct types;
// for v1 we fold both onto ErrAuthFailed because the SM's recovery is
// identical (alert and stop). This test pins the mapping.
func TestGet_Forbidden_GraphQLError(t *testing.T) {
	f := newFakeLinear(t, func(t *testing.T, w http.ResponseWriter, body graphqlBody) {
		writeJSON(w, http.StatusOK, map[string]any{
			"errors": []any{map[string]any{
				"message":    "not allowed",
				"extensions": map[string]any{"type": "forbidden"},
			}},
		})
	})
	tr := newTrackerForTest(t, f)
	_, err := tr.Get(ctx(), domain.TrackerID{Provider: domain.TrackerProviderLinear, Native: "A-1"})
	if !errors.Is(err, ErrAuthFailed) {
		t.Fatalf("err = %v, want ErrAuthFailed", err)
	}
}

// TestGet_FeatureNotAccessible folds Linear's "feature not accessible"
// (plan-gated query, e.g. workspace not on the plan that exposes the
// queried field) onto ErrAuthFailed. Treating it as an auth-class failure
// keeps the SM's recovery surface coherent — both mean "this token can't
// satisfy the request" and require human intervention.
func TestGet_FeatureNotAccessible(t *testing.T) {
	f := newFakeLinear(t, func(t *testing.T, w http.ResponseWriter, body graphqlBody) {
		writeJSON(w, http.StatusOK, map[string]any{
			"errors": []any{map[string]any{
				"message":    "feature is not accessible on this plan",
				"extensions": map[string]any{"type": "feature not accessible"},
			}},
		})
	})
	tr := newTrackerForTest(t, f)
	_, err := tr.Get(ctx(), domain.TrackerID{Provider: domain.TrackerProviderLinear, Native: "A-1"})
	if !errors.Is(err, ErrAuthFailed) {
		t.Fatalf("err = %v, want ErrAuthFailed", err)
	}
}

func TestGet_RejectsWrongProvider(t *testing.T) {
	f := newFakeLinear(t, func(t *testing.T, w http.ResponseWriter, body graphqlBody) {
		t.Errorf("must not hit network on wrong provider")
	})
	tr := newTrackerForTest(t, f)
	_, err := tr.Get(ctx(), domain.TrackerID{Provider: domain.TrackerProviderGitHub, Native: "o/r#1"})
	if !errors.Is(err, ErrWrongProvider) {
		t.Fatalf("err = %v, want ErrWrongProvider", err)
	}
}

func TestGet_RejectsEmptyProvider(t *testing.T) {
	f := newFakeLinear(t, func(t *testing.T, w http.ResponseWriter, body graphqlBody) {
		t.Errorf("must not hit network on empty provider")
	})
	tr := newTrackerForTest(t, f)
	_, err := tr.Get(ctx(), domain.TrackerID{Native: "A-1"})
	if !errors.Is(err, ErrWrongProvider) {
		t.Fatalf("err = %v, want ErrWrongProvider", err)
	}
}

// TestGet_RejectsEmptyNative locks the one validation the adapter does
// over Native: empty strings can't possibly route to an issue, and
// passing them to Linear's issue(id:"") would just return a confusing
// auth-shaped error.
func TestGet_RejectsEmptyNative(t *testing.T) {
	f := newFakeLinear(t, func(t *testing.T, w http.ResponseWriter, body graphqlBody) {
		t.Errorf("must not hit network on empty native id")
	})
	tr := newTrackerForTest(t, f)
	_, err := tr.Get(ctx(), domain.TrackerID{Provider: domain.TrackerProviderLinear, Native: ""})
	if !errors.Is(err, ErrBadID) {
		t.Fatalf("err = %v, want ErrBadID", err)
	}
}

// TestGet_CanonicalizesProviderOnOutput keeps parity with the github
// adapter: callers must be able to re-route the returned Issue without
// inspecting which adapter produced it.
func TestGet_CanonicalizesProviderOnOutput(t *testing.T) {
	f := newFakeLinear(t, func(t *testing.T, w http.ResponseWriter, body graphqlBody) {
		writeJSON(w, http.StatusOK, map[string]any{
			"data": map[string]any{
				"issue": linearIssuePayload("A-1", "t", "b", "unstarted", "u", nil, nil),
			},
		})
	})
	tr := newTrackerForTest(t, f)
	issue, err := tr.Get(ctx(), domain.TrackerID{Provider: domain.TrackerProviderLinear, Native: "A-1"})
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if issue.ID.Provider != domain.TrackerProviderLinear {
		t.Fatalf("issue.ID.Provider = %q, want %q", issue.ID.Provider, domain.TrackerProviderLinear)
	}
}

// ---------------------------------------------------------------------------
// List
// ---------------------------------------------------------------------------

// TestList_WorkspaceWide_NoTeamFilter: empty repo.Native means the SM is
// asking for a workspace-wide enumeration; the adapter must skip the
// teams() lookup AND omit team from the filter argument.
func TestList_WorkspaceWide_NoTeamFilter(t *testing.T) {
	f := newFakeLinear(t, func(t *testing.T, w http.ResponseWriter, body graphqlBody) {
		if strings.Contains(body.Query, "teams(") {
			t.Errorf("must NOT issue a teams() lookup when repo is empty; got %q", body.Query)
		}
		// The filter variables map must not contain a team selector.
		if filt, _ := body.Variables["filter"].(map[string]any); filt != nil {
			if _, ok := filt["team"]; ok {
				t.Errorf("filter.team should be absent for workspace-wide list, got %v", filt["team"])
			}
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"data": map[string]any{
				"issues": map[string]any{"nodes": []any{
					linearIssuePayload("A-1", "t1", "b1", "started", "u1", nil, nil),
				}},
			},
		})
	})
	tr := newTrackerForTest(t, f)
	issues, err := tr.List(ctx(), domain.TrackerRepo{Provider: domain.TrackerProviderLinear, Native: ""}, domain.ListFilter{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(issues) != 1 || issues[0].ID.Native != "A-1" {
		t.Fatalf("issues = %#v", issues)
	}
}

// TestList_TeamScoped_ResolvesAndCachesTeamUUID is the load-bearing
// contract for repo.Native = team key: the adapter must resolve the team
// key to its UUID lazily via teams(filter:{key:{eq:$key}}, first:1), pass
// the UUID into the issues() filter, AND cache it so subsequent calls
// for the same team don't burn another teams() roundtrip.
func TestList_TeamScoped_ResolvesAndCachesTeamUUID(t *testing.T) {
	const teamUUID = "00000000-0000-0000-0000-000000000abc"
	var teamCalls, issuesCalls int
	f := newFakeLinear(t, func(t *testing.T, w http.ResponseWriter, body graphqlBody) {
		switch {
		case strings.Contains(body.Query, "teams("):
			teamCalls++
			if got, _ := body.Variables["key"].(string); got != "ABC" {
				t.Errorf("teams() variables.key = %v, want ABC", body.Variables["key"])
			}
			writeJSON(w, http.StatusOK, map[string]any{
				"data": map[string]any{
					"teams": map[string]any{"nodes": []any{map[string]any{"id": teamUUID, "key": "ABC"}}},
				},
			})
		case strings.Contains(body.Query, "issues("):
			issuesCalls++
			filt, _ := body.Variables["filter"].(map[string]any)
			team, _ := filt["team"].(map[string]any)
			id, _ := team["id"].(map[string]any)
			if got, _ := id["eq"].(string); got != teamUUID {
				t.Errorf("filter.team.id.eq = %v, want %s", id["eq"], teamUUID)
			}
			writeJSON(w, http.StatusOK, map[string]any{
				"data": map[string]any{"issues": map[string]any{"nodes": []any{}}},
			})
		default:
			t.Errorf("unexpected query: %s", body.Query)
		}
	})
	tr := newTrackerForTest(t, f)
	for i := 0; i < 3; i++ {
		if _, err := tr.List(ctx(), domain.TrackerRepo{Provider: domain.TrackerProviderLinear, Native: "ABC"}, domain.ListFilter{}); err != nil {
			t.Fatalf("List #%d: %v", i, err)
		}
	}
	if teamCalls != 1 {
		t.Fatalf("teams() lookups = %d, want 1 (must be cached after first hit)", teamCalls)
	}
	if issuesCalls != 3 {
		t.Fatalf("issues() lookups = %d, want 3", issuesCalls)
	}
}

// TestList_TeamNotFound covers the "key resolves to no team" branch.
// The SM's caller asked to scope to a team that the token can't see (or
// that doesn't exist) — ErrNotFound is the truthful answer.
func TestList_TeamNotFound(t *testing.T) {
	f := newFakeLinear(t, func(t *testing.T, w http.ResponseWriter, body graphqlBody) {
		if !strings.Contains(body.Query, "teams(") {
			t.Errorf("expected teams() lookup, got %q", body.Query)
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"data": map[string]any{"teams": map[string]any{"nodes": []any{}}},
		})
	})
	tr := newTrackerForTest(t, f)
	_, err := tr.List(ctx(), domain.TrackerRepo{Provider: domain.TrackerProviderLinear, Native: "NOPE"}, domain.ListFilter{})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestList_StateFilter(t *testing.T) {
	// open  → unstarted, started, triage, backlog
	// closed → completed, canceled
	// all   → no state filter
	cases := []struct {
		name      string
		filter    domain.ListStateFilter
		wantTypes []string
		wantNoSet bool
	}{
		{"open", domain.ListOpen, []string{"unstarted", "started", "triage", "backlog"}, false},
		{"closed", domain.ListClosed, []string{"completed", "canceled"}, false},
		{"all", domain.ListAll, nil, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFakeLinear(t, func(t *testing.T, w http.ResponseWriter, body graphqlBody) {
				filt, _ := body.Variables["filter"].(map[string]any)
				state, hasState := filt["state"].(map[string]any)
				if tc.wantNoSet {
					if hasState {
						t.Errorf("filter.state should be absent for all; got %v", filt["state"])
					}
				} else {
					typ, _ := state["type"].(map[string]any)
					gotIface, _ := typ["in"].([]any)
					got := make([]string, len(gotIface))
					for i, v := range gotIface {
						got[i], _ = v.(string)
					}
					if !reflect.DeepEqual(got, tc.wantTypes) {
						t.Errorf("filter.state.type.in = %v, want %v", got, tc.wantTypes)
					}
				}
				writeJSON(w, http.StatusOK, map[string]any{
					"data": map[string]any{"issues": map[string]any{"nodes": []any{}}},
				})
			})
			tr := newTrackerForTest(t, f)
			if _, err := tr.List(ctx(), domain.TrackerRepo{Provider: domain.TrackerProviderLinear, Native: ""}, domain.ListFilter{State: tc.filter}); err != nil {
				t.Fatalf("List: %v", err)
			}
		})
	}
}

func TestList_AssigneeAndLabels(t *testing.T) {
	f := newFakeLinear(t, func(t *testing.T, w http.ResponseWriter, body graphqlBody) {
		filt, _ := body.Variables["filter"].(map[string]any)
		ass, _ := filt["assignee"].(map[string]any)
		name, _ := ass["name"].(map[string]any)
		if got, _ := name["eq"].(string); got != "alice" {
			t.Errorf("filter.assignee.name.eq = %v, want alice", name["eq"])
		}
		lab, _ := filt["labels"].(map[string]any)
		ln, _ := lab["name"].(map[string]any)
		gotIface, _ := ln["in"].([]any)
		got := make([]string, len(gotIface))
		for i, v := range gotIface {
			got[i], _ = v.(string)
		}
		want := []string{"bug", "help wanted"}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("filter.labels.name.in = %v, want %v", got, want)
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"data": map[string]any{"issues": map[string]any{"nodes": []any{}}},
		})
	})
	tr := newTrackerForTest(t, f)
	if _, err := tr.List(ctx(),
		domain.TrackerRepo{Provider: domain.TrackerProviderLinear, Native: ""},
		domain.ListFilter{Assignee: "alice", Labels: []string{"bug", "help wanted"}},
	); err != nil {
		t.Fatalf("List: %v", err)
	}
}

// TestList_LimitDefaultAndCap pins the silent-cap contract from the port
// docstring: zero means "adapter default" (50, Linear's pagination
// default), and asking for more than the cap returns exactly cap results
// without error. Linear's hard cap on first: is 250.
func TestList_LimitDefaultAndCap(t *testing.T) {
	cases := []struct {
		name      string
		in        int
		wantFirst float64
	}{
		{"zero → default 50", 0, 50},
		{"custom 100", 100, 100},
		{"capped at 250", 9999, 250},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFakeLinear(t, func(t *testing.T, w http.ResponseWriter, body graphqlBody) {
				got, _ := body.Variables["first"].(float64)
				if got != tc.wantFirst {
					t.Errorf("variables.first = %v, want %v", got, tc.wantFirst)
				}
				writeJSON(w, http.StatusOK, map[string]any{
					"data": map[string]any{"issues": map[string]any{"nodes": []any{}}},
				})
			})
			tr := newTrackerForTest(t, f)
			if _, err := tr.List(ctx(),
				domain.TrackerRepo{Provider: domain.TrackerProviderLinear, Native: ""},
				domain.ListFilter{Limit: tc.in},
			); err != nil {
				t.Fatalf("List: %v", err)
			}
		})
	}
}

func TestList_RejectsWrongProvider(t *testing.T) {
	f := newFakeLinear(t, func(t *testing.T, w http.ResponseWriter, body graphqlBody) {
		t.Errorf("must not hit network on wrong provider")
	})
	tr := newTrackerForTest(t, f)
	_, err := tr.List(ctx(), domain.TrackerRepo{Provider: domain.TrackerProviderGitHub, Native: "o/r"}, domain.ListFilter{})
	if !errors.Is(err, ErrWrongProvider) {
		t.Fatalf("err = %v, want ErrWrongProvider", err)
	}
}

// TestList_CanonicalizesProviderOnOutput keeps parity with Get.
func TestList_CanonicalizesProviderOnOutput(t *testing.T) {
	f := newFakeLinear(t, func(t *testing.T, w http.ResponseWriter, body graphqlBody) {
		writeJSON(w, http.StatusOK, map[string]any{
			"data": map[string]any{"issues": map[string]any{"nodes": []any{
				linearIssuePayload("A-1", "t", "b", "started", "u", nil, nil),
			}}},
		})
	})
	tr := newTrackerForTest(t, f)
	issues, err := tr.List(ctx(), domain.TrackerRepo{Provider: domain.TrackerProviderLinear, Native: ""}, domain.ListFilter{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(issues) != 1 || issues[0].ID.Provider != domain.TrackerProviderLinear {
		t.Fatalf("issues = %#v", issues)
	}
}
