package github

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

const (
	cachePRList        = "pr-list"
	cacheBranchMap     = "branch-map"
	cacheChecks        = "checks"
	cacheReviews       = "reviews"
	cacheReviewDetails = "review-details"
	cacheCheckGuard    = "checks-guard"
	cachePRState       = "pr-state"

	maxGraphQLBatchSize      = 25
	graphQLCheckContextLimit = 20

	cacheCapPRList        = 100
	cacheCapChecks        = 500
	cacheCapReviews       = 500
	cacheCapReviewDetails = 500
	cacheCapBranchMap     = 1000
	cacheCapCheckGuard    = 500
	cacheCapPRState       = 500

	ciFailureLogTailLines = 20
)

type Provider struct {
	client *Client
	host   string
}

type restCacheCommit func() error

type ProviderOptions struct {
	Client     *Client
	HTTPClient *http.Client
	Token      TokenSource
	RESTBase   string
	GraphQLURL string
	Host       string
}

func NewProvider(opts ProviderOptions) *Provider {
	p := &Provider{client: opts.Client, host: opts.Host}
	if p.client == nil {
		p.client = NewClient(ClientOptions{HTTPClient: opts.HTTPClient, Token: opts.Token, RESTBase: opts.RESTBase, GraphQLURL: opts.GraphQLURL})
	}
	if p.host == "" {
		p.host = defaultHost
	}
	return p
}

func (p *Provider) Provider() domain.SCMProvider { return domain.SCMProviderGitHub }

func (p *Provider) ObserveSessions(ctx context.Context, req ports.SCMObserveRequest, cache ports.SCMProviderCache) (ports.SCMObserveResult, error) {
	now := req.Now
	if now.IsZero() {
		now = time.Now()
	}
	res := ports.SCMObserveResult{ProviderName: domain.SCMProviderGitHub}
	groups := map[string][]domain.SCMSubject{}
	for _, subj := range req.Subjects {
		subj = p.normalizeSubject(ctx, subj)
		groups[subj.Repository().Key()] = append(groups[subj.Repository().Key()], subj)
	}
	var firstErr error
	for _, subjects := range groups {
		updated, discoverErr := p.discover(ctx, subjects, cache, now)
		groupErr := discoverErr
		if discoverErr != nil && firstErr == nil {
			firstErr = discoverErr
		}
		res.Subjects = append(res.Subjects, updated...)
		snaps, diags, rl, observeErr := p.observeKnownPRs(ctx, updated, cache, now)
		res.Snapshots = append(res.Snapshots, snaps...)
		res.Diagnostics = append(res.Diagnostics, diags...)
		if rl != nil {
			res.RateLimit = rl
		}
		if observeErr != nil {
			if groupErr == nil {
				groupErr = observeErr
			}
			if firstErr == nil {
				firstErr = observeErr
			}
		}
		for _, subj := range updated {
			key := domain.SCMPollStateKey{Provider: subj.Provider, Host: subj.Host, Repo: subj.Repo}
			st := domain.SCMPollState{Key: key}
			if reader, ok := cache.(pollStateReader); ok {
				if prev, ok, readErr := reader.GetPollState(ctx, key); readErr == nil && ok {
					st = prev
				}
			}
			st.Key = key
			if groupErr != nil {
				st.ConsecutiveFail++
				st.LastFailureAt = now
				st.BackoffUntil = now.Add(30 * time.Second)
				if se, ok := groupErr.(*domain.SCMError); ok {
					st.LastError = se
					if se.Kind == domain.SCMErrorRateLimited && !se.RetryAfter.IsZero() {
						st.RateLimitUntil = se.RetryAfter
					}
				}
			} else {
				st.ConsecutiveFail = 0
				st.LastSuccessAt = now
				st.LastError = nil
				st.BackoffUntil = time.Time{}
				st.RateLimitUntil = time.Time{}
			}
			res.PollStates = append(res.PollStates, st)
		}
	}
	return res, firstErr
}

func (p *Provider) normalizeSubject(ctx context.Context, subj domain.SCMSubject) domain.SCMSubject {
	if subj.Provider == "" {
		subj.Provider = domain.SCMProviderGitHub
	}
	if subj.Host == "" {
		subj.Host = p.host
	}
	if subj.CredentialHash == "" {
		subj.CredentialHash = p.client.CredentialHash(ctx)
	}
	return subj
}

func (p *Provider) discover(ctx context.Context, subjects []domain.SCMSubject, cache ports.SCMProviderCache, now time.Time) ([]domain.SCMSubject, error) {
	if len(subjects) == 0 {
		return nil, nil
	}
	out := append([]domain.SCMSubject(nil), subjects...)
	need := false
	for _, subj := range out {
		if subj.PRNumber == 0 && subj.Branch != "" {
			need = true
			break
		}
	}
	if !need {
		return out, nil
	}
	scope := out[0].CacheScope()
	for i := range out {
		if out[i].PRNumber != 0 || out[i].Branch == "" {
			continue
		}
		if mapped, ok, _ := getCachedBranch(ctx, cache, scope, out[i].Branch); ok {
			out[i].PRNumber = mapped.Number
			out[i].PRURL = mapped.URL
			out[i].BaseBranch = firstNonEmpty(out[i].BaseBranch, mapped.BaseBranch)
		}
	}
	stillNeed := false
	for _, subj := range out {
		if subj.PRNumber == 0 && subj.Branch != "" {
			stillNeed = true
			break
		}
	}
	if !stillNeed {
		return out, nil
	}

	owner, repo := out[0].Repository().OwnerName()
	pulls, changed, _, err := p.fetchOpenPullsForDiscovery(ctx, cache, scope, owner, repo, now)
	if err != nil {
		return out, err
	}
	for _, pr := range pulls {
		if pr.Number <= 0 || pr.Head.Ref == "" {
			continue
		}
		m := branchMapping{Number: pr.Number, URL: pr.HTMLURL, Branch: pr.Head.Ref, BaseBranch: pr.Base.Ref}
		putCachedBranch(ctx, cache, scope, pr.Head.Ref, m, now)
	}
	for i := range out {
		if out[i].PRNumber != 0 || out[i].Branch == "" {
			continue
		}
		if mapped, ok := findPullForBranch(pulls, out[i].Branch); ok {
			out[i].PRNumber = mapped.Number
			out[i].PRURL = mapped.URL
			out[i].BaseBranch = firstNonEmpty(out[i].BaseBranch, mapped.BaseBranch)
			continue
		}
		if !changed {
			continue
		}
		if mapped, ok, err := p.fetchPullForBranch(ctx, owner, repo, out[i].Branch); err != nil {
			return out, err
		} else if ok {
			putCachedBranch(ctx, cache, scope, out[i].Branch, mapped, now)
			out[i].PRNumber = mapped.Number
			out[i].PRURL = mapped.URL
			out[i].BaseBranch = firstNonEmpty(out[i].BaseBranch, mapped.BaseBranch)
		}
	}
	return out, nil
}

