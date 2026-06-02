package ports

import "time"

// SCMRepo identifies a repository without assuming a provider-specific URL
// shape. Repo is conventionally "owner/name" for providers that expose an
// owner namespace, while Owner/Name are kept split for provider calls.
type SCMRepo struct {
	Provider string
	Host     string
	Owner    string
	Name     string
	Repo     string
}

// SCMPRRef identifies a pull request within a provider-neutral repository.
type SCMPRRef struct {
	Repo   SCMRepo
	Number int
	URL    string
}

// SCMGuardResult is an ETag-style cache guard result. NotModified maps to HTTP
// 304 for providers that support it.
type SCMGuardResult struct {
	ETag        string
	NotModified bool
}

// SCMObservation is the provider-neutral pull-request observation emitted by
// the SCM observer and consumed by lifecycle. Provider adapters normalize their
// SCM-specific payloads into this DTO before the observer persists/notifies.
type SCMObservation struct {
	Fetched    bool
	ObservedAt time.Time

	Provider string
	Host     string
	Repo     string

	PR           SCMPRObservation
	CI           SCMCIObservation
	Review       SCMReviewObservation
	Mergeability SCMMergeabilityObservation

	Changed SCMChanged
}

// SCMChanged marks which semantic state buckets changed in the successful poll.
type SCMChanged struct {
	Metadata bool
	CI       bool
	Review   bool
}

// SCMPRObservation carries provider-neutral PR metadata.
type SCMPRObservation struct {
	URL            string
	Number         int
	State          string
	Draft          bool
	Merged         bool
	Closed         bool
	SourceBranch   string
	TargetBranch   string
	HeadSHA        string
	Title          string
	Additions      int
	Deletions      int
	ChangedFiles   int
	Author         string
	BaseSHA        string
	MergeCommitSHA string

	ProviderState            string
	ProviderMergeable        string
	ProviderMergeStateStatus string
	HTMLURL                  string

	CreatedAtProvider time.Time
	UpdatedAtProvider time.Time
	MergedAtProvider  time.Time
	ClosedAtProvider  time.Time
}

// SCMCIObservation carries aggregate CI state plus failing-check details.
type SCMCIObservation struct {
	Summary           string
	HeadSHA           string
	FailedFingerprint string
	Checks            []SCMCheckObservation
	FailedChecks      []SCMCheckObservation
	FailureLogTail    string
}

// SCMCheckObservation is one normalized check/status context. ProviderID is an
// optional provider-owned identifier (for GitHub, Actions job/check-run id) used
// by the provider to fetch logs; consumers should not attach meaning to it.
type SCMCheckObservation struct {
	Name       string
	Status     string
	Conclusion string
	URL        string
	LogTail    string
	ProviderID string
}

// SCMReviewObservation carries normalized review-decision and review-thread facts.
type SCMReviewObservation struct {
	Decision string
	Threads  []SCMReviewThreadObservation
}

// SCMReviewThreadObservation is a normalized review thread with comments.
type SCMReviewThreadObservation struct {
	ID       string
	Path     string
	Line     int
	Resolved bool
	IsBot    bool
	Comments []SCMReviewCommentObservation
}

// SCMReviewCommentObservation is one normalized review comment.
type SCMReviewCommentObservation struct {
	ID     string
	Author string
	Body   string
	URL    string
	IsBot  bool
}

// SCMMergeabilityObservation is the normalized mergeability verdict.
type SCMMergeabilityObservation struct {
	State      string
	Mergeable  bool
	Conflict   bool
	BehindBase bool
	Blockers   []string
}
