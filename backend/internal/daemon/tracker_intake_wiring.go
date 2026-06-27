package daemon

import (
	"context"
	"errors"
	"log/slog"
	"os/exec"
	"strings"
	"sync"
	"time"

	trackergithub "github.com/aoagents/agent-orchestrator/backend/internal/adapters/tracker/github"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	trackerintake "github.com/aoagents/agent-orchestrator/backend/internal/observe/trackerintake"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	sessionsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/session"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
)

// startTrackerIntake wires the opt-in issue-intake loop. The tracker itself is
// lazy so daemon startup and projects without intake enabled do not pay an auth
// or network cost.
func startTrackerIntake(ctx context.Context, store *sqlite.Store, sessions *sessionsvc.Service, logger *slog.Logger) <-chan struct{} {
	observer := trackerintake.New(newLazyGitHubTracker(logger), store, sessions, trackerintake.Config{Logger: logger})
	return observer.Start(ctx)
}

type lazyGitHubTracker struct {
	logger  *slog.Logger
	tokens  *trackerTokenSource
	mu      sync.Mutex
	tracker ports.Tracker
}

func newLazyGitHubTracker(logger *slog.Logger) *lazyGitHubTracker {
	return &lazyGitHubTracker{logger: logger, tokens: &trackerTokenSource{}}
}

func (t *lazyGitHubTracker) Get(ctx context.Context, id domain.TrackerID) (domain.Issue, error) {
	tracker, err := t.resolve()
	if err != nil {
		return domain.Issue{}, err
	}
	return tracker.Get(ctx, id)
}

func (t *lazyGitHubTracker) List(ctx context.Context, repo domain.TrackerRepo, filter domain.ListFilter) ([]domain.Issue, error) {
	tracker, err := t.resolve()
	if err != nil {
		return nil, err
	}
	return tracker.List(ctx, repo, filter)
}

func (t *lazyGitHubTracker) Preflight(ctx context.Context) error {
	tracker, err := t.resolve()
	if err != nil {
		return err
	}
	return tracker.Preflight(ctx)
}

func (t *lazyGitHubTracker) resolve() (ports.Tracker, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.tracker != nil {
		return t.tracker, nil
	}
	tracker, err := trackergithub.New(trackergithub.Options{Token: t.tokens})
	if err != nil {
		if errors.Is(err, trackergithub.ErrNoToken) {
			t.logger.Warn("tracker intake disabled: no usable GitHub token", "err", err)
		}
		return nil, err
	}
	t.tracker = tracker
	return tracker, nil
}

const (
	trackerTokenCacheTTL       = 5 * time.Minute
	trackerTokenCommandTimeout = 5 * time.Second
)

// trackerTokenSource mirrors the SCM credential precedence while returning the
// tracker adapter's own ErrNoToken sentinel.
type trackerTokenSource struct {
	mu        sync.Mutex
	token     string
	expiresAt time.Time
}

func (s *trackerTokenSource) Token(ctx context.Context) (string, error) {
	env := trackergithub.EnvTokenSource{EnvVars: []string{"AO_GITHUB_TOKEN"}}
	if tok, err := env.Token(ctx); err == nil {
		return tok, nil
	} else if !errors.Is(err, trackergithub.ErrNoToken) {
		return "", err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	if s.token != "" && now.Before(s.expiresAt) {
		return s.token, nil
	}
	cmdCtx, cancel := context.WithTimeout(ctx, trackerTokenCommandTimeout)
	defer cancel()
	out, err := exec.CommandContext(cmdCtx, "gh", "auth", "token").Output()
	if err != nil {
		return "", err
	}
	token := strings.TrimSpace(string(out))
	if token == "" {
		return "", trackergithub.ErrNoToken
	}
	s.token = token
	s.expiresAt = now.Add(trackerTokenCacheTTL)
	return token, nil
}
