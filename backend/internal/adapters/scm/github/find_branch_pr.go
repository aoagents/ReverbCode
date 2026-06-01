package github

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// FindOpenPRForBranch returns the canonical github.com URL of the most
// recently updated open PR whose head ref is "{owner}:{branch}", or ""
// with a nil error when no open PR matches.
//
// The poller uses this for branch-based discovery: since the session
// record does not (yet) carry a stored PR URL, the only way to find
// "the PR for this session" is by the workspace branch. The endpoint
// hit is GET /repos/{owner}/{repo}/pulls?head={owner}:{branch}&state=open
// per the GitHub REST API.
//
// When multiple open PRs share the same head ref (rare but legal —
// e.g. forks that pushed to the same branch name), we pick the most
// recently updated one rather than failing closed. Failing closed
// would silently stop observing the PR every time a stale duplicate
// shows up.
func (p *Provider) FindOpenPRForBranch(ctx context.Context, owner, repo, branch string) (string, error) {
	owner = strings.TrimSpace(owner)
	repo = strings.TrimSpace(repo)
	branch = strings.TrimSpace(branch)
	if owner == "" || repo == "" || branch == "" {
		return "", fmt.Errorf("github scm: FindOpenPRForBranch requires owner/repo/branch (got %q/%q/%q)", owner, repo, branch)
	}

	q := url.Values{}
	q.Set("state", "open")
	q.Set("head", owner+":"+branch)
	q.Set("per_page", "100")

	resp, err := p.client.doREST(ctx, http.MethodGet, repoPath(owner, repo, "pulls"), q, nil)
	if err != nil {
		return "", err
	}
	if len(resp.Body) == 0 {
		return "", nil
	}
	var list []listedPR
	if err := json.Unmarshal(resp.Body, &list); err != nil {
		return "", fmt.Errorf("github scm: decode pulls list: %w", err)
	}
	if len(list) == 0 {
		return "", nil
	}

	best := -1
	var bestTime time.Time
	for i, pr := range list {
		if !strings.EqualFold(pr.State, "open") {
			continue
		}
		t := parsePRTimestamp(pr.UpdatedAt)
		if best < 0 || t.After(bestTime) {
			best = i
			bestTime = t
		}
	}
	if best < 0 {
		return "", nil
	}
	chosen := list[best]
	if chosen.HTMLURL != "" {
		return chosen.HTMLURL, nil
	}
	// Construct the canonical web URL from owner/repo/number when the
	// API response omits html_url (some enterprise responses elide it).
	return "https://github.com/" + owner + "/" + repo + "/pull/" + strconv.Itoa(chosen.Number), nil
}

type listedPR struct {
	Number    int    `json:"number"`
	State     string `json:"state"`
	HTMLURL   string `json:"html_url"`
	UpdatedAt string `json:"updated_at"`
}

func parsePRTimestamp(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}
	}
	return t
}
