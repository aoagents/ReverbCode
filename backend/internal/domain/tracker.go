package domain

// TrackerProvider identifies an issue-tracker provider implementation.
// Provider differences (label-driven vs state-machine vs close-reason) are
// absorbed inside each adapter; the rest of the system only sees
// NormalizedIssueState.
type TrackerProvider string

const (
	TrackerProviderGitHub TrackerProvider = "github"
	TrackerProviderGitLab TrackerProvider = "gitlab"
	TrackerProviderLinear TrackerProvider = "linear"
)

// TrackerID identifies a single issue across providers. Native is the
// provider's own canonical form ("owner/repo#123" for GitHub,
// "group/project#456" for GitLab, "ABC-789" for Linear) and is parsed by the
// adapter. Provider is the discriminator the Session Manager uses to pick an
// adapter.
type TrackerID struct {
	Provider TrackerProvider `json:"provider"`
	Native   string          `json:"native"`
}

// NormalizedIssueState is the cross-provider issue-state vocabulary every
// adapter must implement. The closed list is intentional — adding a value
// here is a port-level decision because every adapter must map it.
type NormalizedIssueState string

const (
	IssueOpen       NormalizedIssueState = "open"
	IssueInProgress NormalizedIssueState = "in_progress"
	IssueInReview   NormalizedIssueState = "review"
	IssueDone       NormalizedIssueState = "done"
	IssueCancelled  NormalizedIssueState = "cancelled"
)

// Issue is the minimum projection every tracker can produce. Fields are
// added only when all v1 providers (GitHub, GitLab, Linear) can populate
// them faithfully; richer metadata stays inside provider-specific code paths.
type Issue struct {
	ID        TrackerID            `json:"id"`
	Title     string               `json:"title"`
	Body      string               `json:"body"`
	State     NormalizedIssueState `json:"state"`
	URL       string               `json:"url"`
	Labels    []string             `json:"labels,omitempty"`
	Assignees []string             `json:"assignees,omitempty"`
}

// TrackerRepo identifies a repository (or its provider-equivalent) for
// cross-issue queries like Tracker.List. Native is the provider's canonical
// owner/project form: "owner/repo" for GitHub, "group/project" for GitLab.
// Linear has no native repo concept; adapters may use a team or workspace
// identifier in Native when this port reaches Linear.
type TrackerRepo struct {
	Provider TrackerProvider `json:"provider"`
	Native   string          `json:"native"`
}

// ListStateFilter narrows Tracker.List results by the provider's coarse
// state (open vs closed). It is intentionally NOT the 5-value normalized
// enum — finer filtering (e.g. "only in-review issues") goes through the
// Labels field of ListFilter.
type ListStateFilter string

const (
	// ListAll is the zero value and returns issues in any state.
	ListAll    ListStateFilter = ""
	ListOpen   ListStateFilter = "open"
	ListClosed ListStateFilter = "closed"
)

// ListFilter is the query the Session Manager passes to Tracker.List.
// Empty / zero values mean "no filter on this dimension".
//
// Limit is the requested page size. The adapter applies its own default
// when zero and SILENTLY CAPS at the provider's per-page maximum — a
// caller asking for more than the cap gets exactly cap items back with no
// error and no indication of truncation. v1 has no auto-pagination;
// callers needing more results need to wait for the observer/polling work
// in issue #35.
type ListFilter struct {
	State    ListStateFilter `json:"state,omitempty"`
	Labels   []string        `json:"labels,omitempty"`
	Assignee string          `json:"assignee,omitempty"`
	Limit    int             `json:"limit,omitempty"`
}
