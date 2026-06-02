package github

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	neturl "net/url"
)

// FindPRForBranch resolves the open pull request whose head is the given
// branch in owner/repo, returning its github.com URL. found=false (with a nil
// error) means the branch has no open PR yet — the normal pre-PR state of a
// fresh session, not a failure. Network/auth/rate-limit errors are returned as
// errors so the poller can back off rather than treat them as "no PR".
//
// The head filter is owner:branch, which matches same-repo branches (the AO
// worktree model). Cross-fork PRs from a different head owner are out of scope
// for v1.
func (p *Provider) FindPRForBranch(ctx context.Context, owner, repo, branch string) (url string, found bool, err error) {
	if owner == "" || repo == "" || branch == "" {
		return "", false, fmt.Errorf("github scm: owner, repo, and branch are required")
	}
	q := neturl.Values{}
	q.Set("head", owner+":"+branch)
	q.Set("state", "open")
	q.Set("per_page", "1")

	resp, err := p.client.doREST(ctx, http.MethodGet, repoPath(owner, repo, "pulls"), q, nil)
	if err != nil {
		return "", false, err
	}
	var pulls []struct {
		HTMLURL string `json:"html_url"`
		URL     string `json:"url"`
	}
	if len(resp.Body) > 0 {
		if err := json.Unmarshal(resp.Body, &pulls); err != nil {
			return "", false, fmt.Errorf("github scm: decode pulls list: %w", err)
		}
	}
	if len(pulls) == 0 {
		return "", false, nil
	}
	if pulls[0].HTMLURL != "" {
		return pulls[0].HTMLURL, true, nil
	}
	return pulls[0].URL, true, nil
}
