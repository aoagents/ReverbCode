// Package gitlab implements the ports.Tracker outbound port for GitLab
// Issues over the REST v4 API. v1 is read-only:
//
//   - Get returns a normalized snapshot of one issue (spawn-bootstrap reads
//     it to hydrate the agent prompt).
//   - List returns a filtered slice of issues in a project (one page, no
//     auto-pagination in v1).
//   - Preflight performs a single GET /user against GitLab to verify the
//     token is accepted; success is cached for the lifetime of the Tracker,
//     failures are not.
//
// Writing back to the tracker (Comment, Transition) is deferred to issue
// #40. The observer / polling loop is deferred to issue #35.
//
// # Authentication
//
// The adapter uses the PRIVATE-TOKEN header (not Authorization: Bearer)
// because that is GitLab's recommended path for personal-access and
// project-access tokens. The TokenSource interface lets callers inject a
// static token in tests or read GITLAB_TOKEN (plus arbitrary higher-priority
// env vars) in production.
//
// # ID and repo shape
//
// TrackerID.Native is "group/project#iid". Subgroup paths
// ("group/sub/project#iid", arbitrary depth) are accepted; the FULL project
// path is URL-encoded with url.PathEscape when forming the endpoint URL —
// without that, GitLab routes /projects/group/sub/project/... as a missing
// project rather than the nested one. TrackerRepo.Native is the same shape
// minus the "#iid".
//
// The IID is the project-internal sequential id (shown in the web UI as
// "#42"), not the global database id. That matches GitLab's recommendation
// to use IIDs in URLs.
//
// # Reverse state mapping
//
// GitLab Issues have two coarse states ("opened", "closed") and — unlike
// GitHub — do not expose a structured close_reason / state_reason on the
// REST v4 issue payload. The adapter projects them onto the normalized
// vocabulary as follows:
//
//   - closed + label "cancelled" OR "wontfix"   -> cancelled
//   - closed (no cancelled/wontfix label)       -> done
//   - opened + label "in-review"                 -> review        (wins when
//     both status labels are present; the workflow is progress -> review)
//   - opened + label "in-progress"               -> in_progress
//   - otherwise                                  -> open
//
// The "in-progress" / "in-review" convention is borrowed verbatim from the
// GitHub adapter so a downstream consumer sees the same shape regardless of
// which tracker an issue lives in. The "cancelled" / "wontfix" labels are
// recognized because GitLab has no native equivalent of GitHub's
// state_reason=not_planned — humans (and other tooling) use one of these
// labels to mark issues abandoned rather than resolved. The adapter does
// NOT write any of these labels in v1 (issue #40).
//
// # Rate limiting
//
// GitLab uses RateLimit-Remaining / RateLimit-Reset (no X- prefix, unlike
// GitHub) and 429 (not 403) for rate-limit responses. On 429 the adapter
// returns a typed *RateLimitError matching errors.Is(err, ErrRateLimited);
// callers that want to back off intelligently can extract ResetAt /
// RetryAfter via errors.As.
//
// # Out of scope
//
//   - No Comment, no Transition (issue #40).
//   - No List pagination beyond a single page; callers needing more results
//     need to wait for the observer/polling work in issue #35.
//   - No webhook receiver, no polling goroutine, no fact projection into LCM.
//   - No richer per-provider metadata on Issue (milestones, epics,
//     iterations); the port only carries fields all v1 providers can fill.
package gitlab
