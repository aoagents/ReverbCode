package gitlab

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

// recordedReq captures one inbound HTTP request so tests can assert against
// the exact GitLab API surface the adapter touched. EscapedPath preserves the
// URL-encoded form so subgroup tests can prove the path was escaped properly
// (r.URL.Path is decoded by Go's http server).
type recordedReq struct {
	Method      string
	Path        string
	EscapedPath string
	Body        string
}

// fakeGL is a programmable httptest.Server matching by "METHOD path" (decoded
// path). Unmatched requests fail the test — that's the point of TDD, so an
// accidental extra call is loud.
type fakeGL struct {
	t        *testing.T
	server   *httptest.Server
	mu       sync.Mutex
	requests []recordedReq
	handlers map[string]http.HandlerFunc
}

func newFakeGL(t *testing.T) *fakeGL {
	t.Helper()
	f := &fakeGL{t: t, handlers: map[string]http.HandlerFunc{}}
	f.server = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.server.Close)
	return f
}

func (f *fakeGL) on(method, path string, h http.HandlerFunc) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.handlers[method+" "+path] = h
}

func (f *fakeGL) serve(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	key := r.Method + " " + r.URL.Path
	f.mu.Lock()
	f.requests = append(f.requests, recordedReq{
		Method:      r.Method,
		Path:        r.URL.Path,
		EscapedPath: r.URL.EscapedPath(),
		Body:        string(body),
	})
	h, ok := f.handlers[key]
	f.mu.Unlock()
	if !ok {
		f.t.Errorf("unexpected request: %s", key)
		http.Error(w, "no handler", http.StatusNotImplemented)
		return
	}
	r.Body = io.NopCloser(strings.NewReader(string(body)))
	h(w, r)
}

func (f *fakeGL) calls() []recordedReq {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]recordedReq, len(f.requests))
	copy(out, f.requests)
	return out
}

func newTrackerForTest(t *testing.T, f *fakeGL) *Tracker {
	t.Helper()
	tr, err := New(Options{
		BaseURL:    f.server.URL,
		Token:      StaticTokenSource("tkn-test"),
		HTTPClient: f.server.Client(),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return tr
}

func ctx() context.Context { return context.Background() }

func TestNewRejectsMissingToken(t *testing.T) {
	if _, err := New(Options{Token: StaticTokenSource("")}); !errors.Is(err, ErrNoToken) {
		t.Fatalf("New with empty token = %v, want ErrNoToken", err)
	}
	if _, err := New(Options{}); !errors.Is(err, ErrNoToken) {
		t.Fatalf("New with no source = %v, want ErrNoToken", err)
	}
}

func TestParseID(t *testing.T) {
	cases := []struct {
		name        string
		native      string
		wantProject string
		wantIID     int
		wantErr     bool
	}{
		{"happy", "group/project#42", "group/project", 42, false},
		{"subgroup one level", "group/sub/project#7", "group/sub/project", 7, false},
		{"subgroup deeper", "group/sub1/sub2/project#1", "group/sub1/sub2/project", 1, false},
		{"missing hash", "group/project", "", 0, true},
		{"missing slash", "project#42", "", 0, true},
		{"empty leading segment", "/project#1", "", 0, true},
		{"empty trailing segment", "group/#1", "", 0, true},
		{"empty middle segment", "group//project#1", "", 0, true},
		{"non-numeric", "g/p#abc", "", 0, true},
		{"zero iid", "g/p#0", "", 0, true},
		{"negative iid", "g/p#-1", "", 0, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			project, iid, err := parseGitLabID(tc.native)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got %s#%d", project, iid)
				}
				return
			}
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			if project != tc.wantProject || iid != tc.wantIID {
				t.Fatalf("got %s#%d, want %s#%d", project, iid, tc.wantProject, tc.wantIID)
			}
		})
	}
}

