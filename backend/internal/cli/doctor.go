package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/runtime/zellij"
	"github.com/aoagents/agent-orchestrator/backend/internal/config"
)

type doctorLevel string

const (
	doctorPass doctorLevel = "PASS"
	doctorWarn doctorLevel = "WARN"
	doctorFail doctorLevel = "FAIL"
)

type doctorCheck struct {
	Level   doctorLevel `json:"level"`
	Section string      `json:"section,omitempty"`
	Name    string      `json:"name"`
	Message string      `json:"message"`
}

type doctorReport struct {
	OK       bool          `json:"ok"`
	Failures int           `json:"failures"`
	Checks   []doctorCheck `json:"checks"`
}

const (
	doctorSectionCore           = "Core"
	doctorSectionTools          = "Tools"
	doctorSectionAgents         = "Agent harnesses"
	doctorSectionGitHub         = "GitHub"
	minGitVersion               = "2.25.0"
	githubDoctorUserAgent       = "ao-agent-orchestrator/doctor"
	defaultDoctorGitHubRESTBase = "https://api.github.com"
)

type harnessProbe struct {
	Name       string
	BinaryName string
	VersionArg string
}

var doctorHarnesses = []harnessProbe{
	{Name: "claude-code", BinaryName: "claude", VersionArg: "--version"},
	{Name: "codex", BinaryName: "codex", VersionArg: "--version"},
}

func newDoctorCommand(ctx *commandContext) *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Run local AO health checks",
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			checks := ctx.runDoctor(cmd.Context())
			failures := 0
			for _, check := range checks {
				if check.Level == doctorFail {
					failures++
				}
			}

			if asJSON {
				if err := writeJSON(cmd.OutOrStdout(), doctorReport{
					OK: failures == 0, Failures: failures, Checks: checks,
				}); err != nil {
					return err
				}
			} else {
				if err := writeDoctorText(cmd, checks); err != nil {
					return err
				}
			}

			if failures > 0 {
				return fmt.Errorf("doctor found %d failing check(s)", failures)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "Output health checks as JSON")
	return cmd
}

func writeDoctorText(cmd *cobra.Command, checks []doctorCheck) error {
	var lastSection string
	for _, check := range checks {
		if check.Section != "" && check.Section != lastSection {
			if lastSection != "" {
				if _, err := fmt.Fprintln(cmd.OutOrStdout()); err != nil {
					return err
				}
			}
			if _, err := fmt.Fprintf(cmd.OutOrStdout(), "%s:\n", check.Section); err != nil {
				return err
			}
			lastSection = check.Section
		}
		if _, err := fmt.Fprintf(cmd.OutOrStdout(), "%s %s: %s\n", check.Level, check.Name, check.Message); err != nil {
			return err
		}
	}
	return nil
}

func (c *commandContext) runDoctor(ctx context.Context) []doctorCheck {
	checks := []doctorCheck{}

	cfg, err := config.Load()
	if err != nil {
		return append(checks, doctorCheck{Level: doctorFail, Section: doctorSectionCore, Name: "config", Message: err.Error()})
	}
	checks = append(checks, doctorCheck{
		Level: doctorPass, Section: doctorSectionCore, Name: "config",
		Message: fmt.Sprintf("runFile=%s dataDir=%s port=%d", cfg.RunFilePath, cfg.DataDir, cfg.Port),
	})

	if err := os.MkdirAll(cfg.DataDir, 0o750); err != nil {
		checks = append(checks, doctorCheck{Level: doctorFail, Section: doctorSectionCore, Name: "data-dir", Message: err.Error()})
	} else {
		checks = append(checks,
			doctorCheck{Level: doctorPass, Section: doctorSectionCore, Name: "data-dir", Message: cfg.DataDir},
			checkDataDirWritable(cfg.DataDir),
		)
	}

	checks = append(checks, checkStore(cfg.DataDir))

	st, err := c.inspectDaemon(ctx)
	if err != nil {
		checks = append(checks, doctorCheck{Level: doctorFail, Section: doctorSectionCore, Name: "daemon", Message: err.Error()})
	} else {
		level := doctorPass
		switch st.State {
		case stateStale, stateNotReady:
			level = doctorWarn
		case stateUnhealthy:
			level = doctorFail
		}
		msg := string(st.State)
		if st.PID != 0 {
			msg = fmt.Sprintf("%s pid=%d port=%d", msg, st.PID, st.Port)
		}
		if st.Error != "" {
			msg += " (" + st.Error + ")"
		}
		checks = append(checks, doctorCheck{Level: level, Section: doctorSectionCore, Name: "daemon", Message: msg})
	}

	checks = append(checks,
		c.checkGit(ctx),
		c.checkZellij(ctx),
	)
	for _, harness := range doctorHarnesses {
		checks = append(checks, c.checkHarness(ctx, harness))
	}
	checks = append(checks, c.checkGitHubToken(ctx))
	return checks
}

