package legacyimport

import (
	"io"
	"os"
	"path/filepath"
	"regexp"
)

// claudeSlugRE matches every character Claude Code replaces with "-" when it
// buckets a cwd's transcripts under ~/.claude/projects/<slug>/. The rule
// (empirically verified, issue #2129 §9) is: realpath(cwd) with every char
// outside [a-zA-Z0-9-] replaced by "-". A leading "/" therefore becomes a
// leading "-".
var claudeSlugRE = regexp.MustCompile(`[^a-zA-Z0-9-]`)

func claudeSlug(path string) string {
	return claudeSlugRE.ReplaceAllString(path, "-")
}

// transcriptCopyPlan is the resolved source + destination of a transcript copy.
type transcriptCopyPlan struct {
	uuid       string
	sourcePath string // ~/.claude/projects/<sourceSlug>/<uuid>.jsonl
	destPath   string // ~/.claude/projects/<destSlug>/<uuid>.jsonl
}

// planTranscriptCopy computes the source + destination transcript paths.
//
// Claude Code buckets a transcript under ~/.claude/projects/<slug>/ where the
// slug is derived from the REALPATH of the session's cwd. Both slugs are
// therefore computed from symlink-resolved paths:
//
//   - source: the legacy worktree the orchestrator last ran in (exists on disk).
//   - destination: the orchestrator worktree the rewrite materialises on first
//     resume — {dataDir}/worktrees/{projectID}/orchestrator/{prefix}-orchestrator.
//     The daemon resolves that path through physicalAbs before cd-ing into it
//     (gitworktree New + validateManagedPath), so we resolve it the same way; a
//     literal-path slug would miss the resume bucket whenever any component of
//     dataDir (e.g. a custom AO_DATA_DIR, or macOS /tmp → /private/tmp) is a
//     symlink. The leaf does not exist yet, so resolvePhysical resolves the
//     longest existing ancestor and appends the literal tail — exactly what the
//     daemon's physicalAbs does.
func planTranscriptCopy(dataDir, projectID, prefix, worktree, uuid, claudeProjectsDir string) transcriptCopyPlan {
	if claudeProjectsDir == "" {
		claudeProjectsDir = defaultClaudeProjectsDir()
	}
	sourceSlug := claudeSlug(resolvePhysical(worktree))

	destTemplate := filepath.Join(dataDir, "worktrees", projectID, "orchestrator", prefix+"-orchestrator")
	destSlug := claudeSlug(resolvePhysical(destTemplate))

	return transcriptCopyPlan{
		uuid:       uuid,
		sourcePath: filepath.Join(claudeProjectsDir, sourceSlug, uuid+".jsonl"),
		destPath:   filepath.Join(claudeProjectsDir, destSlug, uuid+".jsonl"),
	}
}

// resolvePhysical resolves path to an absolute, symlink-free path, mirroring the
// daemon's gitworktree.physicalAbs so the transcript destination slug matches the
// cwd the resumed orchestrator actually runs in. When the leaf does not exist
// yet it resolves the longest existing ancestor and appends the literal tail.
func resolvePhysical(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		return filepath.Clean(path)
	}
	abs = filepath.Clean(abs)
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		return filepath.Clean(resolved)
	}
	parent := filepath.Dir(abs)
	base := filepath.Base(abs)
	for parent != "." && parent != string(os.PathSeparator) {
		if resolved, err := filepath.EvalSymlinks(parent); err == nil {
			return filepath.Join(resolved, base)
		}
		base = filepath.Join(filepath.Base(parent), base)
		parent = filepath.Dir(parent)
	}
	if resolved, err := filepath.EvalSymlinks(parent); err == nil {
		return filepath.Join(resolved, base)
	}
	return abs
}

// transcriptOutcome reports what relocateTranscript did.
type transcriptOutcome string

const (
	transcriptCopied         transcriptOutcome = "copied"
	transcriptAlreadyPresent transcriptOutcome = "already-present"
	transcriptSourceMissing  transcriptOutcome = "source-missing"
)

// relocateTranscript executes a transcript copy. Idempotent: an existing
// destination is left as-is (already-present); a missing source is skipped
// (source-missing). Only "copied" counts as a relocation. The legacy source is
// never modified.
func relocateTranscript(plan transcriptCopyPlan) (transcriptOutcome, error) {
	if pathExists(plan.destPath) {
		return transcriptAlreadyPresent, nil
	}
	if !pathExists(plan.sourcePath) {
		return transcriptSourceMissing, nil
	}
	if err := os.MkdirAll(filepath.Dir(plan.destPath), 0o750); err != nil {
		return "", err
	}
	if err := copyFile(plan.sourcePath, plan.destPath); err != nil {
		return "", err
	}
	return transcriptCopied, nil
}

func pathExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src) //nolint:gosec // src is a resolved transcript path under ~/.claude
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}
