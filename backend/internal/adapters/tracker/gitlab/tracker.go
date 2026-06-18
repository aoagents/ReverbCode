package gitlab

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

const (
	defaultBaseURL   = "https://gitlab.com/api/v4"
	defaultUserAgent = "ao-agent-orchestrator/tracker-gitlab"

	// Status labels recognized on open issues. Adopted verbatim from the
	// GitHub adapter so the cross-provider contract is one shape: humans
	// (and other tooling) put "in-progress" / "in-review" on issues, and
	// the adapter projects them onto the normalized state. v1 does NOT
	// write these labels — see issue #40 for the write-side work.
	labelInProgress = "in-progress"
	labelInReview   = "in-review"

	// Status labels recognized on closed issues. GitLab has no equivalent
	// of GitHub's state_reason=not_planned, so we use a label convention:
	// "cancelled" or "wontfix" on a closed issue means cancelled rather
	// than completed.
	labelCancelled = "cancelled"
	labelWontfix   = "wontfix"

	stateClosedGL = "closed"

	// List pagination — GitLab's per_page maxes at 100. Default 30 to
	// match the GitHub adapter (GitLab's API default is 20, but uniform
	// defaults across providers make the SM's behavior predictable).
	defaultListLimit = 30
	maxListLimit     = 100
)

// Sentinel errors. Callers should match via errors.Is; the orchestrator's
// lifecycle code is intentionally insulated from raw HTTP status codes.
var (
	ErrNotFound      = errors.New("gitlab tracker: issue not found")
	ErrRateLimited   = errors.New("gitlab tracker: rate limited")
	ErrAuthFailed    = errors.New("gitlab tracker: authentication failed")
	ErrWrongProvider = errors.New("gitlab tracker: id is not a gitlab tracker id")
	ErrBadID         = errors.New("gitlab tracker: malformed native id")
)

// RateLimitError is returned when GitLab reports the request was rate-limited.
// Callers that want to back off intelligently can extract ResetAt / RetryAfter
// via errors.As; callers that only need the category can use
// errors.Is(err, ErrRateLimited).
type RateLimitError struct {
	ResetAt    time.Time
	RetryAfter time.Duration
	Message    string
}

func (e *RateLimitError) Error() string {
	if e == nil {
		return ErrRateLimited.Error()
	}
	if e.Message != "" {
		return "gitlab tracker: rate limited: " + e.Message
	}
	return ErrRateLimited.Error()
}

func (e *RateLimitError) Is(target error) bool { return target == ErrRateLimited }

// Options configures a Tracker. All fields except Token are optional —
// production code typically sets Token alone; tests inject HTTPClient and
// BaseURL to point at an httptest fake.
type Options struct {
	Token      TokenSource
	HTTPClient *http.Client
	BaseURL    string
	UserAgent  string
}

// Tracker implements ports.Tracker against the GitLab REST v4 API.
//
// Construction performs a fail-fast token presence check (no network call).
// The first Preflight call validates the token against GitLab itself; a
// successful preflight is cached for the lifetime of the Tracker so repeat
// calls are free, while failures are intentionally NOT cached so a transient
// startup glitch doesn't permanently brick the adapter.
type Tracker struct {
	http      *http.Client
	tokens    TokenSource
	baseURL   string
	userAgent string

	// preflightOK is the fast-path: once a Preflight succeeds, every
	// subsequent call short-circuits via atomic.Load without touching the
	// mutex. preflightMu serializes the one-time network call so concurrent
	// first-callers don't all fire GET /user against GitLab.
	preflightOK atomic.Bool
	preflightMu sync.Mutex
}

// New returns a Tracker. It fails fast when no token can be obtained so
// daemons crash at startup rather than at first issue lookup.
func New(opts Options) (*Tracker, error) {
	src := opts.Token
	if src == nil {
		return nil, ErrNoToken
	}
	if _, err := src.Token(context.Background()); err != nil {
		return nil, err
	}
	t := &Tracker{
		http:      opts.HTTPClient,
		tokens:    src,
		baseURL:   opts.BaseURL,
		userAgent: opts.UserAgent,
	}
	if t.http == nil {
		t.http = &http.Client{Timeout: 30 * time.Second}
	}
	if t.baseURL == "" {
		t.baseURL = defaultBaseURL
	}
	if t.userAgent == "" {
		t.userAgent = defaultUserAgent
	}
	return t, nil
}

// Statically assert Tracker satisfies the port.
var _ ports.Tracker = (*Tracker)(nil)

// ---------------------------------------------------------------------------
// Get
// ---------------------------------------------------------------------------

