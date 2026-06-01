// Package scm implements the OBSERVE-layer polling loop that drives
// SCM (pull-request) observations into the PR Manager and Lifecycle
// Manager. The loop is intentionally dumb: every tick it lists alive
// sessions, finds the open PR for each session's branch, asks the
// Provider for an observation, and hands the result to the PR
// Manager (which transactionally writes the row and forwards to
// lifecycle for nudges).
//
// The poller does not own any reaction logic. CI-failure log-tail
// nudges, review-feedback nudges (capped at reviewMaxNudge), and
// merge-conflict rebase nudges all live in lifecycle.ApplyPRObservation.
// Polling is uniform 30s for v1; per-PR adaptive cadence is a follow-up.
package scm

import (
	"context"
	"errors"
	"log/slog"
	"net/url"
	"os/exec"
	"strings"
	"sync/atomic"
	"time"

	scmgithub "github.com/aoagents/agent-orchestrator/backend/internal/adapters/scm/github"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/project"
)

// DefaultInterval is the cadence used when Deps.Interval is zero.
const DefaultInterval = 30 * time.Second

// DefaultObserveTimeout caps one Provider.Observe call so a single hung
// request can't stall the whole tick.
const DefaultObserveTimeout = 15 * time.Second

// Provider observes one PR by its canonical URL. The github adapter
// satisfies this; other SCM adapters (gitlab, etc.) can implement the
// same surface without touching the poller.
type Provider interface {
	Observe(ctx context.Context, prURL string) (ports.PRObservation, error)
}

// BranchPRFinder resolves a session's branch to its open PR URL. v1
// uses this because sessions do not (yet) carry a PR URL field; when
// they do, the poller will prefer the stored URL and only fall back
// here. An empty return with nil error means "no matching open PR".
type BranchPRFinder interface {
	FindOpenPRForBranch(ctx context.Context, owner, repo, branch string) (string, error)
}

// sessionLister narrows the sqlite store to what the poller needs.
type sessionLister interface {
	ListAllSessions(ctx context.Context) ([]domain.SessionRecord, error)
}

// projectGetter narrows project.Manager to its read path.
type projectGetter interface {
	Get(ctx context.Context, id domain.ProjectID) (project.GetResult, error)
}

// prApplier is the seam over pr.Manager.ApplyObservation — which itself
// persists the PR row and forwards to lifecycle for nudges. Keeping
// this one method on the seam means the poller never needs to know
// about lifecycle directly.
type prApplier interface {
	ApplyObservation(ctx context.Context, id domain.SessionID, o ports.PRObservation) error
}

// remoteResolver shells out to git to read a repo's origin URL.
// Injected so tests don't require a real git checkout.
type remoteResolver func(ctx context.Context, projectPath string) (string, error)

// Deps groups every collaborator the Poller needs. Zero-valued
// optional fields fall back to safe defaults (slog.Default, 30s tick,
// 15s observe deadline, real `git` for origin lookup).
type Deps struct {
	Provider       Provider
	Branches       BranchPRFinder
	Sessions       sessionLister
	Projects       projectGetter
	PR             prApplier
	Logger         *slog.Logger
	Interval       time.Duration
	ObserveTimeout time.Duration
	RemoteResolver func(ctx context.Context, projectPath string) (string, error)
}

// Poller is the SCM observation loop. Construct it with New, start the
// background goroutine with Start. Tick is exported so daemon and tests
// can drive a single cycle synchronously.
type Poller struct {
	provider       Provider
	branches       BranchPRFinder
	sessions       sessionLister
	projects       projectGetter
	pr             prApplier
	logger         *slog.Logger
	interval       time.Duration
	observeTimeout time.Duration
	remoteResolver remoteResolver

	healthy atomic.Bool
}