func (p *Provider) checkOpenPullListChanged(ctx context.Context, cache ports.SCMProviderCache, scope domain.SCMProviderCacheScope, owner, repo string, now time.Time) (bool, domain.SCMDiagnostic, restCacheCommit, error) {
	cacheKey := domain.SCMProviderCacheKey{SCMProviderCacheScope: scope, Namespace: cachePRList, Key: "open-guard"}
	entry, hasEntry, _ := cache.GetProviderCache(ctx, cacheKey)
	q := url.Values{"state": []string{"open"}, "sort": []string{"updated"}, "direction": []string{"desc"}, "per_page": []string{"1"}}
	resp, err := p.client.DoREST(ctx, http.MethodGet, repoPath(owner, repo, "pulls"), q, nil, entry.ETag, "github.pr_list_guard")
	if err != nil {
		return true, resp.Diagnostic, nil, err
	}
	_, commit := prepareRESTCache(ctx, cache, cacheKey, entry, hasEntry, resp, now)
	return !resp.NotModified, resp.Diagnostic, commit, nil
}

func (p *Provider) fetchOpenPullsForDiscovery(ctx context.Context, cache ports.SCMProviderCache, scope domain.SCMProviderCacheScope, owner, repo string, now time.Time) ([]restPull, bool, domain.SCMDiagnostic, error) {
	cacheKey := domain.SCMProviderCacheKey{SCMProviderCacheScope: scope, Namespace: cachePRList, Key: "open-discovery"}
	entry, hasEntry, _ := cache.GetProviderCache(ctx, cacheKey)
	q := url.Values{"state": []string{"open"}, "sort": []string{"updated"}, "direction": []string{"desc"}, "per_page": []string{"100"}}
	resp, err := p.client.DoREST(ctx, http.MethodGet, repoPath(owner, repo, "pulls"), q, nil, entry.ETag, "github.pr_list_discovery")
	if err != nil {
		return nil, true, resp.Diagnostic, err
	}
	changed := !resp.NotModified
	body, commit := prepareRESTCache(ctx, cache, cacheKey, entry, hasEntry, resp, now)
	var pulls []restPull
	if err := json.Unmarshal(body, &pulls); err != nil {
		return nil, changed, resp.Diagnostic, &domain.SCMError{Kind: domain.SCMErrorParse, Operation: "github.pr_list_discovery", Message: err.Error(), Cause: err}
	}
	commitRESTCache(commit)
	return pulls, changed, resp.Diagnostic, nil
}

func (p *Provider) fetchPullForBranch(ctx context.Context, owner, repo, branch string) (branchMapping, bool, error) {
	q := url.Values{"state": []string{"open"}, "head": []string{owner + ":" + branch}, "per_page": []string{"1"}}
	resp, err := p.client.DoREST(ctx, http.MethodGet, repoPath(owner, repo, "pulls"), q, nil, "", "github.pr_branch_exact")
	if err != nil {
		return branchMapping{}, false, err
	}
	var pulls []restPull
	if err := json.Unmarshal(resp.Body, &pulls); err != nil {
		return branchMapping{}, false, &domain.SCMError{Kind: domain.SCMErrorParse, Operation: "github.pr_branch_exact", Message: err.Error(), Cause: err}
	}
	if len(pulls) == 0 || pulls[0].Number <= 0 {
		return branchMapping{}, false, nil
	}
	pr := pulls[0]
	return branchMapping{Number: pr.Number, URL: pr.HTMLURL, Branch: pr.Head.Ref, BaseBranch: pr.Base.Ref}, true, nil
}

func latestSnapshots(ctx context.Context, cache ports.SCMProviderCache, subjects []domain.SCMSubject) map[domain.SessionID]domain.SCMSnapshot {
	reader, ok := cache.(snapshotReader)
	if !ok {
		return nil
	}
	out := map[domain.SessionID]domain.SCMSnapshot{}
	for _, subj := range subjects {
		snap, ok, err := reader.GetLatestSnapshot(ctx, subj.SessionID)
		if err == nil && ok {
			out[subj.SessionID] = snap
		}
	}
	return out
}

func (p *Provider) checkRunsChanged(ctx context.Context, cache ports.SCMProviderCache, subj domain.SCMSubject, headSHA string, now time.Time) (bool, domain.SCMDiagnostic, restCacheCommit, error) {
	if headSHA == "" {
		return true, domain.SCMDiagnostic{}, nil, nil
	}
	scope := subj.CacheScope()
	key := domain.SCMProviderCacheKey{SCMProviderCacheScope: scope, Namespace: cacheCheckGuard, Key: headSHA}
	entry, hasEntry, _ := cache.GetProviderCache(ctx, key)
	owner, repo := subj.Repository().OwnerName()
	q := url.Values{"per_page": []string{"1"}}
	resp, err := p.client.DoREST(ctx, http.MethodGet, repoPath(owner, repo, "commits", headSHA, "check-runs"), q, nil, entry.ETag, "github.check_runs_guard")
	if err != nil {
		return true, resp.Diagnostic, nil, err
	}
	_, commit := prepareRESTCache(ctx, cache, key, entry, hasEntry, resp, now)
	return !resp.NotModified, resp.Diagnostic, commit, nil
}

func (p *Provider) reviewCommentsChanged(ctx context.Context, cache ports.SCMProviderCache, subj domain.SCMSubject, now time.Time) (bool, domain.SCMDiagnostic, restCacheCommit, error) {
	if subj.PRNumber == 0 {
		return false, domain.SCMDiagnostic{}, nil, nil
	}
	scope := subj.CacheScope()
	key := domain.SCMProviderCacheKey{SCMProviderCacheScope: scope, Namespace: cacheReviews, Key: strconv.Itoa(subj.PRNumber)}
	entry, hasEntry, _ := cache.GetProviderCache(ctx, key)
	owner, repo := subj.Repository().OwnerName()
	resp, err := p.client.DoREST(ctx, http.MethodGet, repoPath(owner, repo, "pulls", strconv.Itoa(subj.PRNumber), "comments"), nil, nil, entry.ETag, "github.review_comments_guard")
	if err != nil {
		return true, resp.Diagnostic, nil, err
	}
	_, commit := prepareRESTCache(ctx, cache, key, entry, hasEntry, resp, now)
	return !resp.NotModified, resp.Diagnostic, commit, nil
}

func (p *Provider) checkBoundPullState(ctx context.Context, cache ports.SCMProviderCache, subj domain.SCMSubject, now time.Time) (changed bool, terminal *domain.SCMSnapshot, diag domain.SCMDiagnostic, commit restCacheCommit, err error) {
	if subj.PRNumber == 0 {
		return false, nil, domain.SCMDiagnostic{}, nil, nil
	}
	scope := subj.CacheScope()
	key := domain.SCMProviderCacheKey{SCMProviderCacheScope: scope, Namespace: cachePRState, Key: strconv.Itoa(subj.PRNumber)}
	entry, hasEntry, _ := cache.GetProviderCache(ctx, key)
	owner, repo := subj.Repository().OwnerName()
	resp, err := p.client.DoREST(ctx, http.MethodGet, repoPath(owner, repo, "pulls", strconv.Itoa(subj.PRNumber)), nil, nil, entry.ETag, "github.pr_state_guard")
	if err != nil {
		return true, nil, resp.Diagnostic, nil, err
	}
	body, commit := prepareRESTCache(ctx, cache, key, entry, hasEntry, resp, now)
	if resp.NotModified {
		commitRESTCache(commit)
		return false, nil, resp.Diagnostic, nil, nil
	}
	snap, isTerminal, err := snapshotFromRESTPull(subj, body, now, "github.pr_state_guard")
	if err != nil {
		return true, nil, resp.Diagnostic, nil, err
	}
	if isTerminal {
		commitRESTCache(commit)
		return true, &snap, resp.Diagnostic, nil, nil
	}
	return true, nil, resp.Diagnostic, commit, nil
}

