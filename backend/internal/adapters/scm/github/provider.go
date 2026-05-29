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
	cachePRList     = "pr-list"
	cacheBranchMap  = "branch-map"
	cacheChecks     = "checks"
	cacheReviews    = "reviews"
	cacheCheckGuard = "checks-guard"

	maxGraphQLBatchSize      = 25
	graphQLCheckContextLimit = 20

	cacheCapPRList     = 100
	cacheCapChecks     = 500
	cacheCapReviews    = 500
	cacheCapBranchMap  = 1000
	cacheCapCheckGuard = 500
)

type Provider struct {
	client *Client
	host   string
}

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
		updated, err := p.discover(ctx, subjects, cache, now)
		if err != nil && firstErr == nil {
			firstErr = err
		}
		res.Subjects = append(res.Subjects, updated...)
		snaps, diags, rl, err := p.observeKnownPRs(ctx, updated, cache, now)
		res.Snapshots = append(res.Snapshots, snaps...)
		res.Diagnostics = append(res.Diagnostics, diags...)
		if rl != nil {
			res.RateLimit = rl
		}
		if err != nil && firstErr == nil {
			firstErr = err
		}
		for _, subj := range updated {
			key := domain.SCMPollStateKey{Provider: subj.Provider, Host: subj.Host, Repo: subj.Repo}
			st := domain.SCMPollState{Key: key}
			if err != nil {
				st.ConsecutiveFail = 1
				st.LastFailureAt = now
				st.BackoffUntil = now.Add(30 * time.Second)
				if se, ok := err.(*domain.SCMError); ok {
					st.LastError = se
					if se.Kind == domain.SCMErrorRateLimited && !se.RetryAfter.IsZero() {
						st.RateLimitUntil = se.RetryAfter
					}
				}
			} else {
				st.LastSuccessAt = now
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
	pulls, _, _, err := p.fetchOpenPulls(ctx, cache, scope, owner, repo, now)
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
		}
	}
	return out, nil
}

