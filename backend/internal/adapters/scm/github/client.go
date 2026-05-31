package github

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	scmlog "github.com/aoagents/agent-orchestrator/backend/internal/scm/logging"
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
	logger     *slog.Logger
}

type ClientOptions struct {
	HTTPClient *http.Client
	Token      TokenSource
	RESTBase   string
	GraphQLURL string
	UserAgent  string
	Logger     *slog.Logger
}

func NewClient(opts ClientOptions) *Client {
	c := &Client{httpClient: opts.HTTPClient, tokens: opts.Token, restBase: opts.RESTBase, graphqlURL: opts.GraphQLURL, userAgent: opts.UserAgent, logger: opts.Logger}
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
	ctx, _ = scmlog.EnsureCorrelationID(ctx)
	started := time.Now()
	logger := scmlog.Logger(c.logger)
	endpoint := githubEndpointTemplate(path)
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			se := &domain.SCMError{Kind: domain.SCMErrorParse, Operation: operation, Message: err.Error(), Cause: err}
			logTransportFailure(ctx, logger, operation, method, endpoint, started, 0, nil, false, false, se)
			return RESTResponse{}, se
		}
		rdr = bytes.NewReader(b)
	}
	u, err := c.restURL(path, q)
	if err != nil {
		se := &domain.SCMError{Kind: domain.SCMErrorParse, Operation: operation, Message: err.Error(), Cause: err}
		logTransportFailure(ctx, logger, operation, method, endpoint, started, 0, nil, false, etag != "", se)
		return RESTResponse{}, se
	}
	req, err := http.NewRequestWithContext(ctx, method, u, rdr)
	if err != nil {
		se := &domain.SCMError{Kind: domain.SCMErrorParse, Operation: operation, Message: err.Error(), Cause: err}
		logTransportFailure(ctx, logger, operation, method, endpoint, started, 0, nil, false, etag != "", se)
		return RESTResponse{}, se
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
	logTransportRequest(ctx, logger, operation, method, endpoint, etag != "")
	if err := c.authorize(ctx, req); err != nil {
		logTransportFailure(ctx, logger, operation, method, endpoint, started, 0, nil, false, etag != "", err)
		return RESTResponse{}, err
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		se := normalizeHTTPError(operation, err)
		logTransportFailure(ctx, logger, operation, method, endpoint, started, 0, nil, false, etag != "", se)
		return RESTResponse{}, se
	}
	defer resp.Body.Close()
	b, readErr := io.ReadAll(resp.Body)
	if readErr != nil {
		se := &domain.SCMError{Kind: domain.SCMErrorNetwork, Operation: operation, Message: readErr.Error(), Cause: readErr}
		logTransportFailure(ctx, logger, operation, method, endpoint, started, resp.StatusCode, rateLimitFromHeaders(resp.Header), false, etag != "", se)
		return RESTResponse{}, se
	}
	rl := rateLimitFromHeaders(resp.Header)
	out := RESTResponse{StatusCode: resp.StatusCode, NotModified: resp.StatusCode == http.StatusNotModified, ETag: resp.Header.Get("ETag"), Body: b, RateLimit: rl, Diagnostic: domain.SCMDiagnostic{Operation: operation, StatusCode: resp.StatusCode, ETag: resp.Header.Get("ETag"), CacheHit: resp.StatusCode == http.StatusNotModified, StartedAt: started, DurationMS: time.Since(started).Milliseconds()}}
	if resp.StatusCode == http.StatusNotModified {
		logTransportResponse(ctx, logger, operation, method, endpoint, started, resp.StatusCode, rl, out.NotModified, out.ETag != "")
		return out, nil
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		se := githubStatusError(operation, resp.StatusCode, b, rl)
		if se.Kind == domain.SCMErrorAuthFailed {
			c.invalidateToken()
		}
		out.Diagnostic.ErrorKind = se.Kind
		out.Diagnostic.Message = scmlog.SafeDiagnosticMessage(se)
		logTransportFailure(ctx, logger, operation, method, endpoint, started, resp.StatusCode, rl, false, out.ETag != "" || etag != "", se)
		return out, se
	}
	logTransportResponse(ctx, logger, operation, method, endpoint, started, resp.StatusCode, rl, out.NotModified, out.ETag != "")
	return out, nil
}

