package linear

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

const (
	// BaseURL is the host root; the adapter appends graphqlPath for every
	// request. Treating BaseURL this way mirrors the github adapter (where
	// BaseURL is "https://api.github.com" and routes are joined onto it)
	// so tests can point at an httptest server URL with no special-casing.
	defaultBaseURL   = "https://api.linear.app"
	graphqlPath      = "/graphql"
	defaultUserAgent = "ao-agent-orchestrator/tracker-linear"

	// Linear's pagination default and hard cap on the first: argument.
	defaultListLimit = 50
	maxListLimit     = 250

	// extensions.type values Linear sets on GraphQL errors. Spelled
	// lowercase with spaces, matching @linear/sdk/error.ts at HEAD.
	extTypeAuthError    = "authentication error"
	extTypeRatelimited  = "ratelimited"
	extTypeFeatureGated = "feature not accessible"
	extTypeForbidden    = "forbidden"
)

// Sentinel errors. Same shape as the github adapter so the SM can match on
// errors.Is across providers without per-adapter knowledge.
var (
	ErrNotFound      = errors.New("linear tracker: issue not found")
	ErrRateLimited   = errors.New("linear tracker: rate limited")
	ErrAuthFailed    = errors.New("linear tracker: authentication failed")
	ErrWrongProvider = errors.New("linear tracker: id is not a linear tracker id")
	ErrBadID         = errors.New("linear tracker: malformed native id")
)

// RateLimitError is returned when Linear reports the request was
// rate-limited. Callers wanting to back off can pull ResetAt / RetryAfter
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
		return "linear tracker: rate limited: " + e.Message
	}
	return ErrRateLimited.Error()
}

func (e *RateLimitError) Is(target error) bool { return target == ErrRateLimited }

// Options configures a Tracker. Token is the only required field in
// production; tests inject HTTPClient and BaseURL to point at httptest.
type Options struct {
	Token      TokenSource
	HTTPClient *http.Client
	BaseURL    string
	UserAgent  string
}

// Tracker implements ports.Tracker against Linear's GraphQL API.
//
// Construction performs a fail-fast token presence check (no network
// call). Preflight verifies the token against Linear itself; success is
// cached for the lifetime of the Tracker, failures are not.
//
// The team-key → UUID resolution required by List(team-scoped) is cached
// in teamUUIDs guarded by teamMu so concurrent first-callers serialize on
// the lookup and subsequent calls skip the network entirely.
type Tracker struct {
	http      *http.Client
	tokens    TokenSource
	baseURL   string
	userAgent string

	preflightOK atomic.Bool
	preflightMu sync.Mutex

	teamMu    sync.Mutex
	teamUUIDs map[string]string
}

// New returns a Tracker. Fails fast when no token can be obtained.
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
		teamUUIDs: make(map[string]string),
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

var _ ports.Tracker = (*Tracker)(nil)

// ---------------------------------------------------------------------------
// Get
// ---------------------------------------------------------------------------

const issueQuery = `query Issue($id: String!) {
  issue(id: $id) {
    identifier
    title
    description
    url
    state { type }
    labels { nodes { name } }
    assignees { nodes { name } }
  }
}`

type linearIssue struct {
	Identifier  string `json:"identifier"`
	Title       string `json:"title"`
	Description string `json:"description"`
	URL         string `json:"url"`
	State       struct {
		Type string `json:"type"`
	} `json:"state"`
	Labels struct {
		Nodes []struct {
			Name string `json:"name"`
		} `json:"nodes"`
	} `json:"labels"`
	Assignees struct {
		Nodes []struct {
			Name string `json:"name"`
		} `json:"nodes"`
	} `json:"assignees"`
}

func (t *Tracker) Get(ctx context.Context, id domain.TrackerID) (domain.Issue, error) {
	if id.Provider != domain.TrackerProviderLinear {
		return domain.Issue{}, fmt.Errorf("%w: provider=%q", ErrWrongProvider, id.Provider)
	}
	if strings.TrimSpace(id.Native) == "" {
		return domain.Issue{}, fmt.Errorf("%w: empty native id", ErrBadID)
	}

	var data struct {
		Issue *linearIssue `json:"issue"`
	}
	if err := t.do(ctx, issueQuery, map[string]any{"id": id.Native}, &data); err != nil {
		return domain.Issue{}, err
	}
	if data.Issue == nil {
		return domain.Issue{}, fmt.Errorf("%w: %s", ErrNotFound, id.Native)
	}
	return issueFromLinear(id.Native, data.Issue), nil
}