func chunkSubjects(subjects []domain.SCMSubject, size int) [][]domain.SCMSubject {
	if size <= 0 || len(subjects) <= size {
		return [][]domain.SCMSubject{subjects}
	}
	out := make([][]domain.SCMSubject, 0, (len(subjects)+size-1)/size)
	for start := 0; start < len(subjects); start += size {
		end := start + size
		if end > len(subjects) {
			end = len(subjects)
		}
		out = append(out, subjects[start:end])
	}
	return out
}

func diagnosticFromError(operation string, err error) domain.SCMDiagnostic {
	d := domain.SCMDiagnostic{Operation: operation, ErrorKind: domain.SCMErrorUnavailable, Message: fmt.Sprint(err)}
	if se, ok := err.(*domain.SCMError); ok {
		d.Operation = firstNonEmpty(se.Operation, operation)
		d.ErrorKind = se.Kind
		d.StatusCode = se.StatusCode
	}
	return d
}

func shouldFetchCheckRuns(snap domain.SCMSnapshot, prData map[string]any) bool {
	if snap.PR == nil || snap.PR.HeadSHA == "" {
		return false
	}
	missing, incomplete := checkContextsMissingOrIncomplete(prData)
	if missing || incomplete {
		return true
	}
	return snap.CI.Summary == "failing" && !hasFailedCheck(snap.CI.Checks)
}

func checkContextsMissingOrIncomplete(pr map[string]any) (bool, bool) {
	roll := statusRollup(pr)
	contexts, _ := roll["contexts"].(map[string]any)
	if contexts == nil {
		return true, false
	}
	pageInfo, _ := contexts["pageInfo"].(map[string]any)
	return false, boolv(pageInfo["hasNextPage"])
}

func hasFailedCheck(checks []domain.SCMCheck) bool {
	for _, check := range checks {
		if failedCheck(check) {
			return true
		}
	}
	return false
}

type snapshotReader interface {
	GetLatestSnapshot(ctx context.Context, sessionID domain.SessionID) (domain.SCMSnapshot, bool, error)
}

type pollStateReader interface {
	GetPollState(ctx context.Context, key domain.SCMPollStateKey) (domain.SCMPollState, bool, error)
}

func (p *Provider) observeKnownPRs(ctx context.Context, subjects []domain.SCMSubject, cache ports.SCMProviderCache, now time.Time) ([]domain.SCMSnapshot, []domain.SCMDiagnostic, *domain.SCMRateLimit, error) {
	known := make([]domain.SCMSubject, 0, len(subjects))
	snaps := make([]domain.SCMSnapshot, 0, len(subjects))
	for _, subj := range subjects {
		if subj.PRNumber == 0 {
			snaps = append(snaps, domain.SCMSnapshot{SessionID: subj.SessionID, Subject: subj, Freshness: domain.SCMFreshnessFresh, ObservedAt: now})
			continue
		}
		known = append(known, subj)
	}
	if len(known) == 0 {
		return snaps, nil, nil, nil
	}

	owner, repo := known[0].Repository().OwnerName()
	latest := latestSnapshots(ctx, cache, known)
	prListChanged := true
	var prListCommit restCacheCommit
	diags := []domain.SCMDiagnostic{}
	if len(latest) > 0 {
		scope := known[0].CacheScope()
		changed, prListDiag, commit, err := p.checkOpenPullListChanged(ctx, cache, scope, owner, repo, now)
		prListChanged = changed
		prListCommit = commit
		if prListDiag.Operation != "" {
			diags = append(diags, prListDiag)
		}
		if err != nil {
			// PR-list guard failures should not be terminal truth. Fall through to
			// GraphQL so one failed optimization guard does not hide real PR changes.
			prListChanged = true
		}
	}

	toFetch := known
	checkGuardCommits := map[domain.SessionID]restCacheCommit{}
	pullStateCommits := map[domain.SessionID]restCacheCommit{}
	if !prListChanged && len(latest) > 0 {
		toFetch = toFetch[:0]
		for _, subj := range known {
			prev, ok := latest[subj.SessionID]
			if !ok || prev.PR == nil || prev.PR.HeadSHA == "" {
				toFetch = append(toFetch, subj)
				continue
			}
			pullChanged, terminal, diag, commit, err := p.checkBoundPullState(ctx, cache, subj, now)
			if diag.Operation != "" {
				diags = append(diags, diag)
			}
			if terminal != nil {
				snaps = append(snaps, *terminal)
				continue
			}
			if err != nil || pullChanged {
				if err == nil && commit != nil {
					pullStateCommits[subj.SessionID] = commit
				}
				toFetch = append(toFetch, subj)
				continue
			}
			changed, diag, commit, err := p.checkRunsChanged(ctx, cache, subj, prev.PR.HeadSHA, now)
			if diag.Operation != "" {
				diags = append(diags, diag)
			}
			if err != nil || changed {
				if err == nil && changed {
					checkGuardCommits[subj.SessionID] = commit
				}
				toFetch = append(toFetch, subj)
				continue
			}
			reused := prev
			reused.ObservedAt = now
			reused.Freshness = domain.SCMFreshnessUnchanged
			reused.Subject = subj
			reviewDiags := p.refreshReviewDetails(ctx, cache, subj, &reused, prev, true, now)
			diags = append(diags, reviewDiags...)
			snaps = append(snaps, reused)
		}
	}
	if len(toFetch) == 0 {
		return snaps, diags, nil, nil
	}

	var firstErr error
	var lastRL *domain.SCMRateLimit
	mainFetchOK := true
	for _, batch := range chunkSubjects(toFetch, maxGraphQLBatchSize) {
		data, rl, diag, err := p.fetchPRBatch(ctx, owner, repo, batch)
		if diag.Operation != "" {
			diags = append(diags, diag)
		}
		if rl != nil {
			lastRL = rl
		}
		if err != nil {
			mainFetchOK = false
			if firstErr == nil {
				firstErr = err
			}
			for _, subj := range batch {
				if terminal, ok, fallbackDiag, fallbackErr := p.fetchTerminalPRFallback(ctx, subj, now); ok {
					if fallbackDiag.Operation != "" {
						terminal.Diagnostics = append(terminal.Diagnostics, fallbackDiag)
					}
					snaps = append(snaps, terminal)
					continue
				} else if fallbackDiag.Operation != "" {
					diags = append(diags, fallbackDiag)
				} else if fallbackErr != nil {
					diags = append(diags, diagnosticFromError("github.rest_pr_fallback", fallbackErr))
				}
				if prev, ok := latest[subj.SessionID]; ok {
					prev.ObservedAt = now
					prev.Freshness = domain.SCMFreshnessUnavailable
					prev.Diagnostics = append(prev.Diagnostics, diagnosticFromError("github.graphql_pr_batch", err))
					snaps = append(snaps, prev)
				} else {
					snaps = append(snaps, unavailableSnapshot(subj, now, err))
				}
			}
			continue
		}
		for _, subj := range batch {
			prData, _ := data[aliasFor(subj.PRNumber)].(map[string]any)
			if prData == nil {
				mainFetchOK = false
				err := &domain.SCMError{Kind: domain.SCMErrorNotFound, Operation: "github.graphql_pr", Message: "pull request not found"}
				if prev, ok := latest[subj.SessionID]; ok {
					prev.ObservedAt = now
					prev.Freshness = domain.SCMFreshnessUnavailable
					prev.Diagnostics = append(prev.Diagnostics, diagnosticFromError("github.graphql_pr", err))
					snaps = append(snaps, prev)
				} else {
					snaps = append(snaps, unavailableSnapshot(subj, now, err))
				}
				continue
			}
			snap := snapshotFromGraphQL(subj, prData, now)
			prev, havePrev := latest[subj.SessionID]
			if shouldFetchCheckRuns(snap, prData) {
				checks, checkDiag, err := p.fetchCheckRuns(ctx, cache, subj, snap, now)
				if checkDiag.Operation != "" {
					snap.Diagnostics = append(snap.Diagnostics, checkDiag)
				}
				if err == nil {
					snap.CI.Checks = checks
					snap.CI.Summary = summarizeChecks(checks)
					commitRESTCache(checkGuardCommits[subj.SessionID])
					commitRESTCache(pullStateCommits[subj.SessionID])
				} else if err != nil && havePrev && prev.PR != nil && prev.PR.HeadSHA == snap.PR.HeadSHA {
					snap.CI = prev.CI
					snap.Diagnostics = append(snap.Diagnostics, diagnosticFromError("github.check_runs", err))
				}
			} else {
				commitRESTCache(checkGuardCommits[subj.SessionID])
				commitRESTCache(pullStateCommits[subj.SessionID])
			}
			if snap.CI.Summary == "failing" {
				snap.CI.FailureLogTail = combinedFailureTail(snap.CI.Checks)
			}
			reviewDiags := p.refreshReviewDetails(ctx, cache, subj, &snap, prev, havePrev, now)
			snap.Diagnostics = append(snap.Diagnostics, reviewDiags...)
			finalizeMergeability(&snap)
			snaps = append(snaps, snap)
		}
	}
	if mainFetchOK {
		commitRESTCache(prListCommit)
	}
	return snaps, diags, lastRL, firstErr
}

