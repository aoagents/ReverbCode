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
// The source slug realpath-resolves the legacy worktree (it exists on disk).
// The destination slug uses the LITERAL orchestrator-worktree path the rewrite
// will materialise on first resume —
// {dataDir}/worktrees/{projectID}/orchestrator/{prefix}-orchestrator — with NO
// realpath, because that directory does not exist yet and ~/.ao/data is not a
// symlink, so the literal-path slug matches what Claude will compute from the
// resumed orchestrator's cwd (gitworktree managedPath, kind orchestrator).
func planTranscriptCopy(dataDir, projectID, prefix, worktree, uuid, claudeProjectsDir string) transcriptCopyPlan {
	if claudeProjectsDir == "" {
		claudeProjectsDir = defaultClaudeProjectsDir()
	}
	source := worktree
	if resolved, err := filepath.EvalSymlinks(worktree); err == nil {
		source = resolved
	}
	sourceSlug := claudeSlug(source)

	destTemplate := filepath.Join(dataDir, "worktrees", projectID, "orchestrator", prefix+"-orchestrator")
	destSlug := claudeSlug(destTemplate)

	return transcriptCopyPlan{
		uuid:       uuid,
		sourcePath: filepath.Join(claudeProjectsDir, sourceSlug, uuid+".jsonl"),
		destPath:   filepath.Join(claudeProjectsDir, destSlug, uuid+".jsonl"),
	}
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
	if _, err := os.Stat(plan.destPath); err == nil {
		return transcriptAlreadyPresent, nil
	}
	if _, err := os.Stat(plan.sourcePath); err != nil {
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
