package project

import (
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// GitChecker reports whether a filesystem path is the root of a git repository.
// It is the seam that lets the project Service be exercised without a real git
// binary or working tree.
type GitChecker interface {
	IsRepo(path string) bool
}

// execGitChecker is the production GitChecker: it shells out to git.
type execGitChecker struct{}

func (execGitChecker) IsRepo(path string) bool {
	cmd := exec.Command("git", "-C", path, "rev-parse", "--show-toplevel")
	out, err := cmd.Output()
	if err != nil {
		return false
	}
	top := filepath.Clean(strings.TrimSpace(string(out)))
	path = filepath.Clean(path)
	top, err = filepath.EvalSymlinks(top)
	if err != nil {
		return false
	}
	path, err = filepath.EvalSymlinks(path)
	if err != nil {
		return false
	}
	return samePath(top, path)
}

// samePath compares two cleaned, symlink-resolved paths. It is case-insensitive
// only on filesystems that are conventionally case-insensitive (macOS, Windows);
// on case-sensitive filesystems (Linux), "/home/u/Repo" and "/home/u/repo" are
// distinct directories and must not be treated as equal.
func samePath(a, b string) bool {
	if runtime.GOOS == "darwin" || runtime.GOOS == "windows" {
		return strings.EqualFold(a, b)
	}
	return a == b
}