// issueFromLinear projects a Linear issue payload into the normalized
// domain.Issue. The caller's original Native is echoed on the returned ID
// so a UUID-style lookup round-trips faithfully — we don't substitute the
// short identifier from Linear's response.
func issueFromLinear(native string, raw *linearIssue) domain.Issue {
	out := domain.Issue{
		ID: domain.TrackerID{
			Provider: domain.TrackerProviderLinear,
			Native:   native,
		},
		Title: raw.Title,
		Body:  raw.Description,
		State: mapStateFromLinear(raw.State.Type),
		URL:   raw.URL,
	}
	if n := len(raw.Labels.Nodes); n > 0 {
		out.Labels = make([]string, n)
		for i, l := range raw.Labels.Nodes {
			out.Labels[i] = l.Name
		}
	}
	if n := len(raw.Assignees.Nodes); n > 0 {
		out.Assignees = make([]string, n)
		for i, a := range raw.Assignees.Nodes {
			out.Assignees[i] = a.Name
		}
	}
	return out
}

func mapStateFromLinear(linearType string) domain.NormalizedIssueState {
	switch linearType {
	case "completed":
		return domain.IssueDone
	case "canceled":
		return domain.IssueCancelled
	case "started":
		return domain.IssueInProgress
	default:
		return domain.IssueOpen
	}
}

// ---------------------------------------------------------------------------
// List
// ---------------------------------------------------------------------------

const teamLookupQuery = `query TeamByKey($key: String!) {
  teams(filter: {key: {eq: $key}}, first: 1) {
    nodes { id key }
  }
}`

const issuesQuery = `query Issues($filter: IssueFilter, $first: Int!) {
  issues(filter: $filter, first: $first) {
    nodes {
      identifier
      title
      description
      url
      state { type }
      labels { nodes { name } }
      assignees { nodes { name } }
    }
  }
}`

func (t *Tracker) List(ctx context.Context, repo domain.TrackerRepo, filter domain.ListFilter) ([]domain.Issue, error) {
	if repo.Provider != domain.TrackerProviderLinear {
		return nil, fmt.Errorf("%w: provider=%q", ErrWrongProvider, repo.Provider)
	}

	filt := map[string]any{}

	// Team scoping: empty Native is workspace-wide; otherwise resolve the
	// team key to a UUID (lazily, with caching) and add it to the filter.
	if key := strings.TrimSpace(repo.Native); key != "" {
		uuid, err := t.resolveTeamUUID(ctx, key)
		if err != nil {
			return nil, err
		}
		filt["team"] = map[string]any{"id": map[string]any{"eq": uuid}}
	}

	switch filter.State {
	case domain.ListOpen:
		filt["state"] = map[string]any{"type": map[string]any{
			"in": []string{"unstarted", "started", "triage", "backlog"},
		}}
	case domain.ListClosed:
		filt["state"] = map[string]any{"type": map[string]any{
			"in": []string{"completed", "canceled"},
		}}
	}
	if filter.Assignee != "" {
		filt["assignee"] = map[string]any{"name": map[string]any{"eq": filter.Assignee}}
	}
	if len(filter.Labels) > 0 {
		filt["labels"] = map[string]any{"name": map[string]any{"in": filter.Labels}}
	}

	limit := filter.Limit
	if limit <= 0 {
		limit = defaultListLimit
	}
	if limit > maxListLimit {
		limit = maxListLimit
	}

	vars := map[string]any{"first": limit}
	if len(filt) > 0 {
		vars["filter"] = filt
	}

	var data struct {
		Issues struct {
			Nodes []linearIssue `json:"nodes"`
		} `json:"issues"`
	}
	if err := t.do(ctx, issuesQuery, vars, &data); err != nil {
		return nil, err
	}
	out := make([]domain.Issue, 0, len(data.Issues.Nodes))
	for i := range data.Issues.Nodes {
		// Echo back Linear's identifier as Native — this is the list
		// case, where the caller has no pre-existing string to preserve.
		raw := &data.Issues.Nodes[i]
		out = append(out, issueFromLinear(raw.Identifier, raw))
	}
	return out, nil
}

