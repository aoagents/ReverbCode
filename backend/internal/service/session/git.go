package session

import (
	"context"
	"errors"
	"fmt"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// Session git operations: thin orchestration over the WorkspaceGit port. The
// service owns resolving a session to its workspace path and mapping adapter
// sentinels to typed API errors; all git mechanics live in the adapter.

// GitStatus reports the session workspace's branch and uncommitted files.
func (s *Service) GitStatus(ctx context.Context, id domain.SessionID) (ports.GitStatus, error) {
	path, err := s.gitWorkspacePath(ctx, id)
	if err != nil {
		return ports.GitStatus{}, err
	}
	status, err := s.workspaceGit.Status(ctx, path)
	if err != nil {
		return ports.GitStatus{}, toGitAPIError(err)
	}
	return status, nil
}

// GitStageAll stages every change in the session workspace.
func (s *Service) GitStageAll(ctx context.Context, id domain.SessionID) error {
	path, err := s.gitWorkspacePath(ctx, id)
	if err != nil {
		return err
	}
	return toGitAPIError(s.workspaceGit.StageAll(ctx, path))
}

// GitDiscardAll throws away all uncommitted work in the session workspace.
func (s *Service) GitDiscardAll(ctx context.Context, id domain.SessionID) error {
	path, err := s.gitWorkspacePath(ctx, id)
	if err != nil {
		return err
	}
	return toGitAPIError(s.workspaceGit.DiscardAll(ctx, path))
}

// GitCommitAll stages and commits everything in the session workspace,
// optionally pushing the branch to its remote.
func (s *Service) GitCommitAll(ctx context.Context, id domain.SessionID, message string, push bool) (ports.GitCommitResult, error) {
	path, err := s.gitWorkspacePath(ctx, id)
	if err != nil {
		return ports.GitCommitResult{}, err
	}
	result, err := s.workspaceGit.CommitAll(ctx, path, message, push)
	if err != nil {
		return result, toGitAPIError(err)
	}
	return result, nil
}

// gitWorkspacePath resolves a session to the worktree its git routes operate
// on, with typed errors for the ways that can fail.
func (s *Service) gitWorkspacePath(ctx context.Context, id domain.SessionID) (string, error) {
	if s.workspaceGit == nil {
		return "", apierr.Conflict("GIT_UNAVAILABLE", "Git operations are not available on this daemon", nil)
	}
	rec, ok, err := s.store.GetSession(ctx, id)
	if err != nil {
		return "", fmt.Errorf("get session %s: %w", id, err)
	}
	if !ok {
		return "", apierr.NotFound("SESSION_NOT_FOUND", "Unknown session")
	}
	if rec.Metadata.WorkspacePath == "" {
		return "", apierr.Conflict("SESSION_NO_WORKSPACE", "Session has no workspace", nil)
	}
	return rec.Metadata.WorkspacePath, nil
}

func toGitAPIError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, ports.ErrGitNothingToCommit):
		return apierr.Conflict("GIT_NOTHING_TO_COMMIT", "No changes to commit", nil)
	case errors.Is(err, ports.ErrGitNoRemote):
		return apierr.Conflict("GIT_NO_REMOTE", err.Error(), nil)
	default:
		return err
	}
}