func (c *Client) DoGraphQL(ctx context.Context, query string, variables map[string]any, operation string) (map[string]any, *domain.SCMRateLimit, domain.SCMDiagnostic, error) {
	ctx, _ = scmlog.EnsureCorrelationID(ctx)
	started := time.Now()
	logger := scmlog.Logger(c.logger)
	const endpoint = "/graphql"
	body := map[string]any{"query": query, "variables": variables}
	b, err := json.Marshal(body)
	if err != nil {
		se := &domain.SCMError{Kind: domain.SCMErrorParse, Operation: operation, Message: err.Error(), Cause: err}
		logTransportFailure(ctx, logger, operation, http.MethodPost, endpoint, started, 0, nil, false, false, se)
		return nil, nil, domain.SCMDiagnostic{}, se
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.graphqlURL, bytes.NewReader(b))
	if err != nil {
		se := &domain.SCMError{Kind: domain.SCMErrorParse, Operation: operation, Message: err.Error(), Cause: err}
		logTransportFailure(ctx, logger, operation, http.MethodPost, endpoint, started, 0, nil, false, false, se)
		return nil, nil, domain.SCMDiagnostic{}, se
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", c.userAgent)
	logTransportRequest(ctx, logger, operation, http.MethodPost, endpoint, false)
	if err := c.authorize(ctx, req); err != nil {
		logTransportFailure(ctx, logger, operation, http.MethodPost, endpoint, started, 0, nil, false, false, err)
		return nil, nil, domain.SCMDiagnostic{}, err
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		se := normalizeHTTPError(operation, err)
		logTransportFailure(ctx, logger, operation, http.MethodPost, endpoint, started, 0, nil, false, false, se)
		return nil, nil, domain.SCMDiagnostic{}, se
	}
	defer resp.Body.Close()
	respBody, readErr := io.ReadAll(resp.Body)
	if readErr != nil {
		se := &domain.SCMError{Kind: domain.SCMErrorNetwork, Operation: operation, Message: readErr.Error(), Cause: readErr}
		logTransportFailure(ctx, logger, operation, http.MethodPost, endpoint, started, resp.StatusCode, rateLimitFromHeaders(resp.Header), false, false, se)
		return nil, nil, domain.SCMDiagnostic{}, se
	}
	rl := rateLimitFromHeaders(resp.Header)
	diag := domain.SCMDiagnostic{Operation: operation, StatusCode: resp.StatusCode, StartedAt: started, DurationMS: time.Since(started).Milliseconds()}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		se := githubStatusError(operation, resp.StatusCode, respBody, rl)
		if se.Kind == domain.SCMErrorAuthFailed {
			c.invalidateToken()
		}
		diag.ErrorKind = se.Kind
		diag.Message = scmlog.SafeDiagnosticMessage(se)
		logTransportFailure(ctx, logger, operation, http.MethodPost, endpoint, started, resp.StatusCode, rl, false, false, se)
		return nil, rl, diag, se
	}
	var decoded struct {
		Data   map[string]any `json:"data"`
		Errors []struct {
			Message string `json:"message"`
			Type    string `json:"type"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(respBody, &decoded); err != nil {
		se := &domain.SCMError{Kind: domain.SCMErrorParse, Operation: operation, Message: err.Error(), Cause: err}
		diag.ErrorKind = se.Kind
		diag.Message = scmlog.SafeDiagnosticMessage(se)
		logTransportFailure(ctx, logger, operation, http.MethodPost, endpoint, started, resp.StatusCode, rl, false, false, se)
		return nil, rl, diag, se
	}
	if len(decoded.Errors) > 0 {
		kind := domain.SCMErrorUnavailable
		msg := decoded.Errors[0].Message
		if strings.Contains(strings.ToLower(msg), "rate limit") {
			kind = domain.SCMErrorRateLimited
		} else if strings.Contains(strings.ToLower(msg), "bad credentials") || strings.Contains(strings.ToLower(msg), "credentials") {
			kind = domain.SCMErrorAuthFailed
		}
		rl = graphqlRateLimit(decoded.Data, rl)
		se := &domain.SCMError{Kind: kind, Operation: operation, Message: scmlog.SafeDiagnosticMessage(&domain.SCMError{Kind: kind, Message: msg})}
		if se.Kind == domain.SCMErrorAuthFailed {
			c.invalidateToken()
		}
		diag.ErrorKind = se.Kind
		diag.Message = scmlog.SafeDiagnosticMessage(se)
		logTransportFailure(ctx, logger, operation, http.MethodPost, endpoint, started, resp.StatusCode, rl, false, false, se)
		return decoded.Data, rl, diag, se
	}
	rl = graphqlRateLimit(decoded.Data, rl)
	logTransportResponse(ctx, logger, operation, http.MethodPost, endpoint, started, resp.StatusCode, rl, false, false)
	return decoded.Data, rl, diag, nil
}

func (c *Client) authorize(ctx context.Context, req *http.Request) error {
	token, err := c.tokens.Token(ctx)
	if err != nil {
		return &domain.SCMError{Kind: domain.SCMErrorAuthFailed, Operation: "auth", Message: err.Error(), Cause: err}
	}
	req.Header.Set("Authorization", "Bearer "+token)
	return nil
}

func (c *Client) invalidateToken() {
	if src, ok := c.tokens.(tokenInvalidator); ok {
		src.InvalidateToken()
	}
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

func githubStatusError(operation string, status int, body []byte, rl *domain.SCMRateLimit) *domain.SCMError {
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
	var gh struct {
		Message string `json:"message"`
	}
	_ = json.Unmarshal(body, &gh)
	msg := scmlog.StatusMessage(status, body, gh.Message)
	se := &domain.SCMError{Kind: kind, Operation: operation, StatusCode: status, Message: msg}
	if kind == domain.SCMErrorRateLimited && rl != nil {
		se.RetryAfter = rl.ResetAt
	}
	return se
}

func logTransportRequest(ctx context.Context, logger *slog.Logger, operation, method, endpoint string, etagPresent bool) {
	attrs := transportAttrs(ctx, operation, method, endpoint,
		slog.Bool(scmlog.FieldETagPresent, etagPresent),
	)
	logger.Debug(scmlog.EventTransportRequest, scmlog.Args(attrs)...)
}

func logTransportResponse(ctx context.Context, logger *slog.Logger, operation, method, endpoint string, started time.Time, status int, rl *domain.SCMRateLimit, cacheHit, etagPresent bool) {
	attrs := transportAttrs(ctx, operation, method, endpoint,
		slog.Int(scmlog.FieldStatusCode, status),
		slog.Int64(scmlog.FieldDurationMS, scmlog.DurationMS(started)),
		slog.Bool(scmlog.FieldCacheHit, cacheHit),
		slog.Bool(scmlog.FieldETagPresent, etagPresent),
	)
	attrs = append(attrs, scmlog.RateLimitAttrs(rl)...)
	logger.Debug(scmlog.EventTransportResponse, scmlog.Args(attrs)...)
}

func logTransportFailure(ctx context.Context, logger *slog.Logger, operation, method, endpoint string, started time.Time, status int, rl *domain.SCMRateLimit, cacheHit, etagPresent bool, err error) {
	attrs := transportAttrs(ctx, operation, method, endpoint,
		slog.Int64(scmlog.FieldDurationMS, scmlog.DurationMS(started)),
		slog.Bool(scmlog.FieldCacheHit, cacheHit),
		slog.Bool(scmlog.FieldETagPresent, etagPresent),
	)
	if status != 0 {
		attrs = append(attrs, slog.Int(scmlog.FieldStatusCode, status))
	}
	attrs = append(attrs, scmlog.RateLimitAttrs(rl)...)
	attrs = append(attrs, scmlog.ErrorAttrs(err)...)
	if scmlog.ErrorKind(err) == domain.SCMErrorRateLimited {
		logger.Warn(scmlog.EventTransportRateLimited, scmlog.Args(attrs)...)
		return
	}
	logger.Warn(scmlog.EventTransportFailed, scmlog.Args(attrs)...)
}

func transportAttrs(ctx context.Context, operation, method, endpoint string, attrs ...slog.Attr) []slog.Attr {
	base := []slog.Attr{
		scmlog.CorrelationAttr(ctx),
		slog.String(scmlog.FieldProvider, string(domain.SCMProviderGitHub)),
		slog.String(scmlog.FieldOperation, operation),
		slog.String(scmlog.FieldMethod, method),
		slog.String(scmlog.FieldEndpointTemplate, endpoint),
	}
	return append(base, attrs...)
}

func githubEndpointTemplate(p string) string {
	if p == "" {
		return ""
	}
	parts := strings.Split(strings.Trim(p, "/"), "/")
	if len(parts) >= 3 && parts[0] == "repos" {
		out := []string{"repos", "{owner}", "{repo}"}
		for i := 3; i < len(parts); i++ {
			part := parts[i]
			prev := ""
			if i > 0 {
				prev = parts[i-1]
			}
			switch {
			case prev == "pulls" || prev == "issues":
				out = append(out, "{number}")
			case prev == "commits":
				out = append(out, "{ref}")
			default:
				out = append(out, part)
			}
		}
		return "/" + strings.Join(out, "/")
	}
	return "/" + strings.Join(parts, "/")
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