func (p *Provider) fetchPRBatch(ctx context.Context, owner, repo string, subjects []domain.SCMSubject) (map[string]any, *domain.SCMRateLimit, domain.SCMDiagnostic, error) {
	var b strings.Builder
	b.WriteString("query($owner:String!,$repo:String!){ repository(owner:$owner,name:$repo){")
	seen := map[int]bool{}
	for _, subj := range subjects {
		if seen[subj.PRNumber] {
			continue
		}
		seen[subj.PRNumber] = true
		fmt.Fprintf(&b, `%s: pullRequest(number:%d){ number title url state isDraft merged closed headRefName baseRefName headRefOid additions deletions mergeable reviewDecision mergeStateStatus commits(last:1){ nodes{ commit{ statusCheckRollup{ state contexts(first:%d){ nodes{ __typename ... on CheckRun { name status conclusion detailsUrl url } ... on StatusContext { context state targetUrl } } pageInfo{ hasNextPage } } } } } } } }`, aliasFor(subj.PRNumber), subj.PRNumber, graphQLCheckContextLimit)
	}
	b.WriteString("} rateLimit{ limit remaining resetAt } }")
	data, rl, diag, err := p.client.DoGraphQL(ctx, b.String(), map[string]any{"owner": owner, "repo": repo}, "github.graphql_pr_batch")
	if err != nil {
		return nil, rl, diag, err
	}
	repoData, _ := data["repository"].(map[string]any)
	return repoData, rl, diag, nil
}

func snapshotFromGraphQL(subj domain.SCMSubject, pr map[string]any, now time.Time) domain.SCMSnapshot {
	number := int(num(pr["number"]))
	if number == 0 {
		number = subj.PRNumber
	}
	state := prStateFromGraphQL(str(pr["state"]), boolv(pr["isDraft"]), boolv(pr["merged"]), boolv(pr["closed"]))
	url := firstNonEmpty(str(pr["url"]), subj.PRURL)
	pull := &domain.SCMPullRequest{ID: domain.SCMChangeRequestID{Provider: subj.Provider, Host: subj.Host, Repo: subj.Repo, Number: number}, Number: number, URL: url, Title: str(pr["title"]), State: state, Draft: boolv(pr["isDraft"]), Merged: boolv(pr["merged"]), SourceBranch: str(pr["headRefName"]), TargetBranch: str(pr["baseRefName"]), HeadSHA: str(pr["headRefOid"]), Additions: int(num(pr["additions"])), Deletions: int(num(pr["deletions"]))}
	if pull.SourceBranch == "" {
		pull.SourceBranch = subj.Branch
	}
	if pull.TargetBranch == "" {
		pull.TargetBranch = subj.BaseBranch
	}
	subj.PRNumber = number
	subj.PRURL = url
	subj.BaseBranch = firstNonEmpty(subj.BaseBranch, pull.TargetBranch)
	ci := ciFromGraphQL(pr)
	review := reviewFromGraphQL(pr)
	merge := mergeabilityFromGraphQL(pr, ci, review)
	return domain.SCMSnapshot{SessionID: subj.SessionID, Subject: subj, Freshness: domain.SCMFreshnessFresh, ObservedAt: now, PR: pull, CI: ci, Review: review, Mergeability: merge}
}

func ciFromGraphQL(pr map[string]any) domain.SCMCI {
	roll := statusRollup(pr)
	ci := domain.SCMCI{Summary: domain.NormalizeSCMCI(str(roll["state"]))}
	contexts, _ := roll["contexts"].(map[string]any)
	for _, n := range nodes(contexts["nodes"]) {
		typ := str(n["__typename"])
		check := domain.SCMCheck{}
		switch typ {
		case "CheckRun":
			check.Name = str(n["name"])
			check.Status = str(n["status"])
			check.Conclusion = str(n["conclusion"])
			check.URL = firstNonEmpty(str(n["detailsUrl"]), str(n["url"]))
		case "StatusContext":
			check.Name = str(n["context"])
			check.Conclusion = str(n["state"])
			check.URL = str(n["targetUrl"])
		}
		if check.Name != "" {
			ci.Checks = append(ci.Checks, check)
		}
	}
	if ci.Summary == "" {
		ci.Summary = summarizeChecks(ci.Checks)
	}
	return ci
}

func statusRollup(pr map[string]any) map[string]any {
	commits, _ := pr["commits"].(map[string]any)
	for _, n := range nodes(commits["nodes"]) {
		commit, _ := n["commit"].(map[string]any)
		roll, _ := commit["statusCheckRollup"].(map[string]any)
		if roll != nil {
			return roll
		}
	}
	return nil
}

