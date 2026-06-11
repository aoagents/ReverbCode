package gitworktree

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// GitOps implements ports.WorkspaceGit over the git CLI. It shares the
// adapter's exec conventions (runCommand) but operates inside an existing
// session workspace rather than managing worktree lifecycles.
type GitOps struct {
	gitBinary string
}

// NewGitOps returns a WorkspaceGit adapter using the `git` on PATH.
func NewGitOps() *GitOps {
	return &GitOps{gitBinary: defaultGitBinary}
}

var _ ports.WorkspaceGit = (*GitOps)(nil)

func (g *GitOps) git(ctx context.Context, dir string, args ...string) ([]byte, error) {
	return runCommand(ctx, g.gitBinary, append([]string{"-C", dir}, args...)...)
}

// Status reports the workspace branch and its uncommitted files. Line counts
// come from `diff --numstat` for tracked files and a direct line count for
// untracked ones; exotic paths that fail the numstat lookup degrade to 0/0
// rather than failing the whole status.
func (g *GitOps) Status(ctx context.Context, path string) (ports.GitStatus, error) {
	branchOut, err := g.git(ctx, path, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return ports.GitStatus{}, fmt.Errorf("gitops: resolve branch: %w", err)
	}
	branch := strings.TrimSpace(string(branchOut))

	porcelain, err := g.git(ctx, path, "status", "--porcelain", "-z")
	if err != nil {
		return ports.GitStatus{}, fmt.Errorf("gitops: status: %w", err)
	}

	// One numstat against HEAD covers staged and unstaged edits together. It
	// can fail legitimately (unborn HEAD); porcelain still lists files then.
	counts := map[string][2]int{}
	if out, err := g.git(ctx, path, "diff", "--numstat", "HEAD", "--"); err == nil {
		mergeNumstat(counts, out)
	}

	status := ports.GitStatus{Branch: branch, Files: []ports.GitFileChange{}}
	entries := strings.Split(string(porcelain), "\x00")
	for i := 0; i < len(entries); i++ {
		entry := entries[i]
		if len(entry) < 4 {
			continue
		}
		xy, filePath := entry[:2], entry[3:]
		// Renames/copies carry the original path as the next NUL entry.
		if xy[0] == 'R' || xy[0] == 'C' {
			i++
		}
		change := ports.GitFileChange{
			Path:   filePath,
			Staged: xy[0] != ' ' && xy[0] != '?',
		}
		if c, ok := counts[filePath]; ok {
			change.Additions, change.Deletions = c[0], c[1]
		} else if xy == "??" {
			change.Additions = countFileLines(filepath.Join(path, filePath))
		}
		status.Files = append(status.Files, change)
	}
	return status, nil
}

// StageAll stages every change in the workspace, including untracked files.
func (g *GitOps) StageAll(ctx context.Context, path string) error {
	if _, err := g.git(ctx, path, "add", "-A"); err != nil {
		return fmt.Errorf("gitops: stage all: %w", err)
	}
	return nil
}

// DiscardAll resets tracked files to HEAD and removes untracked files and
// directories. Destructive by design; callers own the confirmation UX.
//
// Gitignored files deliberately survive (`clean -fd`, not `-fdx`): discard
// means "drop my changes", not "wipe the workspace" — node_modules, build
// caches, and the gitignored AO session/hook files must keep the worktree
// usable after a discard.
func (g *GitOps) DiscardAll(ctx context.Context, path string) error {
	if _, err := g.git(ctx, path, "reset", "--hard", "HEAD"); err != nil {
		return fmt.Errorf("gitops: reset: %w", err)
	}
	if _, err := g.git(ctx, path, "clean", "-fd"); err != nil {
		return fmt.Errorf("gitops: clean: %w", err)
	}
	return nil
}

// CommitAll stages everything, commits, and (optionally) pushes the branch to
// the repo's remote, preferring "origin" when several exist. A push request
// against a remoteless repo returns ErrGitNoRemote with the commit SHA in the
// message — the commit itself has already landed.
func (g *GitOps) CommitAll(ctx context.Context, path, message string, push bool) (ports.GitCommitResult, error) {
	porcelain, err := g.git(ctx, path, "status", "--porcelain")
	if err != nil {
		return ports.GitCommitResult{}, fmt.Errorf("gitops: status: %w", err)
	}
	if strings.TrimSpace(string(porcelain)) == "" {
		return ports.GitCommitResult{}, ports.ErrGitNothingToCommit
	}

	if err := g.StageAll(ctx, path); err != nil {
		return ports.GitCommitResult{}, err
	}
	if _, err := g.git(ctx, path, "commit", "-m", message); err != nil {
		return ports.GitCommitResult{}, fmt.Errorf("gitops: commit: %w", err)
	}

	shaOut, err := g.git(ctx, path, "rev-parse", "HEAD")
	if err != nil {
		return ports.GitCommitResult{}, fmt.Errorf("gitops: resolve sha: %w", err)
	}
	branchOut, err := g.git(ctx, path, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return ports.GitCommitResult{}, fmt.Errorf("gitops: resolve branch: %w", err)
	}
	result := ports.GitCommitResult{
		SHA:    strings.TrimSpace(string(shaOut)),
		Branch: strings.TrimSpace(string(branchOut)),
	}
	if !push {
		return result, nil
	}

	remote, err := g.pickRemote(ctx, path)
	if err != nil {
		return result, fmt.Errorf("gitops: committed %s: %w", result.SHA, err)
	}
	if _, err := g.git(ctx, path, "push", "--set-upstream", remote, "HEAD"); err != nil {
		return result, fmt.Errorf("gitops: committed %s, push failed: %w", result.SHA, err)
	}
	result.Pushed = true
	return result, nil
}

func (g *GitOps) pickRemote(ctx context.Context, path string) (string, error) {
	out, err := g.git(ctx, path, "remote")
	if err != nil {
		return "", fmt.Errorf("list remotes: %w", err)
	}
	remotes := strings.Fields(string(out))
	if len(remotes) == 0 {
		return "", ports.ErrGitNoRemote
	}
	for _, remote := range remotes {
		if remote == "origin" {
			return remote, nil
		}
	}
	return remotes[0], nil
}

// mergeNumstat folds `diff --numstat` output ("adds\tdels\tpath" per line)
// into counts, summing across calls. Binary files report "-" and count as 0.
func mergeNumstat(counts map[string][2]int, out []byte) {
	for _, line := range strings.Split(string(out), "\n") {
		parts := strings.SplitN(line, "\t", 3)
		if len(parts) != 3 {
			continue
		}
		adds, _ := strconv.Atoi(parts[0])
		dels, _ := strconv.Atoi(parts[1])
		existing := counts[parts[2]]
		counts[parts[2]] = [2]int{existing[0] + adds, existing[1] + dels}
	}
}

// countFileLines sizes an untracked file's "+N" the way numstat would once
// staged. Unreadable or oversized files degrade to 0 rather than erroring.
const maxCountableFileBytes = 4 << 20

func countFileLines(path string) int {
	info, err := os.Stat(path)
	if err != nil || info.IsDir() || info.Size() > maxCountableFileBytes {
		return 0
	}
	data, err := os.ReadFile(path)
	if err != nil || len(data) == 0 {
		return 0
	}
	lines := bytes.Count(data, []byte("\n"))
	if data[len(data)-1] != '\n' {
		lines++
	}
	return lines
}