// checkStore inspects the SQLite store WITHOUT opening or migrating it. The
// daemon is the sole writer and migrator of the database (architecture.md §7);
// the CLI must never run migrations or open a second writer against a database
// a live daemon may already own. Migrations are validated by the daemon at
// startup and surfaced through /readyz, so doctor only confirms whether the
// database file exists yet.
func checkStore(dataDir string) doctorCheck {
	dbPath := filepath.Join(dataDir, "ao.db")
	info, err := os.Stat(dbPath)
	switch {
	case err == nil:
		return doctorCheck{
			Level: doctorPass, Section: doctorSectionCore, Name: "sqlite",
			Message: fmt.Sprintf("%s (%d bytes); migrations are applied by the daemon at startup", dbPath, info.Size()),
		}
	case errors.Is(err, fs.ErrNotExist):
		return doctorCheck{
			Level: doctorWarn, Section: doctorSectionCore, Name: "sqlite",
			Message: "database not created yet; run `ao start` to initialize and migrate it",
		}
	default:
		return doctorCheck{Level: doctorFail, Section: doctorSectionCore, Name: "sqlite", Message: err.Error()}
	}
}

func checkDataDirWritable(dataDir string) doctorCheck {
	f, err := os.CreateTemp(dataDir, ".ao-doctor-write-*")
	if err != nil {
		return doctorCheck{Level: doctorFail, Section: doctorSectionCore, Name: "data-dir-write", Message: err.Error()}
	}
	name := f.Name()
	if _, err := f.WriteString("ok\n"); err != nil {
		_ = f.Close()
		_ = os.Remove(name)
		return doctorCheck{Level: doctorFail, Section: doctorSectionCore, Name: "data-dir-write", Message: err.Error()}
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(name)
		return doctorCheck{Level: doctorFail, Section: doctorSectionCore, Name: "data-dir-write", Message: err.Error()}
	}
	if err := os.Remove(name); err != nil {
		return doctorCheck{Level: doctorWarn, Section: doctorSectionCore, Name: "data-dir-write", Message: fmt.Sprintf("write probe succeeded but cleanup failed: %v", err)}
	}
	return doctorCheck{Level: doctorPass, Section: doctorSectionCore, Name: "data-dir-write", Message: "write probe succeeded"}
}

func (c *commandContext) checkGit(ctx context.Context) doctorCheck {
	path, err := c.deps.LookPath("git")
	if err != nil || path == "" {
		return doctorCheck{Level: doctorFail, Section: doctorSectionTools, Name: "git", Message: "not found in PATH"}
	}
	reqCtx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	out, err := c.deps.CommandOutput(reqCtx, path, "--version")
	if err != nil {
		return doctorCheck{Level: doctorFail, Section: doctorSectionTools, Name: "git", Message: fmt.Sprintf("%s: %v", path, err)}
	}
	version, err := parseGitVersion(string(out))
	if err != nil {
		return doctorCheck{Level: doctorWarn, Section: doctorSectionTools, Name: "git", Message: fmt.Sprintf("%s (version unknown: %s)", path, firstOutputLine(out))}
	}
	cmp, err := compareDottedVersion(version, minGitVersion)
	if err != nil {
		return doctorCheck{Level: doctorWarn, Section: doctorSectionTools, Name: "git", Message: fmt.Sprintf("%s (version unknown: %s)", path, firstOutputLine(out))}
	}
	if cmp < 0 {
		return doctorCheck{Level: doctorWarn, Section: doctorSectionTools, Name: "git", Message: fmt.Sprintf("%s (version %s; AO expects >= %s for worktrees)", path, version, minGitVersion)}
	}
	return doctorCheck{Level: doctorPass, Section: doctorSectionTools, Name: "git", Message: fmt.Sprintf("%s (version %s; supports worktrees)", path, version)}
}