// glIssue is the subset of fields we read off the REST issue payload.
// Note: state is "opened" / "closed" (GitLab spelling, not GitHub's "open").
// Labels are returned as plain strings by default (no with_labels_details
// param), so a []string is sufficient.
type glIssue struct {
	IID         int      `json:"iid"`
	Title       string   `json:"title"`
	Description string   `json:"description"`
	State       string   `json:"state"`
	WebURL      string   `json:"web_url"`
	Labels      []string `json:"labels"`
	Assignees   []glUser `json:"assignees"`
}

type glUser struct {
	Username string `json:"username"`
}

func (t *Tracker) Get(ctx context.Context, id domain.TrackerID) (domain.Issue, error) {
	project, iid, err := t.parseID(id)
	if err != nil {
		return domain.Issue{}, err
	}
	path := fmt.Sprintf("/projects/%s/issues/%d", url.PathEscape(project), iid)

	resp, err := t.do(ctx, http.MethodGet, path, nil)
	if err != nil {
		return domain.Issue{}, err
	}
	var raw glIssue
	if err := json.Unmarshal(resp, &raw); err != nil {
		return domain.Issue{}, fmt.Errorf("gitlab tracker: decode issue: %w", err)
	}
	return issueFromGL(project, raw), nil
}

// issueFromGL projects a raw GitLab issue payload into the normalized
// domain.Issue. project is passed in because the TrackerID.Native shape is
// "group/project#iid" and we want the returned ID to round-trip through the
// same adapter even if the original caller used a zero Provider.
func issueFromGL(project string, raw glIssue) domain.Issue {
	labels := append([]string(nil), raw.Labels...)
	assignees := make([]string, 0, len(raw.Assignees))
	for _, a := range raw.Assignees {
		assignees = append(assignees, a.Username)
	}
	out := domain.Issue{
		ID: domain.TrackerID{
			Provider: domain.TrackerProviderGitLab,
			Native:   fmt.Sprintf("%s#%d", project, raw.IID),
		},
		Title:     raw.Title,
		Body:      raw.Description,
		State:     mapStateFromGitLab(raw.State, labels),
		URL:       raw.WebURL,
		Labels:    labels,
		Assignees: assignees,
	}
	if len(out.Labels) == 0 {
		out.Labels = nil
	}
	if len(out.Assignees) == 0 {
		out.Assignees = nil
	}
	return out
}

// mapStateFromGitLab projects GitLab's opened/closed + labels onto the
// normalized state. On closed issues a "cancelled" or "wontfix" label maps
// to cancelled; on open issues "in-review" wins over "in-progress" when
// both are present (the workflow is progress -> review -> done).
//
// Label matching is case-insensitive — GitLab label names are
// case-insensitive in the UI, so a user typing "In-Progress" should be
// recognized just as "in-progress" is.
func mapStateFromGitLab(state string, labels []string) domain.NormalizedIssueState {
	switch strings.ToLower(state) {
	case stateClosedGL:
		for _, l := range labels {
			switch strings.ToLower(l) {
			case labelCancelled, labelWontfix:
				return domain.IssueCancelled
			}
		}
		return domain.IssueDone
	}
	var hasProgress, hasReview bool
	for _, l := range labels {
		switch strings.ToLower(l) {
		case labelInProgress:
			hasProgress = true
		case labelInReview:
			hasReview = true
		}
	}
	switch {
	case hasReview:
		return domain.IssueInReview
	case hasProgress:
		return domain.IssueInProgress
	default:
		return domain.IssueOpen
	}
}

// ---------------------------------------------------------------------------
// List
// ---------------------------------------------------------------------------

