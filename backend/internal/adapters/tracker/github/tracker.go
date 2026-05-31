package github

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
	defaultBaseURL   = "https://api.github.com"
	defaultUserAgent = "ao-agent-orchestrator/tracker-github"

	// Status labels used by humans (and other tooling) on GitHub Issues.
	// Get's reverse mapping recognizes them so an externally-labeled issue
	// reports as in_progress / review. The adapter does NOT write these
	// labels in v1 — see issue #40 for the write-side work.
	labelInProgress = "in-progress"
	labelInReview   = "in-review"

	stateClosedGH = "closed"
	reasonNotPlan = "not_planned"

	// List pagination — GitHub's per_page maxes at 100. We default to 30
	// (matching the legacy gh CLI default) when the caller passes 0.
	defaultListLimit = 30
	maxListLimit     = 100

	// bootTokenProbeTimeout bounds the construction-time token probe so a
	// hung `gh auth token` fallback (broken shell, unhealthy PATH) cannot
	// stall daemon startup. gh is sub-100ms on a healthy box; 5s gives
	// ~50x headroom without making daemon-start feel slow.
	bootTokenProbeTimeout = 5 * time.Second
)

// Sentinel errors. Adapter-level callers should match on these via
// errors.Is; the orchestrator's lifecycle code is intentionally insulated
// from raw HTTP status codes.
var (
	ErrNotFound      = errors.New("github tracker: issue not found")
	ErrRateLimited   = errors.New("github tracker: rate limited")
	ErrAuthFailed    = errors.New("github tracker: authentication failed")
	ErrWrongProvider = errors.New("github tracker: id is not a github tracker id")
	ErrBadID         = errors.New("github tracker: malformed native id")
)

// RateLimitError is returned when GitHub reports the request was rate-limited.
// Callers that want to back off intelligently can extract ResetAt /
// RetryAfter via errors.As; callers that only need the category can use
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
		return "github tracker: rate limited: " + e.Message
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

// Tracker implements ports.Tracker against the GitHub REST API.
//
// Construction performs a fail-fast token presence check (no network call).
// The first Preflight call validates the token against GitHub itself; a
// successful preflight is cached for the lifetime of the Tracker so repeat
// calls are free, while failures are intentionally NOT cached so a
// transient startup glitch doesn't permanently brick the adapter.
type Tracker struct {
	http      *http.Client
	tokens    TokenSource
	baseURL   string
	userAgent string

	// preflightOK is the fast-path: once a Preflight succeeds, every
	// subsequent call short-circuits via atomic.Load without touching the
	// mutex. preflightMu serializes the one-time network call so concurrent
	// first-callers don't all fire GET /user against GitHub.
	preflightOK atomic.Bool
	preflightMu sync.Mutex
}

