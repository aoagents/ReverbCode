package daemon

import (
	"context"
	"errors"
	"log/slog"

	scmgithub "github.com/aoagents/agent-orchestrator/backend/internal/adapters/scm/github"
	"github.com/aoagents/agent-orchestrator/backend/internal/lifecycle"
	"github.com/aoagents/agent-orchestrator/backend/internal/observe/scm"
	"github.com/aoagents/agent-orchestrator/backend/internal/pr"
	"github.com/aoagents/agent-orchestrator/backend/internal/project"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
)

// scmStack owns the SCM observation loop: a GitHub Provider, a pr.Manager
// that writes PR rows and forwards observations to lifecycle for nudges,
// and the polling goroutine that drives both. A nil-token environment
// degrades gracefully — the daemon still runs locally without SCM
// observation; PR-driven nudges (CI-failure log tail, review feedback,
// merge-conflict rebase) will not fire until a token is supplied.
type scmStack struct {
	pollerDone <-chan struct{}
}

// startSCM constructs and starts the SCM observation stack. The Provider
// reads its token from AO_GITHUB_TOKEN (preferred) or GITHUB_TOKEN, both
// via os.Getenv. Without a token, the poller is not started and a no-op
// done channel is returned — Stop is a free call in that case.
func startSCM(ctx context.Context, store *sqlite.Store, projects project.Manager, lcm *lifecycle.Manager, log *slog.Logger) *scmStack {
	tokenSource := scmgithub.EnvTokenSource{EnvVars: []string{"AO_GITHUB_TOKEN", "GITHUB_TOKEN"}}
	provider, err := scmgithub.NewProvider(scmgithub.ProviderOptions{Token: tokenSource})
	if err != nil {
		if errors.Is(err, scmgithub.ErrNoToken) {
			log.Info("scm poller: no GITHUB_TOKEN configured, SCM observation disabled")
		} else {
			log.Warn("scm poller: provider construction failed, SCM observation disabled", "err", err)
		}
		return &scmStack{pollerDone: closedDone()}
	}
	prMgr := pr.New(pr.Deps{Writer: store, Lifecycle: lcm})
	poller := scm.New(scm.Deps{
		Provider: provider,
		Branches: provider,
		Sessions: store,
		Projects: projects,
		PR:       prMgr,
		Logger:   log,
	})
	return &scmStack{pollerDone: poller.Start(ctx)}
}

// Stop waits for the poller goroutine to exit. The caller must cancel the
// ctx passed to startSCM before calling Stop.
func (s *scmStack) Stop() { <-s.pollerDone }

func closedDone() <-chan struct{} {
	ch := make(chan struct{})
	close(ch)
	return ch
}
