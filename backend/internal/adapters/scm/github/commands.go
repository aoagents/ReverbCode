package github

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

var _ ports.SCMCommandProvider = (*Provider)(nil)

func (p *Provider) Capabilities() ports.SCMCommandCapabilities {
	return ports.SCMCommandCapabilities{Merge: true, Close: true, Comment: true, Assign: true, Checkout: true}
}

func (p *Provider) CacheInvalidationPrefixes(subj domain.SCMSubject, cmd ports.SCMCommand) []domain.SCMProviderCachePrefix {
	scope := subj.CacheScope()
	prefixes := []domain.SCMProviderCachePrefix{
		{SCMProviderCacheScope: scope, Namespace: cachePRList},
		{SCMProviderCacheScope: scope, Namespace: cacheBranchMap},
		{SCMProviderCacheScope: scope, Namespace: cachePRState},
	}
	switch cmd {
	case ports.SCMCommandMerge, ports.SCMCommandClose:
		prefixes = append(prefixes,
			domain.SCMProviderCachePrefix{SCMProviderCacheScope: scope, Namespace: cacheChecks},
			domain.SCMProviderCachePrefix{SCMProviderCacheScope: scope, Namespace: cacheCheckGuard},
			domain.SCMProviderCachePrefix{SCMProviderCacheScope: scope, Namespace: cacheReviews},
			domain.SCMProviderCachePrefix{SCMProviderCacheScope: scope, Namespace: cacheReviewDetails},
		)
	case ports.SCMCommandComment:
		prefixes = append(prefixes,
			domain.SCMProviderCachePrefix{SCMProviderCacheScope: scope, Namespace: cacheReviews},
			domain.SCMProviderCachePrefix{SCMProviderCacheScope: scope, Namespace: cacheReviewDetails},
		)
	default:
		return nil
	}
	return prefixes
}

func (p *Provider) Merge(ctx context.Context, req ports.SCMCommandRequest) (ports.SCMCommandResult, error) {
	req = p.normalizeCommandRequest(ctx, req, ports.SCMCommandMerge)
	owner, repo := req.Subject.Repository().OwnerName()
	body := map[string]any{}
	if req.MergeMethod != "" {
		body["merge_method"] = req.MergeMethod
	}
	if req.CommitTitle != "" {
		body["commit_title"] = req.CommitTitle
	}
	if req.CommitMessage != "" {
		body["commit_message"] = req.CommitMessage
	}
	resp, err := p.client.DoREST(ctx, http.MethodPut, repoPath(owner, repo, "pulls", strconv.Itoa(req.ChangeRequest.Number), "merge"), nil, body, "", "github.command.merge")
	res := commandResult(req, resp.Diagnostic)
	if err != nil {
		return res, err
	}
	var decoded struct {
		SHA     string `json:"sha"`
		Message string `json:"message"`
	}
	_ = jsonUnmarshal(resp.Body, &decoded)
	res.SHA = decoded.SHA
	res.Message = firstNonEmpty(decoded.Message, "merged")
	return res, nil
}

func (p *Provider) Close(ctx context.Context, req ports.SCMCommandRequest) (ports.SCMCommandResult, error) {
	req = p.normalizeCommandRequest(ctx, req, ports.SCMCommandClose)
	owner, repo := req.Subject.Repository().OwnerName()
	resp, err := p.client.DoREST(ctx, http.MethodPatch, repoPath(owner, repo, "pulls", strconv.Itoa(req.ChangeRequest.Number)), nil, map[string]any{"state": "closed"}, "", "github.command.close")
	res := commandResult(req, resp.Diagnostic)
	if err != nil {
		return res, err
	}
	res.Message = "closed"
	return res, nil
}

func (p *Provider) Comment(ctx context.Context, req ports.SCMCommandRequest) (ports.SCMCommandResult, error) {
	req = p.normalizeCommandRequest(ctx, req, ports.SCMCommandComment)
	if strings.TrimSpace(req.Body) == "" && strings.TrimSpace(req.Message) == "" {
		return commandResult(req, domain.SCMDiagnostic{}), &domain.SCMError{Kind: domain.SCMErrorUnsupported, Operation: "github.command.comment", Message: "comment body is required"}
	}
	owner, repo := req.Subject.Repository().OwnerName()
	body := firstNonEmpty(req.Body, req.Message)
	resp, err := p.client.DoREST(ctx, http.MethodPost, repoPath(owner, repo, "issues", strconv.Itoa(req.ChangeRequest.Number), "comments"), nil, map[string]any{"body": body}, "", "github.command.comment")
	res := commandResult(req, resp.Diagnostic)
	if err != nil {
		return res, err
	}
	var decoded struct {
		HTMLURL string `json:"html_url"`
	}
	_ = jsonUnmarshal(resp.Body, &decoded)
	res.URL = decoded.HTMLURL
	res.Message = "commented"
	return res, nil
}