// List returns issues for a project, filtered by state/labels/assignee_username.
// Pagination is intentionally NOT implemented in v1 — callers get one page
// bounded by ListFilter.Limit (default 30, max 100).
func (t *Tracker) List(ctx context.Context, repo domain.TrackerRepo, filter domain.ListFilter) ([]domain.Issue, error) {
	if repo.Provider != domain.TrackerProviderGitLab {
		return nil, fmt.Errorf("%w: provider=%q", ErrWrongProvider, repo.Provider)
	}
	project, err := parseGitLabRepo(repo.Native)
	if err != nil {
		return nil, err
	}

	q := url.Values{}
	switch filter.State {
	case domain.ListOpen:
		// GitLab uses "opened" — NOT "open" like GitHub.
		q.Set("state", "opened")
	case domain.ListClosed:
		q.Set("state", "closed")
	default:
		q.Set("state", "all")
	}
	if len(filter.Labels) > 0 {
		q.Set("labels", strings.Join(filter.Labels, ","))
	}
	if filter.Assignee != "" {
		// GitLab spells this assignee_username (the bare "assignee" param
		// expects a numeric user id, which we don't carry on the port).
		q.Set("assignee_username", filter.Assignee)
	}
	limit := filter.Limit
	if limit <= 0 {
		limit = defaultListLimit
	}
	if limit > maxListLimit {
		limit = maxListLimit
	}
	q.Set("per_page", strconv.Itoa(limit))

	path := fmt.Sprintf("/projects/%s/issues?%s", url.PathEscape(project), q.Encode())
	resp, err := t.do(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}
	var raw []glIssue
	if err := json.Unmarshal(resp, &raw); err != nil {
		return nil, fmt.Errorf("gitlab tracker: decode list: %w", err)
	}
	out := make([]domain.Issue, 0, len(raw))
	for _, r := range raw {
		out = append(out, issueFromGL(project, r))
	}
	return out, nil
}

// ---------------------------------------------------------------------------
// Preflight
// ---------------------------------------------------------------------------

// Preflight verifies the configured token is currently accepted by GitLab
// (one GET /user). It does NOT prove the token has the scope or visibility
// needed for any specific Get/List call — those may still fail with
// ErrAuthFailed even after a successful Preflight. The guarantee is "token
// exists and is valid against GitLab's identity endpoint", not "token can
// do everything the SM will ask of it." Per-project authorization is
// detected lazily at the first Get/List against that project.
//
// Successful checks are cached for the lifetime of the Tracker via a
// double-checked atomic+mutex pattern: the hot path is one atomic.Load with
// no contention; concurrent first-callers serialize on the mutex so only
// one GET /user is in flight. Failures are intentionally NOT cached so a
// transient startup glitch is recoverable on a subsequent call.
func (t *Tracker) Preflight(ctx context.Context) error {
	if t.preflightOK.Load() {
		return nil
	}
	t.preflightMu.Lock()
	defer t.preflightMu.Unlock()
	// Re-check after acquiring the lock — another goroutine may have raced
	// us through the network call and stored success while we were waiting.
	if t.preflightOK.Load() {
		return nil
	}
	if _, err := t.do(ctx, http.MethodGet, "/user", nil); err != nil {
		return err
	}
	t.preflightOK.Store(true)
	return nil
}

// ---------------------------------------------------------------------------
// HTTP plumbing
// ---------------------------------------------------------------------------

