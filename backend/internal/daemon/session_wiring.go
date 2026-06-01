package daemon

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/claudecode"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/portshim"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/messenger/composite"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/messenger/inbox"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/messenger/panep"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/workspace/gitworktree"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/workspace/gitworktree/projectresolver"
	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/lifecycle"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/project"
	"github.com/aoagents/agent-orchestrator/backend/internal/service"
	sessionmanager "github.com/aoagents/agent-orchestrator/backend/internal/session_manager"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
)

// sessionStack groups the per-session collaborators the daemon assembles around
// the Session Manager. The controller-facing surface is *service.Session, which
// wraps the internal session_manager.Manager with read-model assembly.
type sessionStack struct {
	svc       *service.Session
	workspace ports.Workspace
	messenger ports.AgentMessenger
}

// buildSessionStack assembles the session-runtime stack: gitworktree workspace
// over a project-store-backed RepoResolver, claudecode-via-portshim agent,
// inbox-file AgentMessenger, the internal session_manager.Manager, and the
// service.Session wrapper that the HTTP controller binds to. The runtime, lcm,
// projects, and store passed in are the same instances the rest of the daemon
// uses, so there is one source of truth per collaborator.
func buildSessionStack(cfg config.Config, store *sqlite.Store, runtime ports.Runtime, projects project.Manager, lcm *lifecycle.Manager, messenger ports.AgentMessenger) (*sessionStack, error) {
	ws, err := gitworktree.New(gitworktree.Options{
		ManagedRoot:  filepath.Join(cfg.DataDir, "worktrees"),
		RepoResolver: projectresolver.New(projects),
	})
	if err != nil {
		return nil, fmt.Errorf("gitworktree: %w", err)
	}
	sm := sessionmanager.New(sessionmanager.Deps{
		Runtime:   runtime,
		Agent:     portshim.New(claudecode.New()),
		Workspace: ws,
		Store:     store,
		Messenger: messenger,
		Lifecycle: lcm,
	})
	svc := service.NewSession(sm, store)
	return &sessionStack{svc: svc, workspace: ws, messenger: messenger}, nil
}

// storeWorkspaceLookup adapts the sqlite store to the SessionWorkspace lookup
// the inbox messenger needs. WorkspacePath becomes meaningful only after the
// LCM records spawn metadata, so a session that exists but has no path is an
// error — Send must not invent a destination.
type storeWorkspaceLookup struct{ store *sqlite.Store }

func newStoreWorkspaceLookup(store *sqlite.Store) inbox.SessionWorkspace {
	return storeWorkspaceLookup{store: store}
}

func (s storeWorkspaceLookup) WorkspacePath(ctx context.Context, id domain.SessionID) (string, error) {
	rec, ok, err := s.store.GetSession(ctx, id)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", fmt.Errorf("session %s not found", id)
	}
	return rec.Metadata.WorkspacePath, nil
}

// storeSessionHandleLookup adapts the sqlite store to panep.SessionLookup.
// panep needs the runtime handle id (to address the right zellij pane) and the
// workspace path (proof the inbox messenger had a real directory to write to).
type storeSessionHandleLookup struct{ store *sqlite.Store }

func newStoreSessionHandleLookup(store *sqlite.Store) panep.SessionLookup {
	return storeSessionHandleLookup{store: store}
}

func (s storeSessionHandleLookup) SessionHandle(ctx context.Context, id domain.SessionID) (string, string, error) {
	rec, ok, err := s.store.GetSession(ctx, id)
	if err != nil {
		return "", "", err
	}
	if !ok {
		return "", "", fmt.Errorf("session %s not found", id)
	}
	return rec.Metadata.RuntimeHandleID, rec.Metadata.WorkspacePath, nil
}

// newSessionMessenger assembles the per-daemon agent messenger: inbox (durable
// file write, primary) wrapped in a composite with panep (live pane ping,
// best-effort secondary). Ordering matters — see composite.Messenger for the
// "primary must succeed, secondaries are nudges" contract.
func newSessionMessenger(store *sqlite.Store, runtime panep.RuntimePaneWriter, logger *slog.Logger) ports.AgentMessenger {
	inboxMsg := inbox.New(newStoreWorkspaceLookup(store))
	panepMsg := panep.New(runtime, newStoreSessionHandleLookup(store))
	return composite.New([]ports.AgentMessenger{inboxMsg, panepMsg}, logger)
}