func TestParseRepo(t *testing.T) {
	cases := []struct {
		name    string
		native  string
		want    string
		wantErr bool
	}{
		{"top-level", "group/project", "group/project", false},
		{"subgroup", "group/sub/project", "group/sub/project", false},
		{"deep subgroup", "g/a/b/c/project", "g/a/b/c/project", false},
		{"empty", "", "", true},
		{"single segment", "project", "", true},
		{"leading slash", "/project", "", true},
		{"trailing slash", "group/", "", true},
		{"empty middle", "group//project", "", true},
		{"embedded hash", "group/pro#ject", "", true},
		{"embedded space", "group/pro ject", "", true},
		{"embedded newline", "group/project\n", "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseGitLabRepo(tc.native)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got %q", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			if got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestGet_HappyPath(t *testing.T) {
	f := newFakeGL(t)
	f.on("GET", "/projects/group/project/issues/42", func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("PRIVATE-TOKEN"); got != "tkn-test" {
			t.Errorf("PRIVATE-TOKEN = %q, want tkn-test", got)
		}
		if got := r.Header.Get("Authorization"); got != "" {
			t.Errorf("Authorization = %q, want empty (PRIVATE-TOKEN auth)", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"iid": 42,
			"title": "Found a bug",
			"description": "It does not work",
			"state": "opened",
			"web_url": "https://gitlab.com/group/project/-/issues/42",
			"labels": ["bug","in-progress"],
			"assignees": [{"username":"alice"},{"username":"bob"}]
		}`))
	})
	tr := newTrackerForTest(t, f)

	issue, err := tr.Get(ctx(), domain.TrackerID{Provider: domain.TrackerProviderGitLab, Native: "group/project#42"})
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	want := domain.Issue{
		ID:        domain.TrackerID{Provider: domain.TrackerProviderGitLab, Native: "group/project#42"},
		Title:     "Found a bug",
		Body:      "It does not work",
		State:     domain.IssueInProgress,
		URL:       "https://gitlab.com/group/project/-/issues/42",
		Labels:    []string{"bug", "in-progress"},
		Assignees: []string{"alice", "bob"},
	}
	if !reflect.DeepEqual(issue, want) {
		t.Fatalf("issue = %#v\nwant %#v", issue, want)
	}
}

// TestGet_URLEncodesSubgroupPath proves the FULL project path is URL-encoded
// when forming the endpoint — otherwise GitLab returns 404 for any project
// inside a subgroup.
func TestGet_URLEncodesSubgroupPath(t *testing.T) {
	f := newFakeGL(t)
	f.on("GET", "/projects/group/sub/project/issues/5", func(w http.ResponseWriter, r *http.Request) {
		// The wire path must contain encoded slashes, otherwise GitLab will
		// route the request as /projects/group/sub/project/issues/5 instead
		// of /projects/<encoded>/issues/5.
		if !strings.Contains(r.URL.EscapedPath(), "%2F") {
			t.Errorf("escaped path = %q, want slashes encoded as %%2F", r.URL.EscapedPath())
		}
		_, _ = w.Write([]byte(`{"iid":5,"title":"t","description":"","state":"opened","web_url":"https://gitlab.com/group/sub/project/-/issues/5"}`))
	})
	tr := newTrackerForTest(t, f)
	issue, err := tr.Get(ctx(), domain.TrackerID{Provider: domain.TrackerProviderGitLab, Native: "group/sub/project#5"})
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if issue.ID.Native != "group/sub/project#5" {
		t.Fatalf("native = %q, want group/sub/project#5", issue.ID.Native)
	}
}

func TestGet_StateMapping(t *testing.T) {
	cases := []struct {
		name      string
		state     string
		labels    []string
		wantState domain.NormalizedIssueState
	}{
		{"plain opened", "opened", nil, domain.IssueOpen},
		{"opened with in-progress", "opened", []string{"in-progress"}, domain.IssueInProgress},
		{"opened with in-review", "opened", []string{"in-review"}, domain.IssueInReview},
		{"review wins over progress", "opened", []string{"in-progress", "in-review"}, domain.IssueInReview},
		{"closed plain", "closed", nil, domain.IssueDone},
		{"closed cancelled label", "closed", []string{"cancelled"}, domain.IssueCancelled},
		{"closed wontfix label", "closed", []string{"wontfix"}, domain.IssueCancelled},
		{"closed mixed labels still cancelled", "closed", []string{"bug", "wontfix"}, domain.IssueCancelled},
		// Label matching is case-insensitive — GitLab label names are
		// case-insensitive in the UI, so a user typing "In-Progress" or
		// "Cancelled" should be recognized.
		{"opened mixed-case in-progress", "opened", []string{"In-Progress"}, domain.IssueInProgress},
		{"opened upper-case in-review", "opened", []string{"IN-REVIEW"}, domain.IssueInReview},
		{"closed mixed-case cancelled", "closed", []string{"Cancelled"}, domain.IssueCancelled},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFakeGL(t)
			payload := map[string]any{
				"iid":         1,
				"title":       "t",
				"description": "",
				"state":       tc.state,
				"web_url":     "https://gitlab.com/g/p/-/issues/1",
			}
			if tc.labels != nil {
				payload["labels"] = tc.labels
			}
			b, _ := json.Marshal(payload)
			f.on("GET", "/projects/g/p/issues/1", func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write(b)
			})
			tr := newTrackerForTest(t, f)
			issue, err := tr.Get(ctx(), domain.TrackerID{Provider: domain.TrackerProviderGitLab, Native: "g/p#1"})
			if err != nil {
				t.Fatalf("Get: %v", err)
			}
			if issue.State != tc.wantState {
				t.Fatalf("state = %q, want %q", issue.State, tc.wantState)
			}
		})
	}
}

func TestGet_NotFound(t *testing.T) {
	f := newFakeGL(t)
	f.on("GET", "/projects/g/p/issues/1", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"message":"404 Not found"}`, http.StatusNotFound)
	})
	tr := newTrackerForTest(t, f)
	_, err := tr.Get(ctx(), domain.TrackerID{Provider: domain.TrackerProviderGitLab, Native: "g/p#1"})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

// TestGet_RateLimited covers GitLab's 429 path. Headers are RateLimit-Remaining
// / RateLimit-Reset (no X- prefix, unlike GitHub).
func TestGet_RateLimited(t *testing.T) {
	f := newFakeGL(t)
	reset := time.Now().Add(2 * time.Minute).Unix()
	f.on("GET", "/projects/g/p/issues/1", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("RateLimit-Remaining", "0")
		w.Header().Set("RateLimit-Reset", strconv.FormatInt(reset, 10))
		http.Error(w, `{"message":"Retry later"}`, http.StatusTooManyRequests)
	})
	tr := newTrackerForTest(t, f)
	_, err := tr.Get(ctx(), domain.TrackerID{Provider: domain.TrackerProviderGitLab, Native: "g/p#1"})
	if !errors.Is(err, ErrRateLimited) {
		t.Fatalf("err = %v, want ErrRateLimited", err)
	}
	var rle *RateLimitError
	if !errors.As(err, &rle) {
		t.Fatalf("err = %v, want *RateLimitError", err)
	}
	if got := rle.ResetAt.Unix(); got != reset {
		t.Fatalf("ResetAt = %d, want %d", got, reset)
	}
}

// TestGet_RateLimitedRetryAfter covers the path where GitLab returns 429 with
// only the Retry-After header (e.g. some application-level limits).
func TestGet_RateLimitedRetryAfter(t *testing.T) {
	f := newFakeGL(t)
	f.on("GET", "/projects/g/p/issues/1", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "60")
		http.Error(w, `{"message":"This endpoint has been requested too many times."}`, http.StatusTooManyRequests)
	})
	tr := newTrackerForTest(t, f)
	_, err := tr.Get(ctx(), domain.TrackerID{Provider: domain.TrackerProviderGitLab, Native: "g/p#1"})
	if !errors.Is(err, ErrRateLimited) {
		t.Fatalf("err = %v, want ErrRateLimited", err)
	}
	var rle *RateLimitError
	if !errors.As(err, &rle) {
		t.Fatalf("err = %v, want *RateLimitError", err)
	}
	if rle.RetryAfter != 60*time.Second {
		t.Fatalf("RetryAfter = %v, want 60s", rle.RetryAfter)
	}
}

