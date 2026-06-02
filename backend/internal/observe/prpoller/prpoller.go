// Package prpoller implements the OBSERVE-layer polling timer that discovers
// and refreshes pull-request state for live sessions.
//
// Each tick enumerates non-terminated sessions, resolves each session's branch
// to an open PR (self-discovery via the SCM, keyed on the session's branch),
// observes that PR, and feeds the observation into the PR service — which
// persists it and drives lifecycle nudges. The poller reports facts only; it
// never writes session or PR rows directly.
package prpoller

import (
	"context"
	"log/slog"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// DefaultTickInterval is the cadence used when Config.Tick is zero. PR state
// changes far slower than runtime liveness, so this is coarser than the
// reaper's 5s probe and stays well inside GitHub's REST budget.
const DefaultTickInterval = 30 * time.Second

type sessionSource interface {
	ListAllSessions(ctx context.Context) ([]domain.SessionRecord, error)
}

type prDiscoverer interface {
	// FindPRForBranch resolves the open PR for owner/repo's branch. found=false
	// with a nil error is the normal pre-PR state of a fresh session.
	FindPRForBranch(ctx context.Context, owner, repo, branch string) (url string, found bool, err error)
}

type prObserver interface {
	Observe(ctx context.Context, prURL string) (ports.PRObservation, error)
}

type observationSink interface {
	ApplyObservation(ctx context.Context, id domain.SessionID, o ports.PRObservation) error
}

// repoResolver maps a project to its github owner/repo (from the project's
// origin remote). The poller caches results per project within a tick.
type repoResolver interface {
	RepoIdent(ctx context.Context, projectID domain.ProjectID) (owner, repo string, err error)
}

// Config holds the tunable knobs for a Poller. Every field is optional; zero
// values fall back to safe defaults.
type Config struct {
	// Tick is the interval between cycles. <=0 means DefaultTickInterval.
	Tick time.Duration
	// Logger receives operational diagnostics. A failed discovery/observe for
	// one session is logged but never propagated — it must not kill the loop.
	// nil means slog.Default.
	Logger *slog.Logger
}

// Poller is the PR polling timer. Construct it with New; start the background
// goroutine with Start, or drive a single cycle synchronously with Tick.
type Poller struct {
	sessions   sessionSource
	discoverer prDiscoverer
	observer   prObserver
	sink       observationSink
	repos      repoResolver
	tick       time.Duration
	logger     *slog.Logger
}

// New constructs a Poller. sessions supplies the rows to poll; discoverer finds
// a branch's open PR; observer fetches its state; sink persists+reacts; repos
// resolves a project's github owner/repo.
func New(sessions sessionSource, discoverer prDiscoverer, observer prObserver, sink observationSink, repos repoResolver, cfg Config) *Poller {
	p := &Poller{
		sessions:   sessions,
		discoverer: discoverer,
		observer:   observer,
		sink:       sink,
		repos:      repos,
		tick:       cfg.Tick,
		logger:     cfg.Logger,
	}
	if p.tick <= 0 {
		p.tick = DefaultTickInterval
	}
	if p.logger == nil {
		p.logger = slog.Default()
	}
	return p
}

// Start launches the background goroutine and returns a channel that closes
// once the loop has exited. The loop exits on ctx cancellation; wait on the
// channel after cancel to confirm a clean stop before tearing down deps.
func (p *Poller) Start(ctx context.Context) <-chan struct{} {
	done := make(chan struct{})
	go p.loop(ctx, done)
	return done
}

func (p *Poller) loop(ctx context.Context, done chan<- struct{}) {
	defer close(done)
	t := time.NewTicker(p.tick)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if err := p.Tick(ctx); err != nil {
				p.logger.Error("prpoller: tick failed", "err", err)
			}
		}
	}
}

// Tick runs one cycle: enumerate non-terminated sessions with a branch, resolve
// each to an open PR, observe it, and feed the observation to the sink. Only
// the session-listing failure is propagated (it short-circuits the cycle);
// per-session failures are logged so one bad session can't stall the rest.
func (p *Poller) Tick(ctx context.Context) error {
	sessions, err := p.sessions.ListAllSessions(ctx)
	if err != nil {
		return err
	}
	// Cache owner/repo per project for the duration of the tick: many sessions
	// share a project, and remote resolution may shell out to git.
	idents := map[domain.ProjectID]repoIdent{}
	for _, sess := range sessions {
		if sess.IsTerminated || sess.Metadata.Branch == "" {
			continue
		}
		p.pollOne(ctx, sess, idents)
	}
	return nil
}

type repoIdent struct {
	owner string
	repo  string
	ok    bool // resolution succeeded; a failed lookup is cached as !ok to avoid retry-storming within a tick
}

func (p *Poller) pollOne(ctx context.Context, sess domain.SessionRecord, idents map[domain.ProjectID]repoIdent) {
	ident, cached := idents[sess.ProjectID]
	if !cached {
		owner, repo, err := p.repos.RepoIdent(ctx, sess.ProjectID)
		ident = repoIdent{owner: owner, repo: repo, ok: err == nil}
		if err != nil {
			p.logger.Debug("prpoller: could not resolve project repo, skipping",
				"project", sess.ProjectID, "err", err)
		}
		idents[sess.ProjectID] = ident
	}
	if !ident.ok {
		return
	}

	prURL, found, err := p.discoverer.FindPRForBranch(ctx, ident.owner, ident.repo, sess.Metadata.Branch)
	if err != nil {
		p.logger.Debug("prpoller: PR discovery failed",
			"session", sess.ID, "branch", sess.Metadata.Branch, "err", err)
		return
	}
	if !found {
		return
	}

	obs, err := p.observer.Observe(ctx, prURL)
	if err != nil {
		p.logger.Debug("prpoller: PR observation failed",
			"session", sess.ID, "pr", prURL, "err", err)
		return
	}

	if err := p.sink.ApplyObservation(ctx, sess.ID, obs); err != nil {
		p.logger.Error("prpoller: ApplyObservation failed",
			"session", sess.ID, "pr", prURL, "err", err)
	}
}