// New constructs a Poller from its dependencies.
func New(d Deps) *Poller {
	p := &Poller{
		provider:       d.Provider,
		branches:       d.Branches,
		sessions:       d.Sessions,
		projects:       d.Projects,
		pr:             d.PR,
		logger:         d.Logger,
		interval:       d.Interval,
		observeTimeout: d.ObserveTimeout,
		remoteResolver: d.RemoteResolver,
	}
	if p.interval <= 0 {
		p.interval = DefaultInterval
	}
	if p.observeTimeout <= 0 {
		p.observeTimeout = DefaultObserveTimeout
	}
	if p.logger == nil {
		p.logger = slog.Default()
	}
	if p.remoteResolver == nil {
		p.remoteResolver = defaultRemoteResolver
	}
	p.healthy.Store(true)
	return p
}

// Healthy reports whether the SCM provider's authentication has been
// observed working since the poller started. It starts true and flips
// to false the first time the provider returns ErrAuthFailed; it does
// NOT auto-recover, because a single subsequent success could be an
// ETag-cached 304 that didn't actually exercise the token. A future
// health route consumes this bit; clearing it after token rotation is
// a daemon-restart concern.
func (p *Poller) Healthy() bool { return p.healthy.Load() }

// Start launches the background goroutine and returns a channel that
// closes once the loop has exited. The loop exits when ctx is cancelled;
// callers should wait on the returned channel before tearing down the
// PR Manager / lifecycle / store dependencies.
func (p *Poller) Start(ctx context.Context) <-chan struct{} {
	done := make(chan struct{})
	go p.loop(ctx, done)
	return done
}

func (p *Poller) loop(ctx context.Context, done chan<- struct{}) {
	defer close(done)
	t := time.NewTicker(p.interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if err := p.Tick(ctx); err != nil {
				p.logger.Error("scm poller: tick failed", "err", err)
			}
		}
	}
}

