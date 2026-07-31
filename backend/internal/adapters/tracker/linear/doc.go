// Package linear implements the ports.Tracker outbound port for Linear.
// v1 is read-only:
//
//   - Get returns a normalized snapshot of one issue. TrackerID.Native is
//     the opaque identifier Linear accepts at issue(id:) — either the
//     team-prefixed short id ("ABC-123") or the issue's UUID. The adapter
//     does NOT parse Native; it is passed straight through.
//   - List returns one page of issues. TrackerRepo.Native is the team key
//     (e.g. "ABC") or empty for a workspace-wide enumeration. When a team
//     key is set, the adapter lazily resolves it to the team UUID via
//     teams(filter:{key:{eq:$key}}, first:1) and caches the result so
//     subsequent calls for the same team skip the lookup.
//   - Preflight performs a single { viewer { id } } query against Linear
//     to verify the token is accepted; success is cached for the lifetime
//     of the Tracker, failures are not.
//
// Writing back to the tracker (Comment, Transition) is deferred to issue
// #40. The observer / polling loop is deferred to #35.
//
// # Getting started
//
// Linear has no equivalent of the github gh CLI's keyring, so v1 only
// supports a personal API key sourced from an env var. Two steps:
//
//  1. Mint a personal API key at https://linear.app/settings/api.
//  2. Export it as LINEAR_API_KEY (the default EnvTokenSource fallback).
//     Projects that need per-project tokens can configure additional
//     env-var names via EnvTokenSource.EnvVars; the listed names are
//     consulted in order and LINEAR_API_KEY is the final fallback.
//
// When no token can be sourced, the adapter returns ErrNoToken. The
// error message names both the settings URL and the env var so a fresh
// dev hitting it sees the fix without reading these docs first.
//
// # Authentication
//
// Linear personal API keys are sent as a RAW Authorization header value
// with NO "Bearer " prefix:
//
//	Authorization: lin_api_xxxxxxxxxxxx
//
// This is the single most common source of 401s on this adapter — OAuth
// tokens DO use Bearer, but personal keys do NOT. v1 only supports
// personal keys, so the adapter never prefixes Bearer.
//
// Token rotation: the TokenSource is consulted on EVERY request, so a
// rotated token is picked up without restarting. Preflight, however,
// caches the FIRST successful validation for the lifetime of the
// Tracker — if a previously-valid token is later revoked, Preflight will
// continue to return nil, and the bad-token signal will surface lazily
// on the next Get/List (as ErrAuthFailed via 401 or extensions.type).
// Daemons that need to react to revocation must rely on per-request
// failures, not periodic Preflight.
//
// # Transport
//
// The adapter hand-rolls GraphQL over net/http. We intentionally do NOT
// depend on the Linear SDK or any Go GraphQL client library:
//
//   - The SDK ships a 700KB+ generated documents file and a huge surface
//     we'd touch ~3 endpoints of. v1 stays small and auditable.
//   - Tests can drive the wire exactly via an httptest server that
//     inspects {query, variables}; no SDK shimming required.
//   - Errors are routed through one classifier (extensions.type →
//     sentinel), keeping the adapter's contract with the SM identical to
//     the github adapter.
//
// # Reverse state mapping
//
// Linear's workflow state.type vocabulary is fixed:
//
//	triage, backlog, unstarted, started, completed, canceled
//
// Get projects them onto the normalized state as follows:
//
//	completed                          -> done
//	canceled                           -> cancelled
//	started                            -> in_progress
//	unstarted | triage | backlog | "" -> open
//	(any other value)                  -> open
//
// Note: NormalizedIssueState.review is intentionally NOT produced by this
// adapter in v1. Linear has no native "review" type — teams that use a
// status named "In Review" still set type=started, which we collapse to
// in_progress. A v2 could distinguish via state.name string match, but
// every Linear workspace customizes its workflow so name-based mapping is
// brittle. We surface in_progress and rely on label filtering at the
// caller side when finer state is needed.
//
// # Errors
//
// Linear surfaces errors in two shapes that the adapter normalizes to the
// same sentinels:
//
//   - HTTP 200 with a JSON errors[] array. Each error carries
//     extensions.type — Linear's lowercase-words discriminator (e.g.
//     "authentication error", "ratelimited", "feature not accessible").
//     This is the common case; even rate-limited mutations frequently
//     come back as 200 + ratelimited rather than 429.
//   - HTTP 401 / 429 / 5xx with errors[] but no successful data. The
//     classifier checks errors[].extensions.type first and falls back to
//     status code so either surface routes to the same sentinel.
//
// The wire field is extensions.type (lowercase strings with spaces) —
// NOT extensions.code with SCREAMING_SNAKE_CASE. This is consistent with
// the official @linear/sdk error.ts at HEAD.
//
// # Out of scope
//
//   - No Comment, no Transition (issue #40).
//   - No List auto-pagination — callers get one page bounded by
//     ListFilter.Limit (default 50, silently capped at Linear's first:
//     hard limit of 250). Observer/polling work lands in issue #35.
//   - No webhook receiver, no polling goroutine.
//   - No complexity-aware client-side throttling. RateLimitError carries
//     RetryAfter / ResetAt so the SM can back off, but we don't model
//     the X-RateLimit-Complexity-* headers as a separate budget in v1.
package linear
