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

func worktreeListPorcelainArgs(repo string) []string {
	return []string{"-C", repo, "worktree", "list", "--porcelain"}
}

func remoteGetURLOriginArgs(repo string) []string {
	return []string{"-C", repo, "remote", "get-url", "origin"}
}

// baseRefCandidates returns the ordered list of refs to probe for a new
// worktree's base. When hasOrigin is true we prefer remote-tracking refs;
// when false we skip them entirely so we don't burn subprocesses on lookups
// that can't succeed. defaultBranch may already be qualified (e.g.
// "upstream/main"), in which case it is used as-is.
func baseRefCandidates(branch, defaultBranch string, hasOrigin bool) []string {
	var candidates []string
	if hasOrigin {
		candidates = append(candidates, "origin/"+branch)
		if strings.Contains(defaultBranch, "/") {
			candidates = append(candidates, defaultBranch)
		} else {
			candidates = append(candidates, "origin/"+defaultBranch)
		}
	} else if strings.Contains(defaultBranch, "/") {
		candidates = append(candidates, defaultBranch)
	}
	return append(candidates, branch)
}