// Tick runs one observation cycle.
//
// It lists every session, skips terminated rows and rows without a
// branch, resolves each remaining session's open PR URL via the
// BranchPRFinder, asks the Provider for an observation under a
// per-call deadline, and hands a successful observation to the PR
// Manager. Errors are classified by sentinel:
//   - ErrRateLimited: short-circuit the rest of the tick (don't burn
//     through remaining sessions while GitHub is asking us to back off).
//   - ErrAuthFailed: flip Healthy() to false; continue with the next
//     session so a single misconfigured token does not stall the loop.
//   - other: log warn, continue.
//
// A session-listing failure is the only error Tick propagates; it
// short-circuits the cycle just like the reaper.
func (p *Poller) Tick(ctx context.Context) error {
	sessions, err := p.sessions.ListAllSessions(ctx)
	if err != nil {
		return err
	}
	for _, sess := range sessions {
		if sess.IsTerminated || sess.Metadata.Branch == "" {
			continue
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		stop := p.pollOne(ctx, sess)
		if stop {
			return nil
		}
	}
	return nil
}

// pollOne handles one session. Returns stop=true when the caller
// should short-circuit the remaining sessions (rate-limit signal).
func (p *Poller) pollOne(ctx context.Context, sess domain.SessionRecord) bool {
	prURL, err := p.resolvePRURL(ctx, sess)
	if err != nil {
		return p.classify(sess.ID, "resolve-pr-url", err)
	}
	if prURL == "" {
		p.logger.Debug("scm poller: no open PR for branch, skipping",
			"session", sess.ID, "branch", sess.Metadata.Branch)
		return false
	}

	pollCtx, cancel := context.WithTimeout(ctx, p.observeTimeout)
	defer cancel()
	obs, err := p.provider.Observe(pollCtx, prURL)
	if err != nil {
		return p.classify(sess.ID, "observe", err)
	}
	if !obs.Fetched {
		p.logger.Debug("scm poller: observation not fetched, skipping",
			"session", sess.ID, "url", prURL)
		return false
	}
	if err := p.pr.ApplyObservation(ctx, sess.ID, obs); err != nil {
		p.logger.Warn("scm poller: apply observation failed",
			"session", sess.ID, "err", err)
	}
	return false
}

// classify maps a Provider/lookup error to the loop's continue/stop
// decision and surfaces it in the logs. Auth-class failures flip the
// Healthy() bool; rate-limit signals stop the tick.
func (p *Poller) classify(sid domain.SessionID, stage string, err error) bool {
	switch {
	case errors.Is(err, scmgithub.ErrRateLimited):
		p.logger.Warn("scm poller: rate limited, skipping rest of tick",
			"session", sid, "stage", stage, "err", err)
		return true
	case errors.Is(err, scmgithub.ErrAuthFailed):
		p.healthy.Store(false)
		p.logger.Error("scm poller: auth failed, provider marked unhealthy",
			"session", sid, "stage", stage, "err", err)
		return false
	default:
		p.logger.Warn("scm poller: error",
			"session", sid, "stage", stage, "err", err)
		return false
	}
}

// resolvePRURL finds the open PR URL for a session's branch.
//
// v1 strategy: branch-based discovery. Look up the session's project,
// derive owner/repo from project.Repo (which today holds the origin URL),
// falling back to `git remote get-url origin` against the project's
// on-disk path, then ask BranchPRFinder. When neither yields an
// owner/repo, the session is silently skipped — that is not a poller bug,
// it's a project that hasn't been configured for SCM observation.
//
// When the session record grows a stored PR URL field (separate PR),
// this function should prefer it over branch discovery.
func (p *Poller) resolvePRURL(ctx context.Context, sess domain.SessionRecord) (string, error) {
	if p.branches == nil {
		return "", nil
	}
	res, err := p.projects.Get(ctx, sess.ProjectID)
	if err != nil {
		return "", err
	}
	if res.Project == nil {
		return "", nil
	}
	owner, repo, ok := ownerRepoFromProject(*res.Project)
	if !ok {
		remoteURL, err := p.remoteResolver(ctx, res.Project.Path)
		if err != nil {
			p.logger.Debug("scm poller: git remote lookup failed, skipping session",
				"session", sess.ID, "project", sess.ProjectID, "err", err)
			return "", nil
		}
		owner, repo, ok = parseGitHubRemote(remoteURL)
		if !ok {
			return "", nil
		}
	}
	return p.branches.FindOpenPRForBranch(ctx, owner, repo, sess.Metadata.Branch)
}

// ownerRepoFromProject derives (owner, repo) from a Project. Today
// project.Repo holds the origin URL (despite the type comment claiming
// "owner/name") — so we try both shapes here without touching the
// project package.
func ownerRepoFromProject(p project.Project) (owner, repo string, ok bool) {
	repoField := strings.TrimSpace(p.Repo)
	if repoField == "" {
		return "", "", false
	}
	if o, r, ok := parseGitHubRemote(repoField); ok {
		return o, r, true
	}
	return "", "", false
}

// parseGitHubRemote accepts both URL- and SSH-style remote strings and
// the bare "owner/repo" shorthand. It is intentionally host-agnostic —
// the github.Provider will reject non-github hosts at Observe time, so
// rejecting them here would just duplicate that check and silently drop
// legitimately-configured projects on enterprise hosts.
//
// Recognised forms:
//   - https://github.com/owner/repo[.git]
//   - http(s)://host/owner/repo[.git]
//   - git@host:owner/repo[.git]
//   - ssh://git@host/owner/repo[.git]
//   - owner/repo
func parseGitHubRemote(s string) (owner, repo string, ok bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", "", false
	}
	switch {
	case strings.HasPrefix(s, "git@"):
		idx := strings.Index(s, ":")
		if idx < 0 {
			return "", "", false
		}
		s = s[idx+1:]
	case strings.Contains(s, "://"):
		u, err := url.Parse(s)
		if err != nil || u.Host == "" {
			return "", "", false
		}
		s = strings.TrimPrefix(u.Path, "/")
	}
	s = strings.TrimSuffix(s, ".git")
	parts := strings.SplitN(s, "/", 3)
	if len(parts) < 2 {
		return "", "", false
	}
	owner = strings.TrimSpace(parts[0])
	repo = strings.TrimSpace(parts[1])
	if owner == "" || repo == "" {
		return "", "", false
	}
	return owner, repo, true
}

func defaultRemoteResolver(ctx context.Context, projectPath string) (string, error) {
	if strings.TrimSpace(projectPath) == "" {
		return "", errors.New("scm poller: project has no path")
	}
	out, err := exec.CommandContext(ctx, "git", "-C", projectPath, "remote", "get-url", "origin").Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}
