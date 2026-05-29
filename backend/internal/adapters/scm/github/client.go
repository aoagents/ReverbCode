package github

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

const (
	defaultRESTBaseURL = "https://api.github.com"
	defaultGraphQLURL  = "https://api.github.com/graphql"
	defaultHost        = "github.com"
)

type Client struct {
	httpClient *http.Client
	tokens     TokenSource
	restBase   string
	graphqlURL string
	userAgent  string
}

type ClientOptions struct {
	HTTPClient *http.Client
	Token      TokenSource
	RESTBase   string
	GraphQLURL string
	UserAgent  string
}

func NewClient(opts ClientOptions) *Client {
	c := &Client{httpClient: opts.HTTPClient, tokens: opts.Token, restBase: opts.RESTBase, graphqlURL: opts.GraphQLURL, userAgent: opts.UserAgent}
	if c.httpClient == nil {
		c.httpClient = http.DefaultClient
	}
	if c.tokens == nil {
		c.tokens = &GHTokenSource{}
	}
	if c.restBase == "" {
		c.restBase = defaultRESTBaseURL
	}
	if c.graphqlURL == "" {
		c.graphqlURL = defaultGraphQLURL
	}
	if c.userAgent == "" {
		c.userAgent = "ao-agent-orchestrator"
	}
	return c
}

func (c *Client) CredentialHash(ctx context.Context) string {
	tok, err := c.tokens.Token(ctx)
	if err != nil {
		return ""
	}
	return credentialHash(tok)
}

type RESTResponse struct {
	StatusCode  int
	NotModified bool
	ETag        string
	Body        []byte
	RateLimit   *domain.SCMRateLimit
	Diagnostic  domain.SCMDiagnostic
}

func (c *Client) DoREST(ctx context.Context, method, path string, q url.Values, body any, etag string, operation string) (RESTResponse, error) {
	started := time.Now()
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return RESTResponse{}, &domain.SCMError{Kind: domain.SCMErrorParse, Operation: operation, Message: err.Error(), Cause: err}
		}
		rdr = bytes.NewReader(b)
	}
	u, err := c.restURL(path, q)
	if err != nil {
		return RESTResponse{}, &domain.SCMError{Kind: domain.SCMErrorParse, Operation: operation, Message: err.Error(), Cause: err}
	}
	req, err := http.NewRequestWithContext(ctx, method, u, rdr)
	if err != nil {
		return RESTResponse{}, &domain.SCMError{Kind: domain.SCMErrorParse, Operation: operation, Message: err.Error(), Cause: err}
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", c.userAgent)
	if etag != "" {
		req.Header.Set("If-None-Match", etag)
	}
	if err := c.authorize(ctx, req); err != nil {
		return RESTResponse{}, err
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return RESTResponse{}, normalizeHTTPError(operation, err)
	}
	defer resp.Body.Close()
	b, readErr := io.ReadAll(resp.Body)
	if readErr != nil {
		return RESTResponse{}, &domain.SCMError{Kind: domain.SCMErrorNetwork, Operation: operation, Message: readErr.Error(), Cause: readErr}
	}
	rl := rateLimitFromHeaders(resp.Header)
	out := RESTResponse{StatusCode: resp.StatusCode, NotModified: resp.StatusCode == http.StatusNotModified, ETag: resp.Header.Get("ETag"), Body: b, RateLimit: rl, Diagnostic: domain.SCMDiagnostic{Operation: operation, StatusCode: resp.StatusCode, ETag: resp.Header.Get("ETag"), CacheHit: resp.StatusCode == http.StatusNotModified, StartedAt: started, DurationMS: time.Since(started).Milliseconds()}}
	if resp.StatusCode == http.StatusNotModified {
		return out, nil
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return out, githubStatusError(operation, resp.StatusCode, b, rl)
	}
	return out, nil
}