func TestGet_RejectsWrongProvider(t *testing.T) {
	f := newFakeGL(t)
	tr := newTrackerForTest(t, f)
	_, err := tr.Get(ctx(), domain.TrackerID{Provider: domain.TrackerProviderGitHub, Native: "o/r#1"})
	if !errors.Is(err, ErrWrongProvider) {
		t.Fatalf("err = %v, want ErrWrongProvider", err)
	}
}

func TestGet_RejectsEmptyProvider(t *testing.T) {
	f := newFakeGL(t)
	tr := newTrackerForTest(t, f)
	_, err := tr.Get(ctx(), domain.TrackerID{Native: "g/p#1"})
	if !errors.Is(err, ErrWrongProvider) {
		t.Fatalf("err = %v, want ErrWrongProvider", err)
	}
}

// TestGet_CanonicalizesProviderOnOutput pins that returned Issues always carry
// domain.TrackerProviderGitLab so callers can re-route without inspecting
// which adapter they originally talked to.
func TestGet_CanonicalizesProviderOnOutput(t *testing.T) {
	f := newFakeGL(t)
	f.on("GET", "/projects/g/p/issues/1", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"iid":1,"title":"t","description":"","state":"opened","web_url":"https://gitlab.com/g/p/-/issues/1"}`))
	})
	tr := newTrackerForTest(t, f)
	issue, err := tr.Get(ctx(), domain.TrackerID{Provider: domain.TrackerProviderGitLab, Native: "g/p#1"})
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if issue.ID.Provider != domain.TrackerProviderGitLab {
		t.Fatalf("issue.ID.Provider = %q, want %q", issue.ID.Provider, domain.TrackerProviderGitLab)
	}
	if issue.ID.Native != "g/p#1" {
		t.Fatalf("issue.ID.Native = %q, want g/p#1", issue.ID.Native)
	}
}

// TestGet_AuthFailed locks in that a 401 maps to ErrAuthFailed.
func TestGet_AuthFailed(t *testing.T) {
	f := newFakeGL(t)
	f.on("GET", "/projects/g/p/issues/1", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"message":"401 Unauthorized"}`, http.StatusUnauthorized)
	})
	tr := newTrackerForTest(t, f)
	_, err := tr.Get(ctx(), domain.TrackerID{Provider: domain.TrackerProviderGitLab, Native: "g/p#1"})
	if !errors.Is(err, ErrAuthFailed) {
		t.Fatalf("err = %v, want ErrAuthFailed", err)
	}
}