func (p *Provider) fetchOpenPulls(ctx context.Context, cache ports.SCMProviderCache, scope domain.SCMProviderCacheScope, owner, repo string, now time.Time) ([]restPull, bool, domain.SCMDiagnostic, error) {
	cacheKey := domain.SCMProviderCacheKey{SCMProviderCacheScope: scope, Namespace: cachePRList, Key: "open"}
	entry, hasEntry, _ := cache.GetProviderCache(ctx, cacheKey)
	q := url.Values{"state": []string{"open"}, "sort": []string{"updated"}, "direction": []string{"desc"}, "per_page": []string{"100"}}
	resp, err := p.client.DoREST(ctx, http.MethodGet, repoPath(owner, repo, "pulls"), q, nil, entry.ETag, "github.pr_list")
	if err != nil {
		return nil, true, resp.Diagnostic, err
	}
	var body []byte
	changed := !resp.NotModified
	if resp.NotModified && hasEntry {
		body = entry.Value
	} else {
		body = resp.Body
		_ = putProviderCache(ctx, cache, domain.SCMProviderCacheEntry{Key: cacheKey, ETag: resp.ETag, Value: append([]byte(nil), resp.Body...), UpdatedAt: now})
	}
	var pulls []restPull
	if err := json.Unmarshal(body, &pulls); err != nil {
		return nil, changed, resp.Diagnostic, &domain.SCMError{Kind: domain.SCMErrorParse, Operation: "github.pr_list", Message: err.Error(), Cause: err}
	}
	return pulls, changed, resp.Diagnostic, nil
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

func (p *Provider) checkRunsChanged(ctx context.Context, cache ports.SCMProviderCache, subj domain.SCMSubject, headSHA string, now time.Time) (bool, domain.SCMDiagnostic, error) {
	if headSHA == "" {
		return true, domain.SCMDiagnostic{}, nil
	}
	scope := subj.CacheScope()
	key := domain.SCMProviderCacheKey{SCMProviderCacheScope: scope, Namespace: cacheCheckGuard, Key: headSHA}
	entry, _, _ := cache.GetProviderCache(ctx, key)
	owner, repo := subj.Repository().OwnerName()
	q := url.Values{"per_page": []string{"1"}}
	resp, err := p.client.DoREST(ctx, http.MethodGet, repoPath(owner, repo, "commits", headSHA, "check-runs"), q, nil, entry.ETag, "github.check_runs_guard")
	if err != nil {
		return true, resp.Diagnostic, err
	}
	if !resp.NotModified {
		_ = putProviderCache(ctx, cache, domain.SCMProviderCacheEntry{Key: key, ETag: resp.ETag, Value: append([]byte(nil), resp.Body...), UpdatedAt: now})
	}
	return !resp.NotModified, resp.Diagnostic, nil
}

func (p *Provider) reviewCommentsChanged(ctx context.Context, cache ports.SCMProviderCache, subj domain.SCMSubject, now time.Time) (bool, domain.SCMDiagnostic, error) {
	if subj.PRNumber == 0 {
		return false, domain.SCMDiagnostic{}, nil
	}
	scope := subj.CacheScope()
	key := domain.SCMProviderCacheKey{SCMProviderCacheScope: scope, Namespace: cacheReviews, Key: strconv.Itoa(subj.PRNumber)}
	entry, _, _ := cache.GetProviderCache(ctx, key)
	owner, repo := subj.Repository().OwnerName()
	resp, err := p.client.DoREST(ctx, http.MethodGet, repoPath(owner, repo, "pulls", strconv.Itoa(subj.PRNumber), "comments"), nil, nil, entry.ETag, "github.review_comments_guard")
	if err != nil {
		return true, resp.Diagnostic, err
	}
	if !resp.NotModified {
		_ = putProviderCache(ctx, cache, domain.SCMProviderCacheEntry{Key: key, ETag: resp.ETag, Value: append([]byte(nil), resp.Body...), UpdatedAt: now})
	}
	return !resp.NotModified, resp.Diagnostic, nil
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
	return snap.CI.Summary == "failing" || checkContextsIncomplete(prData)
}

func checkContextsIncomplete(pr map[string]any) bool {
	roll := statusRollup(pr)
	contexts, _ := roll["contexts"].(map[string]any)
	pageInfo, _ := contexts["pageInfo"].(map[string]any)
	return boolv(pageInfo["hasNextPage"])
}

type snapshotReader interface {
	GetLatestSnapshot(ctx context.Context, sessionID domain.SessionID) (domain.SCMSnapshot, bool, error)
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
	diags := []domain.SCMDiagnostic{}
	if len(latest) > 0 {
		scope := known[0].CacheScope()
		_, changed, prListDiag, err := p.fetchOpenPulls(ctx, cache, scope, owner, repo, now)
		prListChanged = changed
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
	if !prListChanged && len(latest) > 0 {
		toFetch = toFetch[:0]
		for _, subj := range known {
			prev, ok := latest[subj.SessionID]
			if !ok || prev.PR == nil || prev.PR.HeadSHA == "" {
				toFetch = append(toFetch, subj)
				continue
			}
			changed, diag, err := p.checkRunsChanged(ctx, cache, subj, prev.PR.HeadSHA, now)
			if diag.Operation != "" {
				diags = append(diags, diag)
			}
			if err != nil || changed {
				toFetch = append(toFetch, subj)
				continue
			}
			reviewChanged, diag, err := p.reviewCommentsChanged(ctx, cache, subj, now)
			if diag.Operation != "" {
				diags = append(diags, diag)
			}
			if err != nil || reviewChanged {
				toFetch = append(toFetch, subj)
				continue
			}
			reused := prev
			reused.ObservedAt = now
			reused.Freshness = domain.SCMFreshnessUnchanged
			reused.Subject = subj
			snaps = append(snaps, reused)
		}
	}
	if len(toFetch) == 0 {
		return snaps, diags, nil, nil
	}

	var firstErr error
	var lastRL *domain.SCMRateLimit
	for _, batch := range chunkSubjects(toFetch, maxGraphQLBatchSize) {
		data, rl, diag, err := p.fetchPRBatch(ctx, owner, repo, batch)
		if diag.Operation != "" {
			diags = append(diags, diag)
		}
		if rl != nil {
			lastRL = rl
		}
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			for _, subj := range batch {
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
				if err == nil && len(checks) > 0 {
					snap.CI.Checks = checks
					snap.CI.Summary = summarizeChecks(checks)
					snap.CI.FailureLogTail = combinedFailureTail(checks)
				} else if err != nil && havePrev && prev.PR != nil && prev.PR.HeadSHA == snap.PR.HeadSHA {
					snap.CI = prev.CI
					snap.Diagnostics = append(snap.Diagnostics, diagnosticFromError("github.check_runs", err))
				}
			}
			reviewThreads, reviewDiag, reviewChanged, err := p.fetchReviewComments(ctx, cache, subj, now)
			if reviewDiag.Operation != "" {
				snap.Diagnostics = append(snap.Diagnostics, reviewDiag)
			}
			switch {
			case err == nil && !reviewChanged && havePrev && len(prev.Review.UnresolvedThreads) > 0:
				snap.Review.UnresolvedThreads = prev.Review.UnresolvedThreads
				classifyThreads(&snap.Review)
			case err == nil && len(reviewThreads) > 0:
				snap.Review.UnresolvedThreads = reviewThreads
				classifyThreads(&snap.Review)
			case err != nil && havePrev:
				snap.Review.UnresolvedThreads = prev.Review.UnresolvedThreads
				classifyThreads(&snap.Review)
				snap.Diagnostics = append(snap.Diagnostics, diagnosticFromError("github.review_comments", err))
			}
			finalizeMergeability(&snap)
			snaps = append(snaps, snap)
		}
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
		th := domain.SCMReviewThread{ID: str(n["id"]), Path: str(n["path"]), Line: int(num(n["line"]))}
		comments, _ := n["comments"].(map[string]any)
		for _, cn := range nodes(comments["nodes"]) {
			author, _ := cn["author"].(map[string]any)
			login := str(author["login"])
			isBot := isBotAuthor(login, str(author["__typename"]))
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
	m.NoConflicts = !m.Conflict
	m.BehindBase = mergeState == "BEHIND"
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
	if mergeState == "BLOCKED" {
		m.Blockers = append(m.Blockers, "merge blocked")
	}
	m.Mergeable = raw == "MERGEABLE" && m.NoConflicts && !m.BehindBase && m.CIPassing && m.Approved && len(m.Blockers) == 0
	return m
}

func finalizeMergeability(s *domain.SCMSnapshot) {
	if s.PR == nil || s.PR.State == domain.PRMerged {
		return
	}
	if s.Mergeability.CIPassing == false && (s.CI.Summary == "passing" || s.CI.Summary == "none" || s.CI.Summary == "") {
		s.Mergeability.CIPassing = true
	}
	if s.Mergeability.Approved == false && (s.Review.Decision == "approved" || s.Review.Decision == "none" || s.Review.Decision == "") {
		s.Mergeability.Approved = true
	}
	if s.Mergeability.RawState == "" && !s.Mergeability.Conflict {
		s.Mergeability.NoConflicts = true
	}
	s.Mergeability.Mergeable = s.Mergeability.Mergeable || (s.Mergeability.NoConflicts && !s.Mergeability.BehindBase && s.Mergeability.CIPassing && s.Mergeability.Approved && len(s.Mergeability.Blockers) == 0)
}

func (p *Provider) fetchCheckRuns(ctx context.Context, cache ports.SCMProviderCache, subj domain.SCMSubject, snap domain.SCMSnapshot, now time.Time) ([]domain.SCMCheck, domain.SCMDiagnostic, error) {
	if snap.PR == nil || snap.PR.HeadSHA == "" {
		return nil, domain.SCMDiagnostic{}, nil
	}
	scope := subj.CacheScope()
	key := domain.SCMProviderCacheKey{SCMProviderCacheScope: scope, Namespace: cacheChecks, Key: snap.PR.HeadSHA}
	entry, hasEntry, _ := cache.GetProviderCache(ctx, key)
	owner, repo := subj.Repository().OwnerName()
	resp, err := p.client.DoREST(ctx, http.MethodGet, repoPath(owner, repo, "commits", snap.PR.HeadSHA, "check-runs"), nil, nil, entry.ETag, "github.check_runs")
	if err != nil {
		return nil, resp.Diagnostic, err
	}
	body := resp.Body
	if resp.NotModified && hasEntry {
		body = entry.Value
	} else {
		_ = putProviderCache(ctx, cache, domain.SCMProviderCacheEntry{Key: key, ETag: resp.ETag, Value: append([]byte(nil), resp.Body...), UpdatedAt: now})
	}
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
	checks := make([]domain.SCMCheck, 0, len(decoded.CheckRuns))
	for _, r := range decoded.CheckRuns {
		checks = append(checks, domain.SCMCheck{Name: r.Name, Status: r.Status, Conclusion: r.Conclusion, URL: firstNonEmpty(r.HTMLURL, r.DetailsURL), Details: firstNonEmpty(r.Output.Title, r.Output.Summary), LogTail: tailLines(firstNonEmpty(r.Output.Text, r.Output.Summary), 120)})
	}
	return checks, resp.Diagnostic, nil
}

func (p *Provider) fetchReviewComments(ctx context.Context, cache ports.SCMProviderCache, subj domain.SCMSubject, now time.Time) ([]domain.SCMReviewThread, domain.SCMDiagnostic, bool, error) {
	if subj.PRNumber == 0 {
		return nil, domain.SCMDiagnostic{}, false, nil
	}
	scope := subj.CacheScope()
	key := domain.SCMProviderCacheKey{SCMProviderCacheScope: scope, Namespace: cacheReviews, Key: strconv.Itoa(subj.PRNumber)}
	entry, hasEntry, _ := cache.GetProviderCache(ctx, key)
	owner, repo := subj.Repository().OwnerName()
	resp, err := p.client.DoREST(ctx, http.MethodGet, repoPath(owner, repo, "pulls", strconv.Itoa(subj.PRNumber), "comments"), nil, nil, entry.ETag, "github.review_comments")
	if err != nil {
		return nil, resp.Diagnostic, false, err
	}
	body := resp.Body
	if resp.NotModified && hasEntry {
		body = entry.Value
	} else {
		_ = putProviderCache(ctx, cache, domain.SCMProviderCacheEntry{Key: key, ETag: resp.ETag, Value: append([]byte(nil), resp.Body...), UpdatedAt: now})
	}
	var comments []struct {
		ID                  int64  `json:"id"`
		Body                string `json:"body"`
		HTMLURL             string `json:"html_url"`
		Path                string `json:"path"`
		Line                int    `json:"line"`
		OriginalLine        int    `json:"original_line"`
		PullRequestReviewID int64  `json:"pull_request_review_id"`
		User                struct {
			Login string `json:"login"`
			Type  string `json:"type"`
		} `json:"user"`
	}
	if err := json.Unmarshal(body, &comments); err != nil {
		return nil, resp.Diagnostic, false, &domain.SCMError{Kind: domain.SCMErrorParse, Operation: "github.review_comments", Message: err.Error(), Cause: err}
	}
	threads := make([]domain.SCMReviewThread, 0, len(comments))
	for _, c := range comments {
		line := c.Line
		if line == 0 {
			line = c.OriginalLine
		}
		threadID := fmt.Sprintf("review-%d-comment-%d", c.PullRequestReviewID, c.ID)
		isBot := isBotAuthor(c.User.Login, c.User.Type)
		comment := domain.SCMReviewComment{ID: strconv.FormatInt(c.ID, 10), Author: c.User.Login, Body: c.Body, URL: c.HTMLURL, IsBot: isBot, Path: c.Path, Line: line, ThreadID: threadID}
		threads = append(threads, domain.SCMReviewThread{ID: threadID, Path: c.Path, Line: line, URL: c.HTMLURL, IsBot: isBot, Comments: []domain.SCMReviewComment{comment}})
	}
	return threads, resp.Diagnostic, !resp.NotModified, nil
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
	for _, c := range checks {
		if failedCheck(c) {
			return "failing"
		}
		if c.Conclusion == "" || strings.EqualFold(c.Status, "queued") || strings.EqualFold(c.Status, "in_progress") || strings.EqualFold(c.Status, "pending") {
			pending = true
		}
	}
	if pending {
		return "pending"
	}
	return "passing"
}

func failedCheck(c domain.SCMCheck) bool {
	s := strings.ToLower(firstNonEmpty(c.Conclusion, c.Status))
	return s == "failure" || s == "failed" || s == "error" || s == "timed_out" || s == "cancelled" || s == "action_required"
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
	lines := strings.Split(strings.TrimSpace(s), "\n")
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
	return typ == "bot" || strings.Contains(login, "[bot]") || strings.HasSuffix(login, "-bot") || strings.Contains(login, "bot")
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
	case cacheBranchMap:
		return cacheCapBranchMap
	case cacheCheckGuard:
		return cacheCapCheckGuard
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