func reviewFromGraphQL(pr map[string]any) domain.SCMReview {
	review := domain.SCMReview{Decision: domain.NormalizeSCMReviewDecision(str(pr["reviewDecision"]))}
	threads, _ := pr["reviewThreads"].(map[string]any)
	for _, n := range nodes(threads["nodes"]) {
		if boolv(n["isResolved"]) {
			continue
		}
		th := domain.SCMReviewThread{ID: str(n["id"])}
		comments, _ := n["comments"].(map[string]any)
		for _, cn := range nodes(comments["nodes"]) {
			author, _ := cn["author"].(map[string]any)
			login := str(author["login"])
			isBot := isBotAuthor(login, str(author["__typename"]))
			path := str(cn["path"])
			line := int(num(cn["line"]))
			if th.Path == "" {
				th.Path = path
			}
			if th.Line == 0 {
				th.Line = line
			}
			th.Comments = append(th.Comments, domain.SCMReviewComment{ID: str(cn["id"]), Author: login, Body: str(cn["body"]), URL: str(cn["url"]), IsBot: isBot, Path: th.Path, Line: th.Line, ThreadID: th.ID})
			if th.URL == "" {
				th.URL = str(cn["url"])
			}
		}
		th.IsBot = threadIsBot(th.Comments)
		review.UnresolvedThreads = append(review.UnresolvedThreads, th)
	}
	classifyThreads(&review)
	return review
}

func mergeabilityFromGraphQL(pr map[string]any, ci domain.SCMCI, review domain.SCMReview) domain.SCMMergeability {
	raw := strings.ToUpper(str(pr["mergeable"]))
	mergeState := strings.ToUpper(str(pr["mergeStateStatus"]))
	m := domain.SCMMergeability{RawState: raw, MergeState: mergeState}
	m.CIPassing = ci.Summary == "passing" || ci.Summary == "none" || ci.Summary == ""
	m.Approved = review.Decision == "approved" || review.Decision == "none" || review.Decision == ""
	m.Conflict = raw == "CONFLICTING" || mergeState == "DIRTY"
	m.NoConflicts = raw == "MERGEABLE" && !m.Conflict
	m.BehindBase = mergeState == "BEHIND"
	draft := boolv(pr["isDraft"])
	if raw == "" || raw == "UNKNOWN" {
		m.Blockers = append(m.Blockers, "merge status unknown (GitHub is computing)")
	}
	if m.Conflict {
		m.Blockers = append(m.Blockers, "merge conflicts")
	}
	if m.BehindBase {
		m.Blockers = append(m.Blockers, "branch is behind base")
	}
	if ci.Summary == "failing" {
		m.Blockers = append(m.Blockers, "CI failing")
	}
	if ci.Summary == "pending" {
		m.Blockers = append(m.Blockers, "CI pending")
	}
	if review.Decision == "changes_requested" {
		m.Blockers = append(m.Blockers, "changes requested")
	}
	if review.Decision == "pending" {
		m.Blockers = append(m.Blockers, "review required")
	}
	if mergeState == "BLOCKED" {
		m.Blockers = append(m.Blockers, "merge blocked by branch protection")
	}
	if mergeState == "UNSTABLE" {
		m.Blockers = append(m.Blockers, "required checks are failing")
	}
	if draft {
		m.Blockers = append(m.Blockers, "PR is still a draft")
	}
	m.Mergeable = raw == "MERGEABLE" && !draft && m.NoConflicts && !m.BehindBase && m.CIPassing && m.Approved && len(m.Blockers) == 0
	return m
}

func finalizeMergeability(s *domain.SCMSnapshot) {
	if s.PR == nil || s.PR.State == domain.PRMerged {
		return
	}
	raw := strings.ToUpper(strings.TrimSpace(s.Mergeability.RawState))
	mergeState := strings.ToUpper(strings.TrimSpace(s.Mergeability.MergeState))
	ciSummary := domain.NormalizeSCMCI(s.CI.Summary)
	reviewDecision := domain.NormalizeSCMReviewDecision(s.Review.Decision)
	draft := s.PR.Draft || s.PR.State == domain.PRDraft

	s.Mergeability.RawState = raw
	s.Mergeability.MergeState = mergeState
	s.Mergeability.CIPassing = ciSummary == "passing" || ciSummary == "none" || ciSummary == ""
	s.Mergeability.Approved = reviewDecision == "approved" || reviewDecision == "none" || reviewDecision == ""
	s.Mergeability.Conflict = raw == "CONFLICTING" || mergeState == "DIRTY"
	s.Mergeability.NoConflicts = raw == "MERGEABLE" && !s.Mergeability.Conflict
	s.Mergeability.BehindBase = mergeState == "BEHIND"
	s.Mergeability.Blockers = nil
	addBlocker := func(msg string) {
		if !containsString(s.Mergeability.Blockers, msg) {
			s.Mergeability.Blockers = append(s.Mergeability.Blockers, msg)
		}
	}
	if raw == "" || raw == "UNKNOWN" {
		addBlocker("merge status unknown (GitHub is computing)")
	}
	if s.Mergeability.Conflict {
		addBlocker("merge conflicts")
	}
	if s.Mergeability.BehindBase {
		addBlocker("branch is behind base")
	}
	if ciSummary == "failing" {
		addBlocker("CI failing")
	}
	if ciSummary == "pending" {
		addBlocker("CI pending")
	}
	if reviewDecision == "changes_requested" {
		addBlocker("changes requested")
	}
	if reviewDecision == "pending" {
		addBlocker("review required")
	}
	if mergeState == "BLOCKED" {
		addBlocker("merge blocked by branch protection")
	}
	if mergeState == "UNSTABLE" {
		addBlocker("required checks are failing")
	}
	if draft {
		addBlocker("PR is still a draft")
	}
	s.Mergeability.Mergeable = s.PR.State == domain.PROpen &&
		raw == "MERGEABLE" &&
		!draft &&
		s.Mergeability.NoConflicts &&
		!s.Mergeability.BehindBase &&
		s.Mergeability.CIPassing &&
		s.Mergeability.Approved &&
		len(s.Mergeability.Blockers) == 0
}

