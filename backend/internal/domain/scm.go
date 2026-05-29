package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// SCMProvider identifies a source-control provider implementation. The Go
// rewrite intentionally starts with GitHub as the first real provider, but the
// normalized data model keeps the provider/host/repo identity explicit so later
// providers cannot accidentally key by PR number alone.
type SCMProvider string

const (
	SCMProviderGitHub SCMProvider = "github"
)

// SCMFreshness describes whether an observation fetched current provider truth,
// reused cache because the provider returned 304, or could not fetch. The LCM
// projection treats FreshnessUnavailable as Fetched=false so stale/unavailable
// data never overwrites known lifecycle truth.
type SCMFreshness string

const (
	SCMFreshnessFresh       SCMFreshness = "fresh"
	SCMFreshnessUnchanged   SCMFreshness = "unchanged"
	SCMFreshnessUnavailable SCMFreshness = "unavailable"
)

// SCMErrorKind is the normalized provider error vocabulary used in snapshots,
// diagnostics and poll state. Provider adapters should wrap low-level failures
// into one of these stable categories.
type SCMErrorKind string

const (
	SCMErrorAuthFailed  SCMErrorKind = "auth_failed"
	SCMErrorRateLimited SCMErrorKind = "rate_limited"
	SCMErrorNotFound    SCMErrorKind = "not_found"
	SCMErrorNetwork     SCMErrorKind = "network_error"
	SCMErrorParse       SCMErrorKind = "parse_error"
	SCMErrorUnsupported SCMErrorKind = "unsupported"
	SCMErrorUnavailable SCMErrorKind = "unavailable"
	SCMErrorCommand     SCMErrorKind = "command_error"
)

// SCMError is returned by providers and commands when a provider-specific
// failure needs to cross a package boundary in normalized form.
type SCMError struct {
	Kind       SCMErrorKind `json:"kind"`
	Operation  string       `json:"operation,omitempty"`
	StatusCode int          `json:"statusCode,omitempty"`
	RetryAfter time.Time    `json:"retryAfter,omitempty"`
	Message    string       `json:"message"`
	Cause      error        `json:"-"`
}

func (e *SCMError) Error() string {
	if e == nil {
		return "<nil>"
	}
	parts := []string{string(e.Kind)}
	if e.Operation != "" {
		parts = append(parts, e.Operation)
	}
	if e.StatusCode != 0 {
		parts = append(parts, fmt.Sprintf("status=%d", e.StatusCode))
	}
	if e.Message != "" {
		parts = append(parts, e.Message)
	}
	if e.Cause != nil {
		parts = append(parts, e.Cause.Error())
	}
	return strings.Join(parts, ": ")
}