func (t *Tracker) do(ctx context.Context, method, path string, body any) ([]byte, error) {
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("gitlab tracker: encode body: %w", err)
		}
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, t.baseURL+path, rdr)
	if err != nil {
		return nil, fmt.Errorf("gitlab tracker: build request: %w", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", t.userAgent)
	tok, err := t.tokens.Token(ctx)
	if err != nil {
		return nil, err
	}
	// PRIVATE-TOKEN is GitLab's recommended header for personal-access /
	// project-access tokens. We intentionally do NOT also set
	// Authorization: Bearer — sending both would be redundant and the
	// recommended path is enough.
	req.Header.Set("PRIVATE-TOKEN", tok)

	resp, err := t.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("gitlab tracker: %s %s: %w", method, path, err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return respBody, nil
	}
	return respBody, classifyError(resp, respBody)
}

func classifyError(resp *http.Response, body []byte) error {
	msg := gitlabMessage(body)
	switch resp.StatusCode {
	case http.StatusNotFound:
		return fmt.Errorf("%w: %s", ErrNotFound, msg)
	case http.StatusTooManyRequests:
		// GitLab's canonical rate-limit status. Headers may include
		// RateLimit-Remaining / RateLimit-Reset / Retry-After (no X- prefix).
		return rateLimited(resp, msg)
	case http.StatusUnauthorized:
		// 401 is unambiguously an auth failure on GitLab.
		return fmt.Errorf("%w: %s", ErrAuthFailed, msg)
	case http.StatusForbidden:
		// On GitLab, 403 without rate-limit headers is permission denied
		// (token lacks the right scope, project not visible). 403 with
		// rate-limit signals is rare but handle it the same way as 429
		// to be safe.
		if isRateLimited(resp, msg) {
			return rateLimited(resp, msg)
		}
		return fmt.Errorf("%w: %s", ErrAuthFailed, msg)
	}
	return fmt.Errorf("gitlab tracker: %d %s", resp.StatusCode, msg)
}

func isRateLimited(resp *http.Response, msg string) bool {
	if rem := resp.Header.Get("RateLimit-Remaining"); rem != "" {
		if n, err := strconv.Atoi(rem); err == nil && n == 0 {
			return true
		}
	}
	if resp.Header.Get("Retry-After") != "" {
		return true
	}
	low := strings.ToLower(msg)
	return strings.Contains(low, "rate limit") || strings.Contains(low, "retry later") || strings.Contains(low, "too many requests")
}

func rateLimited(resp *http.Response, msg string) error {
	e := &RateLimitError{Message: msg}
	if reset := resp.Header.Get("RateLimit-Reset"); reset != "" {
		if sec, err := strconv.ParseInt(reset, 10, 64); err == nil && sec > 0 {
			e.ResetAt = time.Unix(sec, 0)
		}
	}
	// Retry-After: v1 parses integer-seconds only. RFC 7231 also allows an
	// HTTP-date form; GitLab's current implementation uses seconds, so the
	// date form is out of scope for v1 and a future caller hitting it just
	// won't populate RetryAfter — they can still back off via ResetAt or
	// generic retry policy.
	if ra := resp.Header.Get("Retry-After"); ra != "" {
		if sec, err := strconv.Atoi(ra); err == nil && sec >= 0 {
			e.RetryAfter = time.Duration(sec) * time.Second
		}
	}
	return e
}

func gitlabMessage(body []byte) string {
	// GitLab error bodies vary: sometimes {"message":"..."}, sometimes
	// {"error":"..."}, sometimes a nested {"message":{"base":["..."]}}.
	// Try the two flat-string forms first; on failure fall back to the
	// raw body trimmed.
	var flat struct {
		Message string `json:"message"`
		Error   string `json:"error"`
	}
	if json.Unmarshal(body, &flat) == nil {
		if flat.Message != "" {
			return flat.Message
		}
		if flat.Error != "" {
			return flat.Error
		}
	}
	return strings.TrimSpace(string(body))
}

// ---------------------------------------------------------------------------
// ID parsing
// ---------------------------------------------------------------------------

func (t *Tracker) parseID(id domain.TrackerID) (project string, iid int, err error) {
	// Strict: the Session Manager picks an adapter by Provider, so reaching
	// this adapter with a non-gitlab Provider is a routing bug, not user
	// input. Empty Provider is treated the same way.
	if id.Provider != domain.TrackerProviderGitLab {
		return "", 0, fmt.Errorf("%w: provider=%q", ErrWrongProvider, id.Provider)
	}
	return parseGitLabID(id.Native)
}

// parseGitLabID accepts "group/project#iid" — including subgroup paths
// "group/sub/.../project#iid" of arbitrary depth — and returns the project
// path and IID. Bare numbers and forms without the # separator are
// intentionally rejected so the rest of the system has one canonical id
// shape.
func parseGitLabID(native string) (project string, iid int, err error) {
	hash := strings.IndexByte(native, '#')
	if hash < 0 {
		return "", 0, fmt.Errorf("%w: missing #iid", ErrBadID)
	}
	project = native[:hash]
	numPart := native[hash+1:]
	if err := validateProjectPath(project); err != nil {
		return "", 0, err
	}
	n, parseErr := strconv.Atoi(numPart)
	if parseErr != nil || n <= 0 {
		return "", 0, fmt.Errorf("%w: bad iid %q", ErrBadID, numPart)
	}
	return project, n, nil
}

// parseGitLabRepo accepts "group/project" or "group/.../project" and
// rejects empty segments, embedded "#", and whitespace.
func parseGitLabRepo(native string) (string, error) {
	if native == "" {
		return "", fmt.Errorf("%w: empty repo", ErrBadID)
	}
	if err := validateProjectPath(native); err != nil {
		return "", err
	}
	return native, nil
}

// validateProjectPath enforces the shared rules for both Get and List
// inputs: at least two non-empty segments, no embedded whitespace or "#".
// Subgroup nesting of arbitrary depth is allowed.
func validateProjectPath(p string) error {
	if p == "" {
		return fmt.Errorf("%w: empty project path", ErrBadID)
	}
	if strings.ContainsAny(p, "# \t\n\r") {
		return fmt.Errorf("%w: invalid characters in project path %q", ErrBadID, p)
	}
	segs := strings.Split(p, "/")
	if len(segs) < 2 {
		return fmt.Errorf("%w: project path needs at least group/project", ErrBadID)
	}
	for _, s := range segs {
		if s == "" {
			return fmt.Errorf("%w: empty segment in project path %q", ErrBadID, p)
		}
	}
	return nil
}