func (p *Provider) fetchCheckRuns(ctx context.Context, cache ports.SCMProviderCache, subj domain.SCMSubject, snap domain.SCMSnapshot, now time.Time) ([]domain.SCMCheck, domain.SCMDiagnostic, error) {
	if snap.PR == nil || snap.PR.HeadSHA == "" {
		return nil, domain.SCMDiagnostic{}, nil
	}
	scope := subj.CacheScope()
	key := domain.SCMProviderCacheKey{SCMProviderCacheScope: scope, Namespace: cacheChecks, Key: snap.PR.HeadSHA}
	entry, hasEntry, _ := cache.GetProviderCache(ctx, key)
	owner, repo := subj.Repository().OwnerName()
	q := url.Values{"per_page": []string{"100"}}
	resp, err := p.client.DoREST(ctx, http.MethodGet, repoPath(owner, repo, "commits", snap.PR.HeadSHA, "check-runs"), q, nil, entry.ETag, "github.check_runs")
	if err != nil {
		return nil, resp.Diagnostic, err
	}
	body, commit := prepareRESTCache(ctx, cache, key, entry, hasEntry, resp, now)
	var decoded struct {
		CheckRuns []struct {
			Name       string `json:"name"`
			Status     string `json:"status"`
			Conclusion string `json:"conclusion"`
			HTMLURL    string `json:"html_url"`
			DetailsURL string `json:"details_url"`
			Output     struct {
				Title   string `json:"title"`
				Summary string `json:"summary"`
				Text    string `json:"text"`
			} `json:"output"`
		} `json:"check_runs"`
	}
	if err := json.Unmarshal(body, &decoded); err != nil {
		return nil, resp.Diagnostic, &domain.SCMError{Kind: domain.SCMErrorParse, Operation: "github.check_runs", Message: err.Error(), Cause: err}
	}
	commitRESTCache(commit)
	checks := make([]domain.SCMCheck, 0, len(decoded.CheckRuns))
	for _, r := range decoded.CheckRuns {
		checks = append(checks, domain.SCMCheck{Name: r.Name, Status: r.Status, Conclusion: r.Conclusion, URL: firstNonEmpty(r.HTMLURL, r.DetailsURL), Details: firstNonEmpty(r.Output.Title, r.Output.Summary), LogTail: tailLines(firstNonEmpty(r.Output.Text, r.Output.Summary), ciFailureLogTailLines)})
	}
	return checks, resp.Diagnostic, nil
}

type reviewDetailsCache struct {
	Decision string                   `json:"decision,omitempty"`
	Threads  []domain.SCMReviewThread `json:"threads,omitempty"`
}

func (p *Provider) refreshReviewDetails(ctx context.Context, cache ports.SCMProviderCache, subj domain.SCMSubject, snap *domain.SCMSnapshot, prev domain.SCMSnapshot, havePrev bool, now time.Time) []domain.SCMDiagnostic {
	if snap == nil || subj.PRNumber == 0 {
		return nil
	}
	cached, entry, hasCache := getCachedReviewDetails(ctx, cache, subj)
	prevDecision := ""
	if havePrev {
		prevDecision = prev.Review.Decision
	}
	decisionChanged := havePrev && prevDecision != snap.Review.Decision
	forceGraphQL := decisionChanged && (snap.Review.Decision == "changes_requested" || prevDecision == "changes_requested" || len(prev.Review.UnresolvedThreads) > 0)

	attach := func(threads []domain.SCMReviewThread) {
		snap.Review.UnresolvedThreads = cloneReviewThreads(threads)
		classifyThreads(&snap.Review)
	}
	attachFallback := func() {
		switch {
		case hasCache:
			attach(cached.Threads)
		case havePrev:
			attach(prev.Review.UnresolvedThreads)
		default:
			classifyThreads(&snap.Review)
		}
	}

	if forceGraphQL {
		threads, diags, err := p.fetchReviewThreadsGraphQL(ctx, subj)
		if err != nil {
			attachFallback()
			return append(diags, diagnosticFromError("github.review_threads", err))
		}
		attach(threads)
		putCachedReviewDetails(ctx, cache, subj, snap.Review.Decision, threads, now)
		return diags
	}

	threads, diags, changed, err := p.fetchReviewThreads(ctx, cache, subj, now)
	if err != nil {
		attachFallback()
		return append(diags, diagnosticFromError("github.review_threads", err))
	}
	if changed {
		attach(threads)
		putCachedReviewDetails(ctx, cache, subj, snap.Review.Decision, threads, now)
		return diags
	}
	if hasCache {
		attach(cached.Threads)
		touchCachedReviewDetails(ctx, cache, entry, now)
		return diags
	}
	if havePrev {
		attach(prev.Review.UnresolvedThreads)
		putCachedReviewDetails(ctx, cache, subj, snap.Review.Decision, prev.Review.UnresolvedThreads, now)
		return diags
	}
	attach(nil)
	putCachedReviewDetails(ctx, cache, subj, snap.Review.Decision, nil, now)
	return diags
}

func (p *Provider) fetchReviewThreads(ctx context.Context, cache ports.SCMProviderCache, subj domain.SCMSubject, now time.Time) ([]domain.SCMReviewThread, []domain.SCMDiagnostic, bool, error) {
	if subj.PRNumber == 0 {
		return nil, nil, false, nil
	}
	scope := subj.CacheScope()
	key := domain.SCMProviderCacheKey{SCMProviderCacheScope: scope, Namespace: cacheReviews, Key: strconv.Itoa(subj.PRNumber)}
	entry, hasEntry, _ := cache.GetProviderCache(ctx, key)
	owner, repo := subj.Repository().OwnerName()
	resp, err := p.client.DoREST(ctx, http.MethodGet, repoPath(owner, repo, "pulls", strconv.Itoa(subj.PRNumber), "comments"), nil, nil, entry.ETag, "github.review_comments")
	diags := []domain.SCMDiagnostic{}
	if resp.Diagnostic.Operation != "" {
		diags = append(diags, resp.Diagnostic)
	}
	if err != nil {
		return nil, diags, false, err
	}
	body, commit := prepareRESTCache(ctx, cache, key, entry, hasEntry, resp, now)
	if resp.NotModified {
		return nil, diags, false, nil
	}
	if reviewCommentsEmpty(body) {
		commitRESTCache(commit)
		return nil, diags, true, nil
	}
	threads, graphDiags, err := p.fetchReviewThreadsGraphQL(ctx, subj)
	diags = append(diags, graphDiags...)
	if err != nil {
		return nil, diags, true, err
	}
	commitRESTCache(commit)
	return threads, diags, true, nil
}

func (p *Provider) fetchReviewThreadsGraphQL(ctx context.Context, subj domain.SCMSubject) ([]domain.SCMReviewThread, []domain.SCMDiagnostic, error) {
	if subj.PRNumber == 0 {
		return nil, nil, nil
	}
	owner, repo := subj.Repository().OwnerName()
	query := `query($owner:String!,$repo:String!,$number:Int!){ repository(owner:$owner,name:$repo){ pullRequest(number:$number){ reviewThreads(last:100){ nodes{ id isResolved comments(first:100){ nodes{ id author{ login __typename } body path line url } } } } } } rateLimit{ limit remaining resetAt } }`
	data, _, diag, err := p.client.DoGraphQL(ctx, query, map[string]any{"owner": owner, "repo": repo, "number": subj.PRNumber}, "github.review_threads")
	diags := []domain.SCMDiagnostic{}
	if diag.Operation != "" {
		diags = append(diags, diag)
	}
	if err != nil {
		return nil, diags, err
	}
	repoData, _ := data["repository"].(map[string]any)
	prData, _ := repoData["pullRequest"].(map[string]any)
	review := reviewFromGraphQL(prData)
	return review.UnresolvedThreads, diags, nil
}