func (p *Provider) Assign(ctx context.Context, req ports.SCMCommandRequest) (ports.SCMCommandResult, error) {
	req = p.normalizeCommandRequest(ctx, req, ports.SCMCommandAssign)
	if len(req.Assignees) == 0 {
		return commandResult(req, domain.SCMDiagnostic{}), &domain.SCMError{Kind: domain.SCMErrorUnsupported, Operation: "github.command.assign", Message: "at least one assignee is required"}
	}
	owner, repo := req.Subject.Repository().OwnerName()
	resp, err := p.client.DoREST(ctx, http.MethodPost, repoPath(owner, repo, "issues", strconv.Itoa(req.ChangeRequest.Number), "assignees"), nil, map[string]any{"assignees": req.Assignees}, "", "github.command.assign")
	res := commandResult(req, resp.Diagnostic)
	if err != nil {
		return res, err
	}
	res.Message = "assigned"
	return res, nil
}

func (p *Provider) Checkout(ctx context.Context, req ports.SCMCommandRequest) (ports.SCMCommandResult, error) {
	req = p.normalizeCommandRequest(ctx, req, ports.SCMCommandCheckout)
	res := commandResult(req, domain.SCMDiagnostic{Operation: "github.command.checkout"})
	if req.WorkspacePath == "" {
		res.Message = fmt.Sprintf("git fetch origin pull/%d/head:pr-%d && git checkout pr-%d", req.ChangeRequest.Number, req.ChangeRequest.Number, req.ChangeRequest.Number)
		return res, nil
	}
	branch := fmt.Sprintf("pr-%d", req.ChangeRequest.Number)
	if err := checkoutWithGit(ctx, req.WorkspacePath, req.ChangeRequest.Number, branch); err == nil {
		res.Message = "checked out " + branch
		return res, nil
	} else if ghErr := checkoutWithGH(ctx, req.WorkspacePath, req.ChangeRequest); ghErr == nil {
		res.Message = "checked out with gh pr checkout"
		return res, nil
	} else {
		return res, &domain.SCMError{Kind: domain.SCMErrorCommand, Operation: "github.command.checkout", Message: fmt.Sprintf("git checkout failed: %v; gh fallback failed: %v", err, ghErr), Cause: err}
	}
}

func checkoutWithGit(ctx context.Context, workspacePath string, number int, branch string) error {
	cmd := exec.CommandContext(ctx, "git", "fetch", "origin", fmt.Sprintf("pull/%d/head:%s", number, branch))
	cmd.Dir = workspacePath
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git fetch: %s: %w", strings.TrimSpace(string(out)), err)
	}
	cmd = exec.CommandContext(ctx, "git", "checkout", branch)
	cmd.Dir = workspacePath
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git checkout: %s: %w", strings.TrimSpace(string(out)), err)
	}
	return nil
}

func checkoutWithGH(ctx context.Context, workspacePath string, cr domain.SCMChangeRequestID) error {
	cmd := exec.CommandContext(ctx, "gh", "pr", "checkout", strconv.Itoa(cr.Number), "--repo", cr.Repo)
	cmd.Dir = workspacePath
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("gh pr checkout: %s: %w", strings.TrimSpace(string(out)), err)
	}
	return nil
}

func (p *Provider) normalizeCommandRequest(ctx context.Context, req ports.SCMCommandRequest, command ports.SCMCommand) ports.SCMCommandRequest {
	req.Command = command
	req.Subject = p.normalizeSubject(ctx, req.Subject)
	if req.ChangeRequest.Number == 0 {
		req.ChangeRequest = req.Subject.ChangeRequestID()
	}
	if req.ChangeRequest.Provider == "" {
		req.ChangeRequest.Provider = domain.SCMProviderGitHub
	}
	if req.ChangeRequest.Host == "" {
		req.ChangeRequest.Host = req.Subject.Host
	}
	if req.ChangeRequest.Repo == "" {
		req.ChangeRequest.Repo = req.Subject.Repo
	}
	if req.Now.IsZero() {
		req.Now = time.Now()
	}
	return req
}

func commandResult(req ports.SCMCommandRequest, diag domain.SCMDiagnostic) ports.SCMCommandResult {
	res := ports.SCMCommandResult{Provider: domain.SCMProviderGitHub, Command: req.Command, ChangeRequest: req.ChangeRequest, PerformedAt: req.Now}
	if !diag.StartedAt.IsZero() || diag.Operation != "" {
		res.Diagnostics = []domain.SCMDiagnostic{diag}
	}
	return res
}

func jsonUnmarshal(b []byte, v any) error {
	if len(b) == 0 {
		return nil
	}
	return json.Unmarshal(b, v)
}