// New returns a Tracker. It fails fast when no token can be obtained so
// daemons crash at startup rather than at first issue lookup. The token
// probe is bounded by a short deadline so a hung `gh auth token` fallback
// (broken shell, unhealthy PATH) cannot stall daemon startup.
func New(opts Options) (*Tracker, error) {
	src := opts.Token
	if src == nil {
		return nil, ErrNoToken
	}
	bootCtx, cancel := context.WithTimeout(context.Background(), bootTokenProbeTimeout)
	defer cancel()
	if _, err := src.Token(bootCtx); err != nil {
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

// Statically assert Tracker satisfies the port. If this stops compiling, the
// port shape changed and the adapter needs to follow.
var _ ports.Tracker = (*Tracker)(nil)

// ---------------------------------------------------------------------------
// Get
// ---------------------------------------------------------------------------

// ghIssue is the subset of fields we read off the REST issue payload.
// PullRequest is present (non-nil) iff GitHub considers this row a PR —
// the /repos/{o}/{r}/issues endpoint conflates the two. List uses it to
// filter PRs out client-side so the SM never sees a PR number as an issue.
type ghIssue struct {
	Number      int              `json:"number"`
	Title       string           `json:"title"`
	Body        string           `json:"body"`
	State       string           `json:"state"`
	StateReason string           `json:"state_reason"`
	HTMLURL     string           `json:"html_url"`
	Labels      []ghLabel        `json:"labels"`
	Assignees   []ghUser         `json:"assignees"`
	PullRequest *json.RawMessage `json:"pull_request,omitempty"`
}

type ghLabel struct {
	Name string `json:"name"`
}

type ghUser struct {
	Login string `json:"login"`
}

func (t *Tracker) Get(ctx context.Context, id domain.TrackerID) (domain.Issue, error) {
	owner, repo, number, err := t.parseID(id)
	if err != nil {
		return domain.Issue{}, err
	}
	path := fmt.Sprintf("/repos/%s/%s/issues/%d", owner, repo, number)

	resp, err := t.do(ctx, http.MethodGet, path, nil)
	if err != nil {
		return domain.Issue{}, err
	}
	var raw ghIssue
	if err := json.Unmarshal(resp, &raw); err != nil {
		return domain.Issue{}, fmt.Errorf("github tracker: decode issue: %w", err)
	}
	return issueFromGH(owner, repo, raw), nil
}

// issueFromGH projects a raw GitHub issue payload into the normalized
// domain.Issue. owner and repo are passed in because the TrackerID.Native
// shape is "owner/repo#N" and we want the returned ID to round-trip
// through the same adapter even if the original caller used a zero
// Provider.
func issueFromGH(owner, repo string, raw ghIssue) domain.Issue {
	labels := make([]string, 0, len(raw.Labels))
	for _, l := range raw.Labels {
		labels = append(labels, l.Name)
	}
	assignees := make([]string, 0, len(raw.Assignees))
	for _, a := range raw.Assignees {
		assignees = append(assignees, a.Login)
	}
	out := domain.Issue{
		ID: domain.TrackerID{
			Provider: domain.TrackerProviderGitHub,
			Native:   fmt.Sprintf("%s/%s#%d", owner, repo, raw.Number),
		},
		Title:     raw.Title,
		Body:      raw.Body,
		State:     mapStateFromGitHub(raw.State, raw.StateReason, labels),
		URL:       raw.HTMLURL,
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

// mapStateFromGitHub projects GitHub's open/closed + state_reason + labels
// surface onto the normalized state. "in-review" wins over "in-progress"
// when both labels are present (the workflow is progress -> review -> done).
func mapStateFromGitHub(state, reason string, labels []string) domain.NormalizedIssueState {
	switch strings.ToLower(state) {
	case stateClosedGH:
		if strings.EqualFold(reason, reasonNotPlan) {
			return domain.IssueCancelled
		}
		return domain.IssueDone
	}
	var hasProgress, hasReview bool
	for _, l := range labels {
		switch l {
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

// List returns issues for a repo, filtered by state/labels/assignee. PRs
// that GitHub's /issues endpoint conflates into the response are filtered
// out client-side. Pagination is intentionally NOT implemented in v1 —
// callers get one page bounded by ListFilter.Limit (default 30, max 100).
func (t *Tracker) List(ctx context.Context, repo domain.TrackerRepo, filter domain.ListFilter) ([]domain.Issue, error) {
	if repo.Provider != domain.TrackerProviderGitHub {
		return nil, fmt.Errorf("%w: provider=%q", ErrWrongProvider, repo.Provider)
	}
	owner, repoName, err := parseGitHubRepo(repo.Native)
	if err != nil {
		return nil, err
	}

	q := url.Values{}
	switch filter.State {
	case domain.ListOpen:
		q.Set("state", "open")
	case domain.ListClosed:
		q.Set("state", "closed")
	default:
		q.Set("state", "all")
	}
	if len(filter.Labels) > 0 {
		q.Set("labels", strings.Join(filter.Labels, ","))
	}
	if filter.Assignee != "" {
		q.Set("assignee", filter.Assignee)
	}
	limit := filter.Limit
	if limit <= 0 {
		limit = defaultListLimit
	}
	if limit > maxListLimit {
		limit = maxListLimit
	}
	q.Set("per_page", strconv.Itoa(limit))

	path := fmt.Sprintf("/repos/%s/%s/issues?%s", owner, repoName, q.Encode())
	resp, err := t.do(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}
	var raw []ghIssue
	if err := json.Unmarshal(resp, &raw); err != nil {
		return nil, fmt.Errorf("github tracker: decode list: %w", err)
	}
	out := make([]domain.Issue, 0, len(raw))
	for _, r := range raw {
		if r.PullRequest != nil {
			continue
		}
		out = append(out, issueFromGH(owner, repoName, r))
	}
	return out, nil
}

// ---------------------------------------------------------------------------
// Preflight
// ---------------------------------------------------------------------------

// Preflight verifies the configured token is currently accepted by GitHub
// (one GET /user). It does NOT prove the token has the repo scope or
// visibility needed for any specific Get/List call — those may still fail
// with ErrAuthFailed even after a successful Preflight. The guarantee is
// "token exists and is valid against GitHub's identity endpoint", not
// "token can do everything the SM will ask of it." Per-repo authorization
// is detected lazily at the first Get/List against that repo.
//
// Successful checks are cached for the lifetime of the Tracker via a
// double-checked atomic+mutex pattern: the hot path is one atomic.Load
// with no contention; concurrent first-callers serialize on the mutex so
// only one GET /user is in flight. Failures are intentionally NOT cached
// so a transient startup glitch is recoverable on a subsequent call.
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
			return nil, fmt.Errorf("github tracker: encode body: %w", err)
		}
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, t.baseURL+path, rdr)
	if err != nil {
		return nil, fmt.Errorf("github tracker: build request: %w", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", t.userAgent)
	tok, err := t.tokens.Token(ctx)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+tok)

	resp, err := t.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("github tracker: %s %s: %w", method, path, err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return respBody, nil
	}
	return respBody, classifyError(resp, respBody)
}

func classifyError(resp *http.Response, body []byte) error {
	msg := githubMessage(body)
	switch resp.StatusCode {
	case http.StatusNotFound:
		return fmt.Errorf("%w: %s", ErrNotFound, msg)
	case http.StatusTooManyRequests:
		return rateLimited(resp, msg)
	case http.StatusUnauthorized:
		// 401 is unambiguously an auth failure. GitHub never uses 401 for
		// rate limiting; that's always 403 or 429.
		return fmt.Errorf("%w: %s", ErrAuthFailed, msg)
	case http.StatusForbidden:
		// GitHub returns 403 for primary rate-limit exhaustion, for
		// secondary/abuse limits, and for genuine auth/permission failures.
		// Disambiguate by signal: primary limit sets X-RateLimit-Remaining=0;
		// secondary/abuse sets Retry-After (often without the Remaining
		// header); either case mentions "rate limit" / "abuse" in the body.
		// Everything else is an auth/permission failure (token missing the
		// right scope, repo not visible to this token, etc).
		if isRateLimited(resp, msg) {
			return rateLimited(resp, msg)
		}
		return fmt.Errorf("%w: %s", ErrAuthFailed, msg)
	}
	return fmt.Errorf("github tracker: %d %s", resp.StatusCode, msg)
}

func isRateLimited(resp *http.Response, msg string) bool {
	if rem := resp.Header.Get("X-RateLimit-Remaining"); rem != "" {
		if n, err := strconv.Atoi(rem); err == nil && n == 0 {
			return true
		}
	}
	if resp.Header.Get("Retry-After") != "" {
		return true
	}
	low := strings.ToLower(msg)
	return strings.Contains(low, "rate limit") || strings.Contains(low, "abuse detection")
}

func rateLimited(resp *http.Response, msg string) error {
	e := &RateLimitError{Message: msg}
	if reset := resp.Header.Get("X-RateLimit-Reset"); reset != "" {
		if sec, err := strconv.ParseInt(reset, 10, 64); err == nil && sec > 0 {
			e.ResetAt = time.Unix(sec, 0)
		}
	}
	if ra := resp.Header.Get("Retry-After"); ra != "" {
		if sec, err := strconv.Atoi(ra); err == nil && sec >= 0 {
			e.RetryAfter = time.Duration(sec) * time.Second
		}
	}
	return e
}

func githubMessage(body []byte) string {
	var p struct {
		Message string `json:"message"`
	}
	if json.Unmarshal(body, &p) == nil && p.Message != "" {
		return p.Message
	}
	return strings.TrimSpace(string(body))
}

// ---------------------------------------------------------------------------
// ID parsing
// ---------------------------------------------------------------------------

func (t *Tracker) parseID(id domain.TrackerID) (owner, repo string, number int, err error) {
	// Strict: the Session Manager picks an adapter by Provider, so reaching
	// this adapter with a non-github Provider is a routing bug, not user
	// input. Empty Provider is treated the same way — it would round-trip
	// to an Issue whose ID can't be re-routed.
	if id.Provider != domain.TrackerProviderGitHub {
		return "", "", 0, fmt.Errorf("%w: provider=%q", ErrWrongProvider, id.Provider)
	}
	return parseGitHubID(id.Native)
}

// parseGitHubID accepts "owner/repo#NUM" and returns the three components.
// Forms like "owner/repo/issues/NUM" or bare numbers are intentionally
// rejected so the rest of the system has one canonical id shape.
func parseGitHubID(native string) (owner, repo string, number int, err error) {
	hash := strings.IndexByte(native, '#')
	if hash < 0 {
		return "", "", 0, fmt.Errorf("%w: missing #issue", ErrBadID)
	}
	repoPart := native[:hash]
	numPart := native[hash+1:]
	slash := strings.IndexByte(repoPart, '/')
	if slash < 0 {
		return "", "", 0, fmt.Errorf("%w: missing owner/repo separator", ErrBadID)
	}
	owner = repoPart[:slash]
	repo = repoPart[slash+1:]
	if owner == "" || repo == "" {
		return "", "", 0, fmt.Errorf("%w: empty owner or repo", ErrBadID)
	}
	n, parseErr := strconv.Atoi(numPart)
	if parseErr != nil || n <= 0 {
		return "", "", 0, fmt.Errorf("%w: bad issue number %q", ErrBadID, numPart)
	}
	return owner, repo, n, nil
}

// parseGitHubRepo accepts "owner/repo" and rejects empty segments,
// embedded slashes, "#", and whitespace. Leading dots are kept legal —
// "owner/.github" is a real GitHub convention for repo-level config repos.
func parseGitHubRepo(native string) (owner, repo string, err error) {
	if native == "" {
		return "", "", fmt.Errorf("%w: empty repo", ErrBadID)
	}
	slash := strings.IndexByte(native, '/')
	if slash < 0 {
		return "", "", fmt.Errorf("%w: missing owner/repo separator", ErrBadID)
	}
	owner = native[:slash]
	repo = native[slash+1:]
	if owner == "" || repo == "" {
		return "", "", fmt.Errorf("%w: empty owner or repo segment", ErrBadID)
	}
	if strings.ContainsAny(owner, "/# \t\n\r") {
		return "", "", fmt.Errorf("%w: invalid owner segment %q", ErrBadID, owner)
	}
	if strings.ContainsAny(repo, "/# \t\n\r") {
		return "", "", fmt.Errorf("%w: invalid repo segment %q", ErrBadID, repo)
	}
	return owner, repo, nil
}