func (p *Provider) fetchTerminalPRFallback(ctx context.Context, subj domain.SCMSubject, now time.Time) (domain.SCMSnapshot, bool, domain.SCMDiagnostic, error) {
	if subj.PRNumber == 0 {
		return domain.SCMSnapshot{}, false, domain.SCMDiagnostic{}, nil
	}
	owner, repo := subj.Repository().OwnerName()
	resp, err := p.client.DoREST(ctx, http.MethodGet, repoPath(owner, repo, "pulls", strconv.Itoa(subj.PRNumber)), nil, nil, "", "github.rest_pr_fallback")
	if err != nil {
		return domain.SCMSnapshot{}, false, resp.Diagnostic, err
	}
	snap, terminal, err := snapshotFromRESTPull(subj, resp.Body, now, "github.rest_pr_fallback")
	return snap, terminal, resp.Diagnostic, err
}

func snapshotFromRESTPull(subj domain.SCMSubject, body []byte, now time.Time, operation string) (domain.SCMSnapshot, bool, error) {
	var decoded struct {
		Number    int    `json:"number"`
		HTMLURL   string `json:"html_url"`
		Title     string `json:"title"`
		State     string `json:"state"`
		Draft     bool   `json:"draft"`
		Merged    bool   `json:"merged"`
		Additions int    `json:"additions"`
		Deletions int    `json:"deletions"`
		Head      struct {
			Ref string `json:"ref"`
			SHA string `json:"sha"`
		} `json:"head"`
		Base struct {
			Ref string `json:"ref"`
		} `json:"base"`
	}
	if err := json.Unmarshal(body, &decoded); err != nil {
		return domain.SCMSnapshot{}, false, &domain.SCMError{Kind: domain.SCMErrorParse, Operation: operation, Message: err.Error(), Cause: err}
	}
	state := domain.PROpen
	if decoded.Merged {
		state = domain.PRMerged
	} else if strings.EqualFold(decoded.State, "closed") {
		state = domain.PRClosed
	} else if decoded.Draft {
		state = domain.PRDraft
	}
	if state != domain.PRMerged && state != domain.PRClosed {
		return domain.SCMSnapshot{}, false, nil
	}
	number := decoded.Number
	if number == 0 {
		number = subj.PRNumber
	}
	url := firstNonEmpty(decoded.HTMLURL, subj.PRURL)
	pull := &domain.SCMPullRequest{
		ID:           domain.SCMChangeRequestID{Provider: subj.Provider, Host: subj.Host, Repo: subj.Repo, Number: number},
		Number:       number,
		URL:          url,
		Title:        decoded.Title,
		State:        state,
		Draft:        decoded.Draft,
		Merged:       decoded.Merged || state == domain.PRMerged,
		SourceBranch: firstNonEmpty(decoded.Head.Ref, subj.Branch),
		TargetBranch: firstNonEmpty(decoded.Base.Ref, subj.BaseBranch),
		HeadSHA:      decoded.Head.SHA,
		Additions:    decoded.Additions,
		Deletions:    decoded.Deletions,
	}
	subj.PRNumber = number
	subj.PRURL = url
	subj.BaseBranch = firstNonEmpty(subj.BaseBranch, pull.TargetBranch)
	return domain.SCMSnapshot{SessionID: subj.SessionID, Subject: subj, Freshness: domain.SCMFreshnessFresh, ObservedAt: now, PR: pull}, true, nil
}

func reviewCommentsEmpty(body []byte) bool {
	var comments []json.RawMessage
	if err := json.Unmarshal(body, &comments); err != nil {
		return false
	}
	return len(comments) == 0
}

func repoPath(owner, repo string, elems ...string) string {
	all := append([]string{"repos", owner, repo}, elems...)
	for i := range all {
		all[i] = url.PathEscape(all[i])
	}
	return "/" + path.Join(all...)
}

func aliasFor(number int) string { return fmt.Sprintf("pr%d", number) }

func prStateFromGraphQL(state string, draft, merged, closed bool) domain.PRState {
	if merged || strings.EqualFold(state, "MERGED") {
		return domain.PRMerged
	}
	if closed || strings.EqualFold(state, "CLOSED") {
		return domain.PRClosed
	}
	if draft {
		return domain.PRDraft
	}
	return domain.PROpen
}

func summarizeChecks(checks []domain.SCMCheck) string {
	if len(checks) == 0 {
		return "none"
	}
	pending := false
	passing := false
	for _, c := range checks {
		if failedCheck(c) {
			return "failing"
		}
		if isPendingCheck(c) {
			pending = true
			continue
		}
		if isPassingCheck(c) {
			passing = true
		}
	}
	if pending {
		return "pending"
	}
	if passing {
		return "passing"
	}
	return "none"
}

func failedCheck(c domain.SCMCheck) bool {
	s := strings.ToLower(strings.TrimSpace(firstNonEmpty(c.Conclusion, c.Status)))
	return s == "failure" || s == "failed" || s == "error" || s == "timed_out" || s == "cancelled" || s == "action_required"
}

func isPendingCheck(c domain.SCMCheck) bool {
	status := strings.ToLower(strings.TrimSpace(c.Status))
	conclusion := strings.ToLower(strings.TrimSpace(c.Conclusion))
	if conclusion != "" {
		return conclusion == "pending" || conclusion == "queued" || conclusion == "in_progress" || conclusion == "requested" || conclusion == "waiting" || conclusion == "expected"
	}
	return status == "queued" || status == "in_progress" || status == "pending" || status == "requested" || status == "waiting" || status == "expected"
}

func isPassingCheck(c domain.SCMCheck) bool {
	status := strings.ToLower(strings.TrimSpace(c.Status))
	conclusion := strings.ToLower(strings.TrimSpace(c.Conclusion))
	if conclusion == "success" || conclusion == "successful" || conclusion == "passed" || conclusion == "passing" || conclusion == "pass" {
		return true
	}
	return conclusion == "" && (status == "success" || status == "successful" || status == "passed" || status == "passing" || status == "pass")
}

func combinedFailureTail(checks []domain.SCMCheck) string {
	var parts []string
	for _, c := range checks {
		if failedCheck(c) && c.LogTail != "" {
			parts = append(parts, c.Name+":\n"+c.LogTail)
		}
	}
	return strings.Join(parts, "\n\n")
}