// resolveTeamUUID maps a team key (e.g. "ABC") to its UUID, caching the
// result. The cache is per-Tracker and grows monotonically — team keys
// don't churn, so this is fine in practice.
//
// Concurrency: the mutex is released across the network round-trip, so
// concurrent first-callers for DIFFERENT keys make their teams() lookups
// in parallel. Concurrent first-callers for the SAME key may both make
// the request — the result is idempotent and the second insert is a
// no-op overwrite. This is the simple-and-correct shape; bringing in
// per-key singleflight wasn't justified by v1's call volume.
func (t *Tracker) resolveTeamUUID(ctx context.Context, key string) (string, error) {
	t.teamMu.Lock()
	if uuid, ok := t.teamUUIDs[key]; ok {
		t.teamMu.Unlock()
		return uuid, nil
	}
	t.teamMu.Unlock()

	var data struct {
		Teams struct {
			Nodes []struct {
				ID  string `json:"id"`
				Key string `json:"key"`
			} `json:"nodes"`
		} `json:"teams"`
	}
	if err := t.do(ctx, teamLookupQuery, map[string]any{"key": key}, &data); err != nil {
		return "", err
	}
	if len(data.Teams.Nodes) == 0 {
		return "", fmt.Errorf("%w: team key=%q", ErrNotFound, key)
	}
	uuid := data.Teams.Nodes[0].ID

	t.teamMu.Lock()
	t.teamUUIDs[key] = uuid
	t.teamMu.Unlock()
	return uuid, nil
}

// ---------------------------------------------------------------------------
// Preflight
// ---------------------------------------------------------------------------

const viewerQuery = `query { viewer { id } }`

// Preflight verifies the configured token is currently accepted by Linear
// (one viewer query). It does NOT prove the token has access to any
// specific workspace, team, or issue — those may still fail with
// ErrAuthFailed even after a successful Preflight. Per-resource auth is
// detected lazily at the first Get/List against the resource.
//
// Caching mirrors the github adapter: atomic.Bool fast path, sync.Mutex
// serializes the one-time network call, failures are NOT cached.
func (t *Tracker) Preflight(ctx context.Context) error {
	if t.preflightOK.Load() {
		return nil
	}
	t.preflightMu.Lock()
	defer t.preflightMu.Unlock()
	if t.preflightOK.Load() {
		return nil
	}
	var data struct {
		Viewer struct {
			ID string `json:"id"`
		} `json:"viewer"`
	}
	if err := t.do(ctx, viewerQuery, nil, &data); err != nil {
		return err
	}
	t.preflightOK.Store(true)
	return nil
}

// ---------------------------------------------------------------------------
// GraphQL plumbing
// ---------------------------------------------------------------------------

// gqlResponse is the standard GraphQL envelope. data is left as RawMessage
// so do() can decode it into the caller's typed struct only after
// confirming there are no errors worth surfacing.
type gqlResponse struct {
	Data   json.RawMessage `json:"data"`
	Errors []gqlError      `json:"errors"`
}

type gqlError struct {
	Message    string         `json:"message"`
	Path       []any          `json:"path,omitempty"`
	Extensions map[string]any `json:"extensions,omitempty"`
}