func (c *commandContext) checkZellij(ctx context.Context) doctorCheck {
	path, err := c.deps.LookPath("zellij")
	if err != nil || path == "" {
		return doctorCheck{Level: doctorWarn, Section: doctorSectionTools, Name: "zellij", Message: "not found in PATH"}
	}
	reqCtx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	out, err := c.deps.CommandOutput(reqCtx, path, "--version")
	if err != nil {
		return doctorCheck{Level: doctorFail, Section: doctorSectionTools, Name: "zellij", Message: fmt.Sprintf("%s: %v", path, err)}
	}
	version, err := zellij.CheckVersionOutput(string(out))
	if err != nil {
		return doctorCheck{Level: doctorFail, Section: doctorSectionTools, Name: "zellij", Message: fmt.Sprintf("%s: %v", path, err)}
	}
	return doctorCheck{Level: doctorPass, Section: doctorSectionTools, Name: "zellij", Message: fmt.Sprintf("%s (version %s; require >= %s)", path, version, zellij.RequiredVersion())}
}

func (c *commandContext) checkHarness(ctx context.Context, harness harnessProbe) doctorCheck {
	path, err := c.deps.LookPath(harness.BinaryName)
	if err != nil || path == "" {
		return doctorCheck{
			Level: doctorWarn, Section: doctorSectionAgents, Name: harness.Name,
			Message: fmt.Sprintf("%s not found in PATH", harness.BinaryName),
		}
	}
	if harness.VersionArg == "" {
		return doctorCheck{Level: doctorPass, Section: doctorSectionAgents, Name: harness.Name, Message: fmt.Sprintf("%s resolves to %s", harness.BinaryName, path)}
	}
	reqCtx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	out, err := c.deps.CommandOutput(reqCtx, path, harness.VersionArg)
	if err != nil {
		return doctorCheck{
			Level: doctorWarn, Section: doctorSectionAgents, Name: harness.Name,
			Message: fmt.Sprintf("%s resolves to %s, but `%s %s` failed: %v", harness.BinaryName, path, harness.BinaryName, harness.VersionArg, err),
		}
	}
	version := firstOutputLine(out)
	if version == "" {
		version = "version output was empty"
	}
	return doctorCheck{Level: doctorPass, Section: doctorSectionAgents, Name: harness.Name, Message: fmt.Sprintf("%s resolves to %s (%s)", harness.BinaryName, path, version)}
}