func tailLines(s string, n int) string {
	lines := strings.Split(strings.ReplaceAll(strings.TrimSpace(s), "\r\n", "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

func classifyThreads(review *domain.SCMReview) {
	review.BotComments = nil
	review.HumanComments = nil
	for i := range review.UnresolvedThreads {
		review.UnresolvedThreads[i].IsBot = threadIsBot(review.UnresolvedThreads[i].Comments)
		if review.UnresolvedThreads[i].IsBot {
			review.BotComments = append(review.BotComments, review.UnresolvedThreads[i])
		} else {
			review.HumanComments = append(review.HumanComments, review.UnresolvedThreads[i])
		}
	}
	if len(review.HumanComments) > 0 && review.Decision == "none" {
		review.Decision = "changes_requested"
	}
}

func cloneReviewThreads(in []domain.SCMReviewThread) []domain.SCMReviewThread {
	if len(in) == 0 {
		return nil
	}
	out := make([]domain.SCMReviewThread, len(in))
	for i := range in {
		out[i] = in[i]
		if len(in[i].Comments) > 0 {
			out[i].Comments = append([]domain.SCMReviewComment(nil), in[i].Comments...)
		}
	}
	return out
}

func threadIsBot(comments []domain.SCMReviewComment) bool {
	if len(comments) == 0 {
		return false
	}
	for _, c := range comments {
		if !c.IsBot {
			return false
		}
	}
	return true
}

func isBotAuthor(login, typ string) bool {
	login = strings.ToLower(login)
	typ = strings.ToLower(typ)
	if typ == "bot" || strings.Contains(login, "[bot]") || strings.HasSuffix(login, "-bot") {
		return true
	}
	_, ok := knownBotLogins[login]
	return ok
}

var knownBotLogins = map[string]struct{}{
	"codecov":        {},
	"cursor":         {},
	"dependabot":     {},
	"github-actions": {},
	"renovate":       {},
	"sonarcloud":     {},
	"snyk":           {},
}

func nodes(v any) []map[string]any {
	a, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]map[string]any, 0, len(a))
	for _, item := range a {
		if m, ok := item.(map[string]any); ok {
			out = append(out, m)
		}
	}
	return out
}

func str(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

func boolv(v any) bool {
	if b, ok := v.(bool); ok {
		return b
	}
	return false
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

type restPull struct {
	Number  int    `json:"number"`
	HTMLURL string `json:"html_url"`
	Head    struct {
		Ref string `json:"ref"`
	} `json:"head"`
	Base struct {
		Ref string `json:"ref"`
	} `json:"base"`
}

type branchMapping struct {
	Number     int    `json:"number"`
	URL        string `json:"url"`
	Branch     string `json:"branch"`
	BaseBranch string `json:"baseBranch"`
}

func getCachedBranch(ctx context.Context, cache ports.SCMProviderCache, scope domain.SCMProviderCacheScope, branch string) (branchMapping, bool, error) {
	entry, ok, err := cache.GetProviderCache(ctx, domain.SCMProviderCacheKey{SCMProviderCacheScope: scope, Namespace: cacheBranchMap, Key: branch})
	if err != nil || !ok {
		return branchMapping{}, false, err
	}
	var mapped branchMapping
	if err := json.Unmarshal(entry.Value, &mapped); err != nil {
		return branchMapping{}, false, nil
	}
	return mapped, mapped.Number > 0, nil
}

func putCachedBranch(ctx context.Context, cache ports.SCMProviderCache, scope domain.SCMProviderCacheScope, branch string, mapped branchMapping, now time.Time) {
	b, _ := json.Marshal(mapped)
	_ = putProviderCache(ctx, cache, domain.SCMProviderCacheEntry{Key: domain.SCMProviderCacheKey{SCMProviderCacheScope: scope, Namespace: cacheBranchMap, Key: branch}, Value: b, UpdatedAt: now})
}

func getCachedReviewDetails(ctx context.Context, cache ports.SCMProviderCache, subj domain.SCMSubject) (reviewDetailsCache, domain.SCMProviderCacheEntry, bool) {
	key := domain.SCMProviderCacheKey{SCMProviderCacheScope: subj.CacheScope(), Namespace: cacheReviewDetails, Key: strconv.Itoa(subj.PRNumber)}
	entry, ok, err := cache.GetProviderCache(ctx, key)
	if err != nil || !ok {
		return reviewDetailsCache{}, domain.SCMProviderCacheEntry{}, false
	}
	var cached reviewDetailsCache
	if len(entry.Value) > 0 {
		if err := json.Unmarshal(entry.Value, &cached); err != nil {
			return reviewDetailsCache{}, domain.SCMProviderCacheEntry{}, false
		}
	}
	return cached, entry, true
}

func putCachedReviewDetails(ctx context.Context, cache ports.SCMProviderCache, subj domain.SCMSubject, decision string, threads []domain.SCMReviewThread, now time.Time) {
	b, _ := json.Marshal(reviewDetailsCache{Decision: decision, Threads: cloneReviewThreads(threads)})
	_ = putProviderCache(ctx, cache, domain.SCMProviderCacheEntry{Key: domain.SCMProviderCacheKey{SCMProviderCacheScope: subj.CacheScope(), Namespace: cacheReviewDetails, Key: strconv.Itoa(subj.PRNumber)}, Value: b, UpdatedAt: now})
}

func touchCachedReviewDetails(ctx context.Context, cache ports.SCMProviderCache, entry domain.SCMProviderCacheEntry, now time.Time) {
	entry.UpdatedAt = now
	_ = putProviderCache(ctx, cache, entry)
}

func prepareRESTCache(ctx context.Context, cache ports.SCMProviderCache, key domain.SCMProviderCacheKey, entry domain.SCMProviderCacheEntry, hasEntry bool, resp RESTResponse, now time.Time) ([]byte, restCacheCommit) {
	if resp.NotModified {
		if hasEntry {
			if resp.ETag != "" && resp.ETag != entry.ETag {
				entry.ETag = resp.ETag
				entry.UpdatedAt = now
				_ = putProviderCache(ctx, cache, entry)
			}
			return entry.Value, nil
		}
		return resp.Body, nil
	}
	pending := domain.SCMProviderCacheEntry{Key: key, ETag: resp.ETag, Value: append([]byte(nil), resp.Body...), UpdatedAt: now}
	return resp.Body, func() error { return putProviderCache(ctx, cache, pending) }
}

func commitRESTCache(commit restCacheCommit) {
	if commit != nil {
		_ = commit()
	}
}

func putProviderCache(ctx context.Context, cache ports.SCMProviderCache, entry domain.SCMProviderCacheEntry) error {
	entry.MaxEntries = githubCacheCap(entry.Key.Namespace)
	return cache.PutProviderCache(ctx, entry)
}

func githubCacheCap(namespace string) int {
	switch namespace {
	case cachePRList:
		return cacheCapPRList
	case cacheChecks:
		return cacheCapChecks
	case cacheReviews:
		return cacheCapReviews
	case cacheReviewDetails:
		return cacheCapReviewDetails
	case cacheBranchMap:
		return cacheCapBranchMap
	case cacheCheckGuard:
		return cacheCapCheckGuard
	case cachePRState:
		return cacheCapPRState
	default:
		return 0
	}
}

func findPullForBranch(pulls []restPull, branch string) (branchMapping, bool) {
	for _, pr := range pulls {
		if pr.Head.Ref == branch {
			return branchMapping{Number: pr.Number, URL: pr.HTMLURL, Branch: pr.Head.Ref, BaseBranch: pr.Base.Ref}, true
		}
	}
	return branchMapping{}, false
}

func unavailableSnapshot(subj domain.SCMSubject, now time.Time, err error) domain.SCMSnapshot {
	d := domain.SCMDiagnostic{Operation: "github.observe", ErrorKind: domain.SCMErrorUnavailable, Message: fmt.Sprint(err)}
	if se, ok := err.(*domain.SCMError); ok {
		d.Operation = se.Operation
		d.ErrorKind = se.Kind
		d.StatusCode = se.StatusCode
	}
	return domain.SCMSnapshot{SessionID: subj.SessionID, Subject: subj, Freshness: domain.SCMFreshnessUnavailable, ObservedAt: now, Diagnostics: []domain.SCMDiagnostic{d}}
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