func (e *SCMError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

// SCMRepository is a globally-stable repository identity. Repo is owner/name.
type SCMRepository struct {
	Provider SCMProvider `json:"provider"`
	Host     string      `json:"host"`
	Repo     string      `json:"repo"`
}

func (r SCMRepository) Key() string {
	return strings.Join([]string{string(r.Provider), normalizeHost(r.Host), strings.ToLower(r.Repo)}, "|")
}

func (r SCMRepository) OwnerName() (owner, name string) {
	parts := strings.SplitN(r.Repo, "/", 2)
	if len(parts) == 2 {
		return parts[0], parts[1]
	}
	return "", r.Repo
}

func normalizeHost(h string) string {
	h = strings.TrimSpace(strings.ToLower(h))
	h = strings.TrimPrefix(h, "https://")
	h = strings.TrimPrefix(h, "http://")
	return strings.TrimSuffix(h, "/")
}

// SCMChangeRequestID prevents PR-number-only identity bugs.
type SCMChangeRequestID struct {
	Provider SCMProvider `json:"provider"`
	Host     string      `json:"host"`
	Repo     string      `json:"repo"`
	Number   int         `json:"number"`
}

func (id SCMChangeRequestID) Key() string {
	return strings.Join([]string{string(id.Provider), normalizeHost(id.Host), strings.ToLower(id.Repo), fmt.Sprintf("%d", id.Number)}, "|")
}

// SCMSubject binds an orchestrator session to a source branch and, once known,
// to a concrete change request. CredentialHash is deliberately part of cache
// scoping but not of PR identity.
type SCMSubject struct {
	SessionID      SessionID   `json:"sessionId"`
	ProjectID      ProjectID   `json:"projectId"`
	Provider       SCMProvider `json:"provider"`
	Host           string      `json:"host"`
	Repo           string      `json:"repo"`
	Branch         string      `json:"branch"`
	BaseBranch     string      `json:"baseBranch,omitempty"`
	CredentialHash string      `json:"credentialHash,omitempty"`
	PRNumber       int         `json:"prNumber,omitempty"`
	PRURL          string      `json:"prUrl,omitempty"`
	CreatedAt      time.Time   `json:"createdAt,omitempty"`
	UpdatedAt      time.Time   `json:"updatedAt,omitempty"`
}

func (s SCMSubject) Repository() SCMRepository {
	return SCMRepository{Provider: s.Provider, Host: s.Host, Repo: s.Repo}
}

func (s SCMSubject) ChangeRequestID() SCMChangeRequestID {
	return SCMChangeRequestID{Provider: s.Provider, Host: s.Host, Repo: s.Repo, Number: s.PRNumber}
}

func (s SCMSubject) SubjectKey() string { return string(s.SessionID) }

func (s SCMSubject) CacheScope() SCMProviderCacheScope {
	return SCMProviderCacheScope{Provider: s.Provider, Host: s.Host, Repo: s.Repo, CredentialHash: s.CredentialHash}
}

// SCMSubjectFilter is used by stores/read models to find bindings.
type SCMSubjectFilter struct {
	ProjectID ProjectID
	Provider  SCMProvider
	Host      string
	Repo      string
}

// SCMSnapshot is the latest normalized provider truth for a session. Revision is
// assigned by the SCMStore and increments only when SemanticHash changes.
type SCMSnapshot struct {
	SessionID    SessionID       `json:"sessionId"`
	Subject      SCMSubject      `json:"subject"`
	Revision     int64           `json:"revision"`
	SemanticHash string          `json:"semanticHash"`
	Freshness    SCMFreshness    `json:"freshness"`
	ObservedAt   time.Time       `json:"observedAt"`
	PR           *SCMPullRequest `json:"pr,omitempty"`
	CI           SCMCI           `json:"ci"`
	Review       SCMReview       `json:"review"`
	Mergeability SCMMergeability `json:"mergeability"`
	RateLimit    *SCMRateLimit   `json:"rateLimit,omitempty"`
	Diagnostics  []SCMDiagnostic `json:"diagnostics,omitempty"`
}

func (s SCMSnapshot) ChangeRequestID() SCMChangeRequestID {
	if s.PR != nil && s.PR.Number > 0 {
		return SCMChangeRequestID{Provider: s.Subject.Provider, Host: s.Subject.Host, Repo: s.Subject.Repo, Number: s.PR.Number}
	}
	return s.Subject.ChangeRequestID()
}

// SCMPullRequest holds provider-normalized PR metadata.
type SCMPullRequest struct {
	ID           SCMChangeRequestID `json:"id"`
	Number       int                `json:"number"`
	URL          string             `json:"url"`
	Title        string             `json:"title,omitempty"`
	State        PRState            `json:"state"`
	Draft        bool               `json:"draft"`
	Merged       bool               `json:"merged"`
	SourceBranch string             `json:"sourceBranch"`
	TargetBranch string             `json:"targetBranch"`
	HeadSHA      string             `json:"headSha,omitempty"`
	Additions    int                `json:"additions,omitempty"`
	Deletions    int                `json:"deletions,omitempty"`
}

// SCMCI summarizes checks. Summary values intentionally mirror ports.CISummary
// strings without importing ports into domain.
type SCMCI struct {
	Summary        string     `json:"summary"`
	Checks         []SCMCheck `json:"checks,omitempty"`
	FailureLogTail string     `json:"failureLogTail,omitempty"`
}

type SCMCheck struct {
	Name       string `json:"name"`
	Status     string `json:"status,omitempty"`
	Conclusion string `json:"conclusion,omitempty"`
	URL        string `json:"url,omitempty"`
	Details    string `json:"details,omitempty"`
	LogTail    string `json:"logTail,omitempty"`
}

// SCMReview captures both aggregate review decision and unresolved threads. A
// thread is classified as bot when all unresolved comments in it are bot-authored;
// mixed or human comments keep IsBot=false so humans outrank bots.
type SCMReview struct {
	Decision          string            `json:"decision"`
	UnresolvedThreads []SCMReviewThread `json:"unresolvedThreads,omitempty"`
	BotComments       []SCMReviewThread `json:"botComments,omitempty"`
	HumanComments     []SCMReviewThread `json:"humanComments,omitempty"`
}

type SCMReviewThread struct {
	ID       string             `json:"id"`
	Path     string             `json:"path,omitempty"`
	Line     int                `json:"line,omitempty"`
	URL      string             `json:"url,omitempty"`
	IsBot    bool               `json:"isBot"`
	Comments []SCMReviewComment `json:"comments,omitempty"`
}

type SCMReviewComment struct {
	ID       string `json:"id,omitempty"`
	Author   string `json:"author,omitempty"`
	Body     string `json:"body,omitempty"`
	URL      string `json:"url,omitempty"`
	IsBot    bool   `json:"isBot"`
	Path     string `json:"path,omitempty"`
	Line     int    `json:"line,omitempty"`
	ThreadID string `json:"threadId,omitempty"`
}

type SCMMergeability struct {
	Mergeable   bool     `json:"mergeable"`
	CIPassing   bool     `json:"ciPassing"`
	Approved    bool     `json:"approved"`
	NoConflicts bool     `json:"noConflicts"`
	BehindBase  bool     `json:"behindBase,omitempty"`
	Conflict    bool     `json:"conflict,omitempty"`
	Blockers    []string `json:"blockers,omitempty"`
	RawState    string   `json:"rawState,omitempty"`
	MergeState  string   `json:"mergeState,omitempty"`
}

type SCMRateLimit struct {
	Limit     int       `json:"limit,omitempty"`
	Remaining int       `json:"remaining,omitempty"`
	ResetAt   time.Time `json:"resetAt,omitempty"`
	Resource  string    `json:"resource,omitempty"`
}

type SCMDiagnostic struct {
	Operation  string       `json:"operation"`
	StatusCode int          `json:"statusCode,omitempty"`
	ErrorKind  SCMErrorKind `json:"errorKind,omitempty"`
	Message    string       `json:"message,omitempty"`
	ETag       string       `json:"etag,omitempty"`
	CacheHit   bool         `json:"cacheHit,omitempty"`
	StartedAt  time.Time    `json:"startedAt,omitempty"`
	DurationMS int64        `json:"durationMs,omitempty"`
}

// Provider cache keys are scoped by provider+host+repo+credential hash, with a
// namespace/key inside that scope for ETags and opaque provider hints.
type SCMProviderCacheScope struct {
	Provider       SCMProvider `json:"provider"`
	Host           string      `json:"host"`
	Repo           string      `json:"repo"`
	CredentialHash string      `json:"credentialHash,omitempty"`
}

func (s SCMProviderCacheScope) ScopeKey() string {
	return strings.Join([]string{string(s.Provider), normalizeHost(s.Host), strings.ToLower(s.Repo), s.CredentialHash}, "|")
}

type SCMProviderCacheKey struct {
	SCMProviderCacheScope
	Namespace string `json:"namespace"`
	Key       string `json:"key"`
}

func (k SCMProviderCacheKey) String() string {
	return k.ScopeKey() + "|" + k.Namespace + "|" + k.Key
}

type SCMProviderCachePrefix struct {
	SCMProviderCacheScope
	Namespace string `json:"namespace,omitempty"`
	KeyPrefix string `json:"keyPrefix,omitempty"`
}

func (p SCMProviderCachePrefix) Matches(k SCMProviderCacheKey) bool {
	if p.Provider != "" && p.Provider != k.Provider {
		return false
	}
	if p.Host != "" && normalizeHost(p.Host) != normalizeHost(k.Host) {
		return false
	}
	if p.Repo != "" && strings.ToLower(p.Repo) != strings.ToLower(k.Repo) {
		return false
	}
	if p.CredentialHash != "" && p.CredentialHash != k.CredentialHash {
		return false
	}
	if p.Namespace != "" && p.Namespace != k.Namespace {
		return false
	}
	if p.KeyPrefix != "" && !strings.HasPrefix(k.Key, p.KeyPrefix) {
		return false
	}
	return true
}

type SCMProviderCacheEntry struct {
	Key       SCMProviderCacheKey `json:"key"`
	ETag      string              `json:"etag,omitempty"`
	Value     json.RawMessage     `json:"value,omitempty"`
	UpdatedAt time.Time           `json:"updatedAt"`
	ExpiresAt time.Time           `json:"expiresAt,omitempty"`
	// MaxEntries is an optional provider-owned retention hint. It is intentionally
	// not persisted so durable cache records remain provider data, while the store
	// only enforces the generic cap requested by the writer.
	MaxEntries int `json:"-"`
}

func (e SCMProviderCacheEntry) Expired(now time.Time) bool {
	return !e.ExpiresAt.IsZero() && !now.Before(e.ExpiresAt)
}

type SCMPollStateKey struct {
	Provider SCMProvider `json:"provider"`
	Host     string      `json:"host"`
	Repo     string      `json:"repo"`
}

func (k SCMPollStateKey) String() string {
	return strings.Join([]string{string(k.Provider), normalizeHost(k.Host), strings.ToLower(k.Repo)}, "|")
}

type SCMPollState struct {
	Key             SCMPollStateKey `json:"key"`
	ConsecutiveFail int             `json:"consecutiveFailures"`
	LastSuccessAt   time.Time       `json:"lastSuccessAt,omitempty"`
	LastFailureAt   time.Time       `json:"lastFailureAt,omitempty"`
	BackoffUntil    time.Time       `json:"backoffUntil,omitempty"`
	RateLimitUntil  time.Time       `json:"rateLimitUntil,omitempty"`
	LastError       *SCMError       `json:"lastError,omitempty"`
}

// ComputeSCMSemanticHash hashes provider truth while deliberately ignoring
// observation mechanics (ObservedAt, Revision, diagnostics, rate-limit data).
// Stores use this to decide whether a new snapshot revision is warranted.
func ComputeSCMSemanticHash(s SCMSnapshot) (string, error) {
	s.Revision = 0
	s.SemanticHash = ""
	s.ObservedAt = time.Time{}
	s.Subject.CreatedAt = time.Time{}
	s.Subject.UpdatedAt = time.Time{}
	if s.Freshness == SCMFreshnessUnchanged {
		s.Freshness = SCMFreshnessFresh
	}
	s.Diagnostics = nil
	s.RateLimit = nil
	b, err := json.Marshal(s)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), nil
}

func MustSCMSemanticHash(s SCMSnapshot) string {
	h, err := ComputeSCMSemanticHash(s)
	if err != nil {
		panic(err)
	}
	return h
}

func NormalizeSCMCI(summary string) string {
	switch strings.ToLower(summary) {
	case "success", "successful", "passed", "passing", "pass":
		return "passing"
	case "failure", "failed", "failing", "error", "timed_out", "cancelled", "action_required":
		return "failing"
	case "pending", "queued", "in_progress", "requested", "waiting", "expected":
		return "pending"
	case "", "none", "no_checks":
		return "none"
	default:
		return strings.ToLower(summary)
	}
}

func NormalizeSCMReviewDecision(decision string) string {
	switch strings.ToUpper(strings.TrimSpace(decision)) {
	case "APPROVED", "APPROVE":
		return "approved"
	case "CHANGES_REQUESTED", "REQUEST_CHANGES":
		return "changes_requested"
	case "REVIEW_REQUIRED", "PENDING", "COMMENTED":
		return "pending"
	case "", "NONE":
		return "none"
	default:
		return strings.ToLower(decision)
	}
}