// TestGet_ForbiddenIsAuth pins that 403 without rate-limit signals maps to
// ErrAuthFailed (not ErrRateLimited).
func TestGet_ForbiddenIsAuth(t *testing.T) {
	f := newFakeGL(t)
	f.on("GET", "/projects/g/p/issues/1", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"message":"403 Forbidden"}`, http.StatusForbidden)
	})
	tr := newTrackerForTest(t, f)
	_, err := tr.Get(ctx(), domain.TrackerID{Provider: domain.TrackerProviderGitLab, Native: "g/p#1"})
	if !errors.Is(err, ErrAuthFailed) {
		t.Fatalf("err = %v, want ErrAuthFailed", err)
	}
}

// ---------------------------------------------------------------------------
// Preflight
// ---------------------------------------------------------------------------

func TestPreflight_HappyPath(t *testing.T) {
	f := newFakeGL(t)
	f.on("GET", "/user", func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("PRIVATE-TOKEN"); got != "tkn-test" {
			t.Errorf("PRIVATE-TOKEN = %q", got)
		}
		_, _ = w.Write([]byte(`{"id":1,"username":"alice"}`))
	})
	tr := newTrackerForTest(t, f)
	if err := tr.Preflight(ctx()); err != nil {
		t.Fatalf("Preflight: %v", err)
	}
}

func TestPreflight_InvalidToken(t *testing.T) {
	f := newFakeGL(t)
	f.on("GET", "/user", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"message":"401 Unauthorized"}`, http.StatusUnauthorized)
	})
	tr := newTrackerForTest(t, f)
	err := tr.Preflight(ctx())
	if !errors.Is(err, ErrAuthFailed) {
		t.Fatalf("err = %v, want ErrAuthFailed", err)
	}
}

