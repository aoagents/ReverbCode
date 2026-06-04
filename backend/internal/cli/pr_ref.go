package cli

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

func (c *commandContext) resolvePRRef(ctx context.Context, ref string, project projectDetails) (string, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return "", usageError{errors.New("PR reference must be a github.com PR URL or a number")}
	}
	if isNumericPRRef(ref) {
		repo := strings.TrimSpace(project.Repo)
		if repo == "" {
			// The daemon must not shell out to external CLIs from its loopback API;
			// when the durable project record lacks repo_origin_url, the thin CLI
			// does the one-off gh lookup from the registered project checkout and
			// sends the daemon a normalized URL.
			out, err := c.deps.CommandOutputInDir(ctx, project.Path, "gh", "repo", "view", "--json", "url", "-q", ".url")
			if err != nil || strings.TrimSpace(string(out)) == "" {
				return "", usageError{errors.New("gh not available; pass the full PR URL")}
			}
			repo = strings.TrimSpace(string(out))
		}
		owner, name, err := cliGitHubRepoFromURL(repo)
		if err != nil {
			return "", usageError{errors.New("PR reference must be a github.com PR URL or a number")}
		}
		n, _ := strconv.Atoi(strings.TrimPrefix(ref, "#"))
		return fmt.Sprintf("https://github.com/%s/%s/pull/%d", owner, name, n), nil
	}
	owner, name, n, err := cliParseGitHubPRURL(ref)
	if err != nil || owner == "" || name == "" || n <= 0 {
		return "", usageError{errors.New("PR reference must be a github.com PR URL or a number")}
	}
	return fmt.Sprintf("https://github.com/%s/%s/pull/%d", owner, name, n), nil
}

func isNumericPRRef(ref string) bool {
	ref = strings.TrimPrefix(strings.TrimSpace(ref), "#")
	n, err := strconv.Atoi(ref)
	return err == nil && n > 0
}

func cliParseGitHubPRURL(raw string) (string, string, int, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", "", 0, err
	}
	if !strings.EqualFold(u.Scheme, "https") || !strings.EqualFold(u.Hostname(), "github.com") {
		return "", "", 0, errors.New("not github")
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) != 4 || parts[2] != "pull" {
		return "", "", 0, errors.New("not pr")
	}
	n, err := strconv.Atoi(parts[3])
	if err != nil || n <= 0 {
		return "", "", 0, errors.New("bad number")
	}
	return parts[0], strings.TrimSuffix(parts[1], ".git"), n, nil
}

func cliGitHubRepoFromURL(raw string) (string, string, error) {
	raw = strings.TrimSpace(raw)
	if strings.HasPrefix(raw, "git@github.com:") {
		parts := strings.Split(strings.TrimSuffix(strings.TrimPrefix(raw, "git@github.com:"), ".git"), "/")
		if len(parts) == 2 && parts[0] != "" && parts[1] != "" {
			return parts[0], parts[1], nil
		}
		return "", "", errors.New("bad repo")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", "", err
	}
	if !strings.EqualFold(u.Hostname(), "github.com") {
		return "", "", errors.New("not github")
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
		return "", "", errors.New("bad repo")
	}
	return parts[0], strings.TrimSuffix(parts[1], ".git"), nil
}
