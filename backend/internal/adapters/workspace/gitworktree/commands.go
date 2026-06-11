package gitworktree

import "strings"

func checkRefFormatBranchArgs(repo, branch string) []string {
	return []string{"-C", repo, "check-ref-format", "--branch", branch}
}

func revParseVerifyArgs(repo, ref string) []string {
	return []string{"-C", repo, "rev-parse", "--verify", "--quiet", ref}
}

func worktreeAddBranchArgs(repo, path, branch string) []string {
	return []string{"-C", repo, "worktree", "add", path, branch}
}

func worktreeAddNewBranchArgs(repo, branch, path, baseRef string) []string {
	return []string{"-C", repo, "worktree", "add", "-b", branch, path, baseRef}
}

// worktreeRemoveArgs intentionally omits --force: a dirty worktree (uncommitted
// agent work) MUST cause `git worktree remove` to fail, so the post-prune
// "still registered" check in Destroy surfaces the refusal to the Session
// Manager's Cleanup, which routes the session to Skipped rather than deleting
// the agent's in-progress changes.
func worktreeRemoveArgs(repo, path string) []string {
	return []string{"-C", repo, "worktree", "remove", path}
}

func worktreePruneArgs(repo string) []string {
	return []string{"-C", repo, "worktree", "prune"}
}

// statusPorcelainArgs probes the worktree at path for uncommitted changes or
// untracked files — the condition `git worktree remove` (without --force)
// refuses on — so Destroy can classify a refusal as ports.ErrWorkspaceDirty.
func statusPorcelainArgs(path string) []string {
	return []string{"-C", path, "status", "--porcelain"}
}

func worktreeListPorcelainArgs(repo string) []string {
	return []string{"-C", repo, "worktree", "list", "--porcelain"}
}

func baseRefCandidates(branch, defaultBranch string) []string {
	candidates := []string{"origin/" + branch}
	if strings.Contains(defaultBranch, "/") {
		// A qualified default ("upstream/main") is used verbatim; git's refname
		// disambiguation already falls back to refs/heads/<defaultBranch>.
		candidates = append(candidates, defaultBranch)
	} else {
		// The local head comes after origin/<defaultBranch> so remote-tracking
		// still wins when present, but a remoteless repo can base new branches
		// on its local default branch instead of failing BRANCH_NOT_FETCHED.
		candidates = append(candidates, "origin/"+defaultBranch, "refs/heads/"+defaultBranch)
	}
	return append(candidates, branch)
}