// TestPreflight_CachesSuccess pins that successful checks are cached.
func TestPreflight_CachesSuccess(t *testing.T) {
	f := newFakeGL(t)
	f.on("GET", "/user", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"id":1,"username":"alice"}`))
	})
	tr := newTrackerForTest(t, f)
	for i := 0; i < 5; i++ {
		if err := tr.Preflight(ctx()); err != nil {
			t.Fatalf("Preflight #%d: %v", i, err)
		}
	}
	if got := len(f.calls()); got != 1 {
		t.Fatalf("HTTP calls = %d, want 1 (success should be cached)", got)
	}
}

// TestPreflight_RetriesAfterFailure pins that failures are NOT cached.
func TestPreflight_RetriesAfterFailure(t *testing.T) {
	f := newFakeGL(t)
	var calls int
	f.on("GET", "/user", func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			http.Error(w, `{"message":"server exploded"}`, http.StatusInternalServerError)
			return
		}
		_, _ = w.Write([]byte(`{"id":1,"username":"alice"}`))
	})
	tr := newTrackerForTest(t, f)
	if err := tr.Preflight(ctx()); err == nil {
		t.Fatalf("first Preflight expected to fail")
	}
	if err := tr.Preflight(ctx()); err != nil {
		t.Fatalf("second Preflight: %v", err)
	}
	if got := len(f.calls()); got != 2 {
		t.Fatalf("HTTP calls = %d, want 2 (first fail not cached)", got)
	}
}

// ---------------------------------------------------------------------------
// List
// ---------------------------------------------------------------------------

func TestList_HappyPathAndDefaults(t *testing.T) {
	f := newFakeGL(t)
	f.on("GET", "/projects/g/p/issues", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		// GitLab state names are different from GitHub: "all" / "opened" / "closed".
		if got := q.Get("state"); got != "all" {
			t.Errorf("state = %q, want all (default)", got)
		}
		if got := q.Get("per_page"); got != "30" {
			t.Errorf("per_page = %q, want 30 (default)", got)
		}
		_, _ = w.Write([]byte(`[
			{"iid":1,"title":"first","description":"b1","state":"opened","web_url":"https://gitlab.com/g/p/-/issues/1","labels":["bug"],"assignees":[]},
			{"iid":2,"title":"second","description":"b2","state":"closed","web_url":"https://gitlab.com/g/p/-/issues/2","labels":[],"assignees":[{"username":"alice"}]}
		]`))
	})
	tr := newTrackerForTest(t, f)
	issues, err := tr.List(ctx(), domain.TrackerRepo{Provider: domain.TrackerProviderGitLab, Native: "g/p"}, domain.ListFilter{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(issues) != 2 {
		t.Fatalf("len = %d, want 2", len(issues))
	}
	if issues[0].ID.Native != "g/p#1" || issues[0].State != domain.IssueOpen || issues[0].Title != "first" {
		t.Fatalf("issues[0] = %#v", issues[0])
	}
	if issues[1].ID.Native != "g/p#2" || issues[1].State != domain.IssueDone || len(issues[1].Assignees) != 1 || issues[1].Assignees[0] != "alice" {
		t.Fatalf("issues[1] = %#v", issues[1])
	}
}

// TestList_URLEncodesSubgroupPath proves subgroup repos work for List too.
func TestList_URLEncodesSubgroupPath(t *testing.T) {
	f := newFakeGL(t)
	f.on("GET", "/projects/group/sub/project/issues", func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.EscapedPath(), "%2F") {
			t.Errorf("escaped path = %q, want slashes encoded", r.URL.EscapedPath())
		}
		_, _ = w.Write([]byte(`[]`))
	})
	tr := newTrackerForTest(t, f)
	if _, err := tr.List(ctx(), domain.TrackerRepo{Provider: domain.TrackerProviderGitLab, Native: "group/sub/project"}, domain.ListFilter{}); err != nil {
		t.Fatalf("List: %v", err)
	}
}

func TestList_QueryEncoding(t *testing.T) {
	cases := []struct {
		name   string
		filter domain.ListFilter
		wantQ  map[string]string
	}{
		{
			name:   "opened + labels + assignee_username + limit",
			filter: domain.ListFilter{State: domain.ListOpen, Labels: []string{"bug", "help wanted"}, Assignee: "alice", Limit: 50},
			wantQ:  map[string]string{"state": "opened", "labels": "bug,help wanted", "assignee_username": "alice", "per_page": "50"},
		},
		{
			name:   "closed only",
			filter: domain.ListFilter{State: domain.ListClosed},
			wantQ:  map[string]string{"state": "closed", "per_page": "30"},
		},
		{
			name:   "limit capped at 100",
			filter: domain.ListFilter{Limit: 9999},
			wantQ:  map[string]string{"state": "all", "per_page": "100"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFakeGL(t)
			f.on("GET", "/projects/g/p/issues", func(w http.ResponseWriter, r *http.Request) {
				got := r.URL.Query()
				for k, want := range tc.wantQ {
					if g := got.Get(k); g != want {
						t.Errorf("query[%q] = %q, want %q", k, g, want)
					}
				}
				_, _ = w.Write([]byte(`[]`))
			})
			tr := newTrackerForTest(t, f)
			if _, err := tr.List(ctx(), domain.TrackerRepo{Provider: domain.TrackerProviderGitLab, Native: "g/p"}, tc.filter); err != nil {
				t.Fatalf("List: %v", err)
			}
		})
	}
}

func TestList_RejectsWrongProvider(t *testing.T) {
	f := newFakeGL(t)
	tr := newTrackerForTest(t, f)
	_, err := tr.List(ctx(), domain.TrackerRepo{Provider: domain.TrackerProviderGitHub, Native: "o/r"}, domain.ListFilter{})
	if !errors.Is(err, ErrWrongProvider) {
		t.Fatalf("err = %v, want ErrWrongProvider", err)
	}
	if calls := f.calls(); len(calls) != 0 {
		t.Fatalf("unexpected HTTP calls: %#v", calls)
	}
}

func TestList_RejectsBadRepo(t *testing.T) {
	cases := []string{
		"",                // empty
		"noseparator",     // missing /
		"/project",        // empty leading
		"group/",          // empty trailing
		"group//proj",     // empty middle
		"group/pro ject",  // embedded space
		"group/pro#ject",  // embedded #
		"\tgroup/project", // leading tab
		"group/project\n", // trailing newline
	}
	for _, native := range cases {
		t.Run(native, func(t *testing.T) {
			f := newFakeGL(t)
			tr := newTrackerForTest(t, f)
			_, err := tr.List(ctx(), domain.TrackerRepo{Provider: domain.TrackerProviderGitLab, Native: native}, domain.ListFilter{})
			if !errors.Is(err, ErrBadID) {
				t.Fatalf("native=%q: err = %v, want ErrBadID", native, err)
			}
		})
	}
}