// do posts a GraphQL request and decodes data into out. It maps Linear's
// error surface — both HTTP-level and errors[].extensions.type — onto the
// adapter's sentinels. out may be nil for queries whose data the caller
// doesn't need (Preflight uses this).
func (t *Tracker) do(ctx context.Context, query string, variables map[string]any, out any) error {
	body := map[string]any{"query": query}
	if variables != nil {
		body["variables"] = variables
	}
	buf, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("linear tracker: encode body: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.baseURL+graphqlPath, bytes.NewReader(buf))
	if err != nil {
		return fmt.Errorf("linear tracker: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", t.userAgent)

	tok, err := t.tokens.Token(ctx)
	if err != nil {
		return err
	}
	// CRITICAL: NO "Bearer " prefix. Personal API keys go raw. See doc.go.
	req.Header.Set("Authorization", tok)

	resp, err := t.http.Do(req)
	if err != nil {
		return fmt.Errorf("linear tracker: POST %s%s: %w", t.baseURL, graphqlPath, err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)

	var env gqlResponse
	// A non-JSON body on a non-2xx is still meaningful — fall back to
	// HTTP-status classification when we can't parse the envelope.
	jsonOK := len(respBody) > 0 && json.Unmarshal(respBody, &env) == nil

	// Priority: recognized extensions.type wins, because Linear surfaces
	// the most actionable category that way (e.g. 200 + ratelimited).
	// Failing that, HTTP status code carries the next-most-specific
	// signal (401/429/5xx). A leftover errors[] with no recognized type
	// on an HTTP-200 response surfaces a generic graphql error.
	if jsonOK && len(env.Errors) > 0 {
		if err := classifyKnownGraphQLError(resp, env.Errors); err != nil {
			return err
		}
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return classifyHTTPStatus(resp, respBody)
	}

	if jsonOK && len(env.Errors) > 0 {
		first := env.Errors[0]
		if first.Message != "" {
			return fmt.Errorf("linear tracker: graphql error: %s", first.Message)
		}
		return fmt.Errorf("linear tracker: graphql error with no message")
	}

	if out == nil {
		return nil
	}
	if !jsonOK {
		return fmt.Errorf("linear tracker: decode envelope: invalid JSON body")
	}
	if len(env.Data) == 0 || string(env.Data) == "null" {
		return fmt.Errorf("linear tracker: empty data field on success response")
	}
	if err := json.Unmarshal(env.Data, out); err != nil {
		return fmt.Errorf("linear tracker: decode data: %w", err)
	}
	return nil
}

// classifyKnownGraphQLError walks errors[] and returns a sentinel-wrapped
// error iff at least one entry carries a recognized extensions.type. A nil
// return means "no known type found" — the caller falls back to HTTP
// status code, then to a generic graphql-error wrap as a last resort.
//
// The type string is normalized via TrimSpace+ToLower before matching.
// Linear's @linear/sdk codes are documented as lowercase ("authentication
// error", "ratelimited", etc.), but the cost of normalizing is zero and
// the cost of a silent miscategorization (e.g. ErrAuthFailed → generic
// graphql-error → SM doesn't recover) is high.
func classifyKnownGraphQLError(resp *http.Response, errs []gqlError) error {
	for _, e := range errs {
		raw, _ := e.Extensions["type"].(string)
		typ := strings.ToLower(strings.TrimSpace(raw))
		msg := e.Message
		if msg == "" {
			if upm, ok := e.Extensions["userPresentableMessage"].(string); ok {
				msg = upm
			} else {
				msg = raw
			}
		}
		switch typ {
		case extTypeAuthError:
			return fmt.Errorf("%w: %s", ErrAuthFailed, msg)
		case extTypeRatelimited:
			return rateLimited(resp, msg)
		case extTypeFeatureGated, extTypeForbidden:
			// Both mean "this token can't satisfy this request" — fold
			// onto ErrAuthFailed so the SM's recovery path is uniform.
			return fmt.Errorf("%w: %s", ErrAuthFailed, msg)
		}
	}
	return nil
}

// classifyHTTPStatus handles the non-2xx fallback for cases where Linear
// returns a status code without a parsable errors[] envelope (proxy
// errors, edge 429s, etc.).
func classifyHTTPStatus(resp *http.Response, body []byte) error {
	msg := strings.TrimSpace(string(body))
	switch resp.StatusCode {
	case http.StatusUnauthorized, http.StatusForbidden:
		return fmt.Errorf("%w: %s", ErrAuthFailed, msg)
	case http.StatusTooManyRequests:
		return rateLimited(resp, msg)
	}
	return fmt.Errorf("linear tracker: %d %s", resp.StatusCode, msg)
}

func rateLimited(resp *http.Response, msg string) error {
	e := &RateLimitError{Message: msg}
	if reset := resp.Header.Get("X-RateLimit-Requests-Reset"); reset != "" {
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
