package daemon

import (
	"context"
	"fmt"
	"os/exec"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// gitRepoResolver maps a project to its github owner/repo by reading the
// project's on-disk repo path from the store and parsing its origin remote. It
// backs the PR poller's per-project owner/repo lookup.
type gitRepoResolver struct {
	store projectPathStore
	// git is the shell-out hook; nil falls back to the real git binary. Tests
	// inject a fake to avoid requiring a repo on disk.
	git func(ctx context.Context, repoPath string) (string, error)
}

type projectPathStore interface {
	GetProject(ctx context.Context, id string) (domain.ProjectRecord, bool, error)
}

// RepoIdent resolves owner/repo for projectID. It fails (rather than guessing)
// when the project is unregistered, has no repo path, or its origin remote
// isn't a parseable github URL — the poller treats a failure as "skip", so a
// non-github project simply never gets PR observations.
func (r gitRepoResolver) RepoIdent(ctx context.Context, projectID domain.ProjectID) (string, string, error) {
	rec, ok, err := r.store.GetProject(ctx, string(projectID))
	if err != nil {
		return "", "", fmt.Errorf("look up project %q: %w", projectID, err)
	}
	if !ok || rec.Path == "" {
		return "", "", fmt.Errorf("project %q has no repo path on record", projectID)
	}
	run := r.git
	if run == nil {
		run = gitOriginURL
	}
	remote, err := run(ctx, rec.Path)
	if err != nil {
		return "", "", fmt.Errorf("read origin remote for %q: %w", projectID, err)
	}
	return parseOwnerRepo(remote)
}

func gitOriginURL(ctx context.Context, repoPath string) (string, error) {
	out, err := exec.CommandContext(ctx, "git", "-C", repoPath, "remote", "get-url", "origin").Output()
	if err != nil {
		return "", err
	}
	return string(out), nil
}

// parseOwnerRepo extracts owner/repo from the common github remote URL shapes:
//
//	https://github.com/owner/repo(.git)
//	git@github.com:owner/repo(.git)
//	ssh://git@github.com/owner/repo(.git)
//
// Only github hosts are accepted; anything else returns an error so the poller
// skips the project rather than POSTing to a non-github API.
func parseOwnerRepo(remote string) (string, string, error) {
	s := strings.TrimSpace(remote)
	if s == "" {
		return "", "", fmt.Errorf("empty origin remote")
	}
	// Normalise scp-style (git@host:owner/repo) to a slash-delimited tail.
	if !strings.Contains(s, "://") {
		if at := strings.Index(s, "@"); at >= 0 {
			s = s[at+1:]
		}
		s = strings.Replace(s, ":", "/", 1)
	} else {
		s = s[strings.Index(s, "://")+len("://"):]
		if at := strings.Index(s, "@"); at >= 0 {
			s = s[at+1:]
		}
	}
	// s is now host/owner/repo(.git)[/...]. Require a github host.
	parts := strings.Split(strings.Trim(s, "/"), "/")
	if len(parts) < 3 {
		return "", "", fmt.Errorf("origin remote %q is not an owner/repo url", remote)
	}
	host := strings.ToLower(parts[0])
	if host != "github.com" && host != "www.github.com" && !strings.HasSuffix(host, ".github.com") && !strings.HasSuffix(host, ".ghe.io") {
		return "", "", fmt.Errorf("origin remote host %q is not github", host)
	}
	owner := parts[1]
	repo := strings.TrimSuffix(parts[2], ".git")
	if owner == "" || repo == "" {
		return "", "", fmt.Errorf("origin remote %q is missing owner or repo", remote)
	}
	return owner, repo, nil
}