func (c *Client) DoGraphQL(ctx context.Context, query string, variables map[string]any, operation string) (map[string]any, *domain.SCMRateLimit, domain.SCMDiagnostic, error) {
	started := time.Now()
	body := map[string]any{"query": query, "variables": variables}
	b, err := json.Marshal(body)
	if err != nil {
		return nil, nil, domain.SCMDiagnostic{}, &domain.SCMError{Kind: domain.SCMErrorParse, Operation: operation, Message: err.Error(), Cause: err}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.graphqlURL, bytes.NewReader(b))
	if err != nil {
		return nil, nil, domain.SCMDiagnostic{}, &domain.SCMError{Kind: domain.SCMErrorParse, Operation: operation, Message: err.Error(), Cause: err}
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", c.userAgent)
	if err := c.authorize(ctx, req); err != nil {
		return nil, nil, domain.SCMDiagnostic{}, err
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, nil, domain.SCMDiagnostic{}, normalizeHTTPError(operation, err)
	}
	defer resp.Body.Close()
	respBody, readErr := io.ReadAll(resp.Body)
	if readErr != nil {
		return nil, nil, domain.SCMDiagnostic{}, &domain.SCMError{Kind: domain.SCMErrorNetwork, Operation: operation, Message: readErr.Error(), Cause: readErr}
	}
	rl := rateLimitFromHeaders(resp.Header)
	diag := domain.SCMDiagnostic{Operation: operation, StatusCode: resp.StatusCode, StartedAt: started, DurationMS: time.Since(started).Milliseconds()}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, rl, diag, githubStatusError(operation, resp.StatusCode, respBody, rl)
	}
	var decoded struct {
		Data   map[string]any `json:"data"`
		Errors []struct {
			Message string `json:"message"`
			Type    string `json:"type"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(respBody, &decoded); err != nil {
		return nil, rl, diag, &domain.SCMError{Kind: domain.SCMErrorParse, Operation: operation, Message: err.Error(), Cause: err}
	}
	if len(decoded.Errors) > 0 {
		kind := domain.SCMErrorUnavailable
		msg := decoded.Errors[0].Message
		if strings.Contains(strings.ToLower(msg), "rate limit") {
			kind = domain.SCMErrorRateLimited
		}
		return decoded.Data, graphqlRateLimit(decoded.Data, rl), diag, &domain.SCMError{Kind: kind, Operation: operation, Message: msg}
	}
	return decoded.Data, graphqlRateLimit(decoded.Data, rl), diag, nil
}

func (c *Client) authorize(ctx context.Context, req *http.Request) error {
	token, err := c.tokens.Token(ctx)
	if err != nil {
		return &domain.SCMError{Kind: domain.SCMErrorAuthFailed, Operation: "auth", Message: err.Error(), Cause: err}
	}
	req.Header.Set("Authorization", "Bearer "+token)
	return nil
}

func (c *Client) restURL(path string, q url.Values) (string, error) {
	base, err := url.Parse(c.restBase)
	if err != nil {
		return "", err
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	base.Path = strings.TrimSuffix(base.Path, "/") + path
	base.RawQuery = q.Encode()
	return base.String(), nil
}

func normalizeHTTPError(operation string, err error) error {
	kind := domain.SCMErrorNetwork
	var ne net.Error
	if err != nil && strings.Contains(strings.ToLower(err.Error()), "unsupported protocol") {
		kind = domain.SCMErrorUnsupported
	} else if err != nil && errors.As(err, &ne) && ne.Timeout() {
		kind = domain.SCMErrorNetwork
	}
	return &domain.SCMError{Kind: kind, Operation: operation, Message: err.Error(), Cause: err}
}

func githubStatusError(operation string, status int, body []byte, rl *domain.SCMRateLimit) error {
	kind := domain.SCMErrorUnavailable
	switch status {
	case http.StatusUnauthorized, http.StatusForbidden:
		if rl != nil && rl.Remaining == 0 {
			kind = domain.SCMErrorRateLimited
		} else {
			kind = domain.SCMErrorAuthFailed
		}
	case http.StatusNotFound:
		kind = domain.SCMErrorNotFound
	case http.StatusTooManyRequests:
		kind = domain.SCMErrorRateLimited
	}
	msg := strings.TrimSpace(string(body))
	var gh struct {
		Message string `json:"message"`
	}
	if json.Unmarshal(body, &gh) == nil && gh.Message != "" {
		msg = gh.Message
	}
	se := &domain.SCMError{Kind: kind, Operation: operation, StatusCode: status, Message: msg}
	if kind == domain.SCMErrorRateLimited && rl != nil {
		se.RetryAfter = rl.ResetAt
	}
	return se
}

func rateLimitFromHeaders(h http.Header) *domain.SCMRateLimit {
	if h == nil || h.Get("X-RateLimit-Limit") == "" {
		return nil
	}
	limit, _ := strconv.Atoi(h.Get("X-RateLimit-Limit"))
	remaining, _ := strconv.Atoi(h.Get("X-RateLimit-Remaining"))
	resetUnix, _ := strconv.ParseInt(h.Get("X-RateLimit-Reset"), 10, 64)
	var reset time.Time
	if resetUnix > 0 {
		reset = time.Unix(resetUnix, 0)
	}
	return &domain.SCMRateLimit{Limit: limit, Remaining: remaining, ResetAt: reset, Resource: h.Get("X-RateLimit-Resource")}
}

func graphqlRateLimit(data map[string]any, fallback *domain.SCMRateLimit) *domain.SCMRateLimit {
	if data == nil {
		return fallback
	}
	rl, ok := data["rateLimit"].(map[string]any)
	if !ok {
		return fallback
	}
	out := &domain.SCMRateLimit{Resource: "graphql"}
	out.Limit = int(num(rl["limit"]))
	out.Remaining = int(num(rl["remaining"]))
	if s, ok := rl["resetAt"].(string); ok {
		out.ResetAt, _ = time.Parse(time.RFC3339, s)
	}
	return out
}

func num(v any) float64 {
	switch t := v.(type) {
	case float64:
		return t
	case int:
		return float64(t)
	case json.Number:
		f, _ := t.Float64()
		return f
	default:
		return 0
	}
}