func (c *commandContext) checkGitHubToken(ctx context.Context) doctorCheck {
	token, source, err := c.githubToken(ctx)
	if err != nil {
		return doctorCheck{Level: doctorWarn, Section: doctorSectionGitHub, Name: "github-token", Message: err.Error()}
	}

	reqCtx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, strings.TrimRight(c.deps.DoctorGitHubRESTBase, "/")+"/user", http.NoBody)
	if err != nil {
		return doctorCheck{Level: doctorFail, Section: doctorSectionGitHub, Name: "github-token", Message: err.Error()}
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", githubDoctorUserAgent)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := c.deps.HTTPClient.Do(req)
	if err != nil {
		return doctorCheck{Level: doctorFail, Section: doctorSectionGitHub, Name: "github-token", Message: fmt.Sprintf("%s token validation failed: %v", source, err)}
	}
	defer func() {
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}()

	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return doctorCheck{Level: doctorFail, Section: doctorSectionGitHub, Name: "github-token", Message: fmt.Sprintf("%s token rejected by GitHub (HTTP %d)", source, resp.StatusCode)}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return doctorCheck{Level: doctorWarn, Section: doctorSectionGitHub, Name: "github-token", Message: fmt.Sprintf("%s token probe returned HTTP %d", source, resp.StatusCode)}
	}

	var user struct {
		Login string `json:"login"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&user); err != nil {
		return doctorCheck{Level: doctorFail, Section: doctorSectionGitHub, Name: "github-token", Message: fmt.Sprintf("%s token probe decode failed: %v", source, err)}
	}
	login := user.Login
	if login == "" {
		login = "unknown user"
	}
	scopes := strings.TrimSpace(resp.Header.Get("X-OAuth-Scopes"))
	scopeMsg := "scopes unavailable"
	if scopes != "" {
		scopeMsg = "scopes: " + scopes
	}
	return doctorCheck{Level: doctorPass, Section: doctorSectionGitHub, Name: "github-token", Message: fmt.Sprintf("%s token valid for %s (%s)", source, login, scopeMsg)}
}

func (c *commandContext) githubToken(ctx context.Context) (token, source string, err error) {
	for _, name := range []string{"AO_GITHUB_TOKEN", "GITHUB_TOKEN"} {
		if v := strings.TrimSpace(os.Getenv(name)); v != "" {
			return v, name, nil
		}
	}
	path, lookErr := c.deps.LookPath("gh")
	if lookErr != nil || path == "" {
		return "", "", errors.New("no GitHub token found (set AO_GITHUB_TOKEN/GITHUB_TOKEN or run `gh auth login`)")
	}
	reqCtx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	out, cmdErr := c.deps.CommandOutput(reqCtx, path, "auth", "token")
	if cmdErr != nil {
		return "", "", fmt.Errorf("gh is installed but no token was available (`gh auth token` failed: %w)", cmdErr)
	}
	token = strings.TrimSpace(string(out))
	if token == "" {
		return "", "", errors.New("gh is installed but returned an empty auth token")
	}
	return token, "gh", nil
}

var (
	ansiRE       = regexp.MustCompile(`\x1b\[[0-9;]*[A-Za-z]`)
	gitVersionRE = regexp.MustCompile(`(?i)\bgit version\s+(\d+(?:\.\d+){1,3})`)
)

func parseGitVersion(out string) (string, error) {
	clean := ansiRE.ReplaceAllString(out, "")
	m := gitVersionRE.FindStringSubmatch(clean)
	if len(m) < 2 {
		return "", fmt.Errorf("parse git version from %q", strings.TrimSpace(clean))
	}
	return m[1], nil
}

func firstOutputLine(out []byte) string {
	clean := strings.TrimSpace(ansiRE.ReplaceAllString(string(out), ""))
	if clean == "" {
		return ""
	}
	line := strings.SplitN(clean, "\n", 2)[0]
	return strings.TrimSpace(line)
}

func compareDottedVersion(a, b string) (int, error) {
	ap, err := dottedVersionParts(a)
	if err != nil {
		return 0, err
	}
	bp, err := dottedVersionParts(b)
	if err != nil {
		return 0, err
	}
	maxLen := len(ap)
	if len(bp) > maxLen {
		maxLen = len(bp)
	}
	for i := 0; i < maxLen; i++ {
		var av, bv int
		if i < len(ap) {
			av = ap[i]
		}
		if i < len(bp) {
			bv = bp[i]
		}
		switch {
		case av < bv:
			return -1, nil
		case av > bv:
			return 1, nil
		}
	}
	return 0, nil
}

func dottedVersionParts(s string) ([]int, error) {
	raw := strings.Split(s, ".")
	parts := make([]int, 0, len(raw))
	for _, part := range raw {
		if part == "" {
			return nil, fmt.Errorf("empty version segment in %q", s)
		}
		n, err := strconv.Atoi(part)
		if err != nil {
			return nil, fmt.Errorf("parse version segment %q in %q: %w", part, s, err)
		}
		parts = append(parts, n)
	}
	return parts, nil
}
