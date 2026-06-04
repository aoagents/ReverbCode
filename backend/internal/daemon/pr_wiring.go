package daemon

import (
	"context"
	"log/slog"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/scm/github"
	"github.com/aoagents/agent-orchestrator/backend/internal/lifecycle"
	"github.com/aoagents/agent-orchestrator/backend/internal/observe/prpoller"
	prsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/pr"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
)

// prPollerStack owns the PR poller goroutine. When no GitHub token is
// configured the stack is inert (closed done channel, nothing started): PR
// observation degrades gracefully rather than failing daemon startup.
type prPollerStack struct {
	done <-chan struct{}
}

// Stop waits for the poller goroutine to exit. The caller must cancel the ctx
// passed to startPRPoller first. Safe on an inert stack.
func (s *prPollerStack) Stop() {
	if s == nil || s.done == nil {
		return
	}
	<-s.done
}

// startPRPoller wires the PR observation path: a GitHub provider (token from
// env, falling back to `gh auth token`), the PR service (persist + lifecycle
// nudges over the shared store/LCM), and the poller that self-discovers each
// live session's PR from its branch. If no token resolves, the poller is
// skipped and the daemon runs without PR observation.
func startPRPoller(ctx context.Context, store *sqlite.Store, lcm *lifecycle.Manager, log *slog.Logger) *prPollerStack {
	tokens := githubTokenSource()
	if _, err := tokens.Token(ctx); err != nil {
		log.Warn("PR poller disabled: no GitHub token (set AO_GITHUB_TOKEN/GITHUB_TOKEN or run `gh auth login`)", "err", err)
		return &prPollerStack{}
	}

	provider, err := github.NewProvider(github.ProviderOptions{Token: tokens})
	if err != nil {
		log.Warn("PR poller disabled: could not build GitHub provider", "err", err)
		return &prPollerStack{}
	}

	manager := prsvc.New(prsvc.Deps{Writer: store, Lifecycle: lcm})
	resolver := gitRepoResolver{store: store}
	poller := prpoller.New(store, provider, provider, manager, resolver, prpoller.Config{Logger: log})

	log.Info("PR poller started", "interval", prpoller.DefaultTickInterval)
	return &prPollerStack{done: poller.Start(ctx)}
}

// githubTokenSource resolves a token from env first (AO_GITHUB_TOKEN, then
// GITHUB_TOKEN via EnvTokenSource's built-in fallback), then `gh auth token`.
func githubTokenSource() github.TokenSource {
	return chainTokenSource{
		github.EnvTokenSource{EnvVars: []string{"AO_GITHUB_TOKEN"}},
		&github.GHTokenSource{},
	}
}

// chainTokenSource returns the first source that yields a token, so a CI env
// var wins but a developer's `gh` login still works with no env set up.
type chainTokenSource []github.TokenSource

func (c chainTokenSource) Token(ctx context.Context) (string, error) {
	var lastErr error
	for _, s := range c {
		tok, err := s.Token(ctx)
		if err == nil && tok != "" {
			return tok, nil
		}
		lastErr = err
	}
	if lastErr == nil {
		lastErr = github.ErrNoToken
	}
	return "", lastErr
}

// InvalidateToken forwards to any chained source that supports invalidation, so
// a rotated token is picked up on the next request.
func (c chainTokenSource) InvalidateToken() {
	for _, s := range c {
		if inv, ok := s.(interface{ InvalidateToken() }); ok {
			inv.InvalidateToken()
		}
	}
}
