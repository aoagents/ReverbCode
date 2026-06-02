// Package scm implements the provider-neutral SCM polling observer. It owns the
// polling loop, ETag/cache checks, semantic diffing, DB persistence, and
// lifecycle notification; provider adapters only normalize provider-specific
// APIs into ports.SCMObservation values.
package scm

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

const (
	// DefaultTickInterval is the SCM observer's PR/CI polling cadence.
	DefaultTickInterval = 30 * time.Second
	// DefaultReviewInterval is the minimum interval between review-thread polls
	// for a PR whose review state warrants thread refresh.
	DefaultReviewInterval = 2 * time.Minute
	// DefaultCacheMax bounds each in-memory ETag/review cache map.
	DefaultCacheMax = 512
	// BatchSize is the maximum number of PRs in one provider batch fetch.
	BatchSize = 25
)

// Provider is the normalized SCM provider contract used by the observer.
type Provider interface {
	ParseRepository(remote string) (ports.SCMRepo, bool)
	RepoPRListGuard(ctx context.Context, repo ports.SCMRepo, etag string) (ports.SCMGuardResult, error)
	DetectPRByBranch(ctx context.Context, repo ports.SCMRepo, branch string) (ports.SCMPRObservation, error)
	CommitChecksGuard(ctx context.Context, repo ports.SCMRepo, headSHA, etag string) (ports.SCMGuardResult, error)
	FetchPullRequests(ctx context.Context, refs []ports.SCMPRRef) ([]ports.SCMObservation, error)
	FetchFailedCheckLogTail(ctx context.Context, repo ports.SCMRepo, check ports.SCMCheckObservation) (string, error)
	FetchReviewThreads(ctx context.Context, ref ports.SCMPRRef) (ports.SCMReviewObservation, error)
}

// Store is the persistence contract the observer needs for discovery, local
// hash reads, and transactional SCM writes.
type Store interface {
	ListAllSessions(ctx context.Context) ([]domain.SessionRecord, error)
	GetProject(ctx context.Context, id string) (domain.ProjectRecord, bool, error)
	ListPRsBySession(ctx context.Context, sessionID domain.SessionID) ([]domain.PullRequest, error)
	ListChecks(ctx context.Context, prURL string) ([]domain.PullRequestCheck, error)
	WriteSCMObservation(ctx context.Context, pr domain.PullRequest, checks []domain.PullRequestCheck, threads []domain.PullRequestReviewThread, comments []domain.PullRequestComment, replaceReview bool) error
}

// Lifecycle is the provider-neutral lifecycle notification sink.
type Lifecycle interface {
	ApplySCMObservation(ctx context.Context, sessionID domain.SessionID, obs ports.SCMObservation) error
}

// Config holds optional observer knobs. Zero values use production defaults.
type Config struct {
	Tick           time.Duration
	ReviewInterval time.Duration
	Clock          func() time.Time
	Logger         *slog.Logger
	CacheMax       int
}

// ObserverCache stores provider ETags and review polling timestamps in memory.
// It is intentionally non-persistent for v1; cold restarts simply revalidate.
type ObserverCache struct {
	RepoPRListETag      map[string]string
	CommitChecksETag    map[string]string
	ReviewETag          map[string]string
	LastReviewPollAt    map[string]time.Time
	repoOrder           []string
	commitOrder         []string
	lastReviewPollOrder []string
	max                 int
}

func newCache(maxEntries int) ObserverCache {
	if maxEntries <= 0 {
		maxEntries = DefaultCacheMax
	}
	return ObserverCache{
		RepoPRListETag:   map[string]string{},
		CommitChecksETag: map[string]string{},
		ReviewETag:       map[string]string{},
		LastReviewPollAt: map[string]time.Time{},
		max:              maxEntries,
	}
}

// Observer coordinates provider polling, semantic diffing, persistence, and
// lifecycle notifications for SCM observations.
type Observer struct {
	provider       Provider
	store          Store
	lifecycle      Lifecycle
	tick           time.Duration
	reviewInterval time.Duration
	clock          func() time.Time
	logger         *slog.Logger
	Cache          ObserverCache
}

// New constructs an Observer with default cadence/cache settings for zero
// values in cfg.
func New(provider Provider, store Store, lifecycle Lifecycle, cfg Config) *Observer {
	o := &Observer{provider: provider, store: store, lifecycle: lifecycle, tick: cfg.Tick, reviewInterval: cfg.ReviewInterval, clock: cfg.Clock, logger: cfg.Logger, Cache: newCache(cfg.CacheMax)}
	if o.tick <= 0 {
		o.tick = DefaultTickInterval
	}
	if o.reviewInterval <= 0 {
		o.reviewInterval = DefaultReviewInterval
	}
	if o.clock == nil {
		o.clock = time.Now
	}
	if o.logger == nil {
		o.logger = slog.Default()
	}
	return o
}

// Start launches the observer loop. The first Poll runs immediately inside the
// goroutine so daemon startup is not blocked; subsequent polls run on the tick.
func (o *Observer) Start(ctx context.Context) <-chan struct{} {
	done := make(chan struct{})
	go o.loop(ctx, done)
	return done
}

func (o *Observer) loop(ctx context.Context, done chan<- struct{}) {
	defer close(done)
	if err := o.Poll(ctx); err != nil && !errors.Is(err, context.Canceled) {
		o.logger.Error("scm observer: initial poll failed", "err", err)
	}
	t := time.NewTicker(o.tick)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if err := o.Poll(ctx); err != nil && !errors.Is(err, context.Canceled) {
				o.logger.Error("scm observer: poll failed", "err", err)
			}
		}
	}
}

type subject struct {
	session       domain.SessionRecord
	project       domain.ProjectRecord
	repo          ports.SCMRepo
	branch        string
	known         domain.PullRequest
	hasPR         bool
	newlyDetected bool
}

type repoGuardState struct {
	result  ports.SCMGuardResult
	hadETag bool
	err     error
}

// Poll runs one synchronous SCM observation cycle.
func (o *Observer) Poll(ctx context.Context) error {
	now := o.clock().UTC()
	if err := ctx.Err(); err != nil {
		return err
	}
	subjects, err := o.discoverSubjects(ctx)
	if err != nil {
		return err
	}
	if len(subjects) == 0 {
		return nil
	}

	repoGuards := o.guardRepos(ctx, subjects)
	if err := ctx.Err(); err != nil {
		return err
	}
	o.detectMissingPRs(ctx, subjects, repoGuards, now)
	if err := ctx.Err(); err != nil {
		return err
	}

	refs, subjectsByPR := o.selectRefreshCandidates(ctx, subjects, repoGuards)
	observations := map[string]ports.SCMObservation{}
	for _, chunk := range chunks(refs, BatchSize) {
		if err := ctx.Err(); err != nil {
			return err
		}
		batch, err := o.provider.FetchPullRequests(ctx, chunk)
		if err != nil {
			o.logger.Error("scm observer: GraphQL PR batch failed", "err", err)
			continue
		}
		for _, obs := range batch {
			obs.ObservedAt = now
			key := prKeyFromObs(obs)
			if key == "" {
				continue
			}
			observations[key] = obs
		}
	}

	for key, subj := range subjectsByPR {
		if err := ctx.Err(); err != nil {
			return err
		}
		obs, ok := observations[key]
		if !ok {
			continue
		}
		local := subj.known
		o.enrichFailureLogs(ctx, &obs, local)
		observations[key] = obs
	}

	reviewRefreshed := map[string]bool{}
	o.refreshReviews(ctx, subjects, observations, subjectsByPR, reviewRefreshed, now)
	if err := ctx.Err(); err != nil {
		return err
	}

	for key, obs := range observations {
		if err := ctx.Err(); err != nil {
			return err
		}
		subj, ok := subjectsByPR[key]
		if !ok {
			continue
		}
		local := subj.known
		prepared := o.prepareForPersistence(obs, local, reviewRefreshed[key], now)
		if !prepared.Changed.Metadata && !prepared.Changed.CI && !prepared.Changed.Review {
			continue
		}
		pr, checks, threads, comments := domainFromObservation(subj.session.ID, prepared, local, reviewRefreshed[key], now)
		if err := o.store.WriteSCMObservation(ctx, pr, checks, threads, comments, reviewRefreshed[key]); err != nil {
			o.logger.Error("scm observer: DB write failed", "session", subj.session.ID, "pr", pr.URL, "err", err)
			continue
		}
		if o.lifecycle != nil {
			if err := o.lifecycle.ApplySCMObservation(ctx, subj.session.ID, prepared); err != nil {
				o.logger.Error("scm observer: lifecycle notification failed", "session", subj.session.ID, "pr", pr.URL, "err", err)
			}
		}
	}
	return nil
}

func (o *Observer) discoverSubjects(ctx context.Context) (map[string]*subject, error) {
	sessions, err := o.store.ListAllSessions(ctx)
	if err != nil {
		return nil, err
	}
	projects := map[domain.ProjectID]domain.ProjectRecord{}
	out := map[string]*subject{}
	for _, sess := range sessions {
		if sess.IsTerminated {
			continue
		}
		branch := strings.TrimSpace(sess.Metadata.Branch)
		if branch == "" {
			continue
		}
		proj, ok := projects[sess.ProjectID]
		if !ok {
			p, found, err := o.store.GetProject(ctx, string(sess.ProjectID))
			if err != nil {
				return nil, err
			}
			if !found || !p.ArchivedAt.IsZero() {
				continue
			}
			projects[sess.ProjectID] = p
			proj = p
		}
		repo, ok := o.provider.ParseRepository(proj.RepoOriginURL)
		if !ok {
			o.logger.Debug("scm observer: project has no supported SCM origin", "project", proj.ID, "origin", proj.RepoOriginURL)
			continue
		}
		prs, err := o.store.ListPRsBySession(ctx, sess.ID)
		if err != nil {
			return nil, err
		}
		known, hasPR := chooseKnownPR(prs)
		s := &subject{session: sess, project: proj, repo: repo, branch: branch, known: known, hasPR: hasPR}
		if hasPR && known.Number > 0 {
			out[prKey(repo, known.Number)] = s
		} else {
			out["session:"+string(sess.ID)] = s
		}
	}
	return out, nil
}

func chooseKnownPR(prs []domain.PullRequest) (domain.PullRequest, bool) {
	if len(prs) == 0 {
		return domain.PullRequest{}, false
	}
	for _, pr := range prs {
		if pr.Number > 0 && !pr.Merged && !pr.Closed {
			return pr, true
		}
	}
	for _, pr := range prs {
		if pr.Number > 0 {
			return pr, true
		}
	}
	return domain.PullRequest{}, false
}

func (o *Observer) guardRepos(ctx context.Context, subjects map[string]*subject) map[string]repoGuardState {
	repos := map[string]ports.SCMRepo{}
	for _, s := range subjects {
		repos[prKey(s.repo, 0)] = s.repo
	}
	out := map[string]repoGuardState{}
	for key, repo := range repos {
		prev, had := o.Cache.RepoPRListETag[key]
		res, err := o.provider.RepoPRListGuard(ctx, repo, prev)
		if err != nil {
			o.logger.Error("scm observer: repo PR-list guard failed", "repo", repoFullName(repo), "err", err)
			out[key] = repoGuardState{hadETag: had, err: err}
			continue
		}
		if res.ETag != "" {
			o.cacheSetString(o.Cache.RepoPRListETag, &o.Cache.repoOrder, key, res.ETag)
		}
		out[key] = repoGuardState{result: res, hadETag: had}
	}
	return out
}

func (o *Observer) detectMissingPRs(ctx context.Context, subjects map[string]*subject, guards map[string]repoGuardState, now time.Time) {
	for oldKey, s := range subjects {
		if s.hasPR {
			continue
		}
		g := guards[prKey(s.repo, 0)]
		if g.err != nil {
			continue
		}
		if g.result.NotModified && g.hadETag {
			continue
		}
		pr, err := o.provider.DetectPRByBranch(ctx, s.repo, s.branch)
		if err != nil {
			o.logger.Debug("scm observer: no PR detected for branch", "session", s.session.ID, "branch", s.branch, "err", err)
			continue
		}
		if pr.Number <= 0 {
			continue
		}
		s.known = domain.PullRequest{URL: pr.URL, SessionID: s.session.ID, Number: pr.Number, SourceBranch: pr.SourceBranch, TargetBranch: pr.TargetBranch, HeadSHA: pr.HeadSHA, Provider: s.repo.Provider, Host: s.repo.Host, Repo: repoFullName(s.repo), UpdatedAt: now}
		s.hasPR = true
		s.newlyDetected = true
		delete(subjects, oldKey)
		subjects[prKey(s.repo, pr.Number)] = s
	}
}

func (o *Observer) selectRefreshCandidates(ctx context.Context, subjects map[string]*subject, guards map[string]repoGuardState) ([]ports.SCMPRRef, map[string]*subject) {
	var refs []ports.SCMPRRef
	subjectsByPR := map[string]*subject{}
	seen := map[string]bool{}
	for _, s := range subjects {
		if !s.hasPR || s.known.Number <= 0 {
			continue
		}
		key := prKey(s.repo, s.known.Number)
		subjectsByPR[key] = s
		candidate := s.newlyDetected || missingLocalState(s.known)
		g := guards[prKey(s.repo, 0)]
		if g.err == nil && !g.result.NotModified {
			candidate = true
		}
		if s.known.HeadSHA != "" {
			commitKey := commitKey(s.repo, s.known.HeadSHA)
			prev := o.Cache.CommitChecksETag[commitKey]
			res, err := o.provider.CommitChecksGuard(ctx, s.repo, s.known.HeadSHA, prev)
			if err != nil {
				o.logger.Error("scm observer: commit check-runs guard failed", "pr", s.known.URL, "sha", s.known.HeadSHA, "err", err)
			} else {
				if res.ETag != "" {
					o.cacheSetString(o.Cache.CommitChecksETag, &o.Cache.commitOrder, commitKey, res.ETag)
				}
				if !res.NotModified {
					candidate = true
				}
			}
		}
		if candidate && !seen[key] {
			refs = append(refs, ports.SCMPRRef{Repo: s.repo, Number: s.known.Number, URL: s.known.URL})
			seen[key] = true
		}
	}
	return refs, subjectsByPR
}

func missingLocalState(pr domain.PullRequest) bool {
	return pr.URL == "" || pr.HeadSHA == "" || pr.MetadataHash == "" || pr.CIHash == ""
}

func (o *Observer) enrichFailureLogs(ctx context.Context, obs *ports.SCMObservation, local domain.PullRequest) {
	if obs.CI.Summary != string(domain.CIFailing) || obs.CI.FailedFingerprint == "" {
		return
	}
	if strings.HasPrefix(local.CIHash, obs.CI.FailedFingerprint+":") {
		checks, err := o.store.ListChecks(ctx, local.URL)
		if err == nil && applyStoredFailedLogTails(obs, checks) {
			return
		}
	}
	tails := make([]string, 0, len(obs.CI.FailedChecks))
	for i := range obs.CI.FailedChecks {
		tail, err := o.provider.FetchFailedCheckLogTail(ctx, ports.SCMRepo{Provider: obs.Provider, Host: obs.Host, Repo: obs.Repo, Owner: ownerOf(obs.Repo), Name: nameOf(obs.Repo)}, obs.CI.FailedChecks[i])
		if err != nil {
			tail = "<log fetch failed: " + scrubLine(err.Error()) + ">"
		}
		obs.CI.FailedChecks[i].LogTail = tail
		if tail != "" {
			tails = append(tails, tail)
		}
		for j := range obs.CI.Checks {
			if obs.CI.Checks[j].Name == obs.CI.FailedChecks[i].Name && obs.CI.Checks[j].ProviderID == obs.CI.FailedChecks[i].ProviderID {
				obs.CI.Checks[j].LogTail = tail
			}
		}
	}
	obs.CI.FailureLogTail = strings.Join(tails, "\n---\n")
}

func applyStoredFailedLogTails(obs *ports.SCMObservation, checks []domain.PullRequestCheck) bool {
	tailsByName := map[string]string{}
	for _, ch := range checks {
		if ch.LogTail != "" && (ch.Status == domain.PRCheckFailed || ch.Status == domain.PRCheckCancelled) {
			tailsByName[ch.Name] = ch.LogTail
		}
	}
	if len(tailsByName) == 0 {
		return false
	}
	tails := make([]string, 0, len(obs.CI.FailedChecks))
	for i := range obs.CI.FailedChecks {
		tail := tailsByName[obs.CI.FailedChecks[i].Name]
		if tail == "" {
			return false
		}
		obs.CI.FailedChecks[i].LogTail = tail
		tails = append(tails, tail)
	}
	for i := range obs.CI.Checks {
		if tail := tailsByName[obs.CI.Checks[i].Name]; tail != "" {
			obs.CI.Checks[i].LogTail = tail
		}
	}
	obs.CI.FailureLogTail = strings.Join(tails, "\n---\n")
	return true
}

func (o *Observer) refreshReviews(ctx context.Context, subjects map[string]*subject, observations map[string]ports.SCMObservation, subjectsByPR map[string]*subject, reviewRefreshed map[string]bool, now time.Time) {
	for _, s := range subjects {
		if !s.hasPR || s.known.Number <= 0 {
			continue
		}
		pkey := prKey(s.repo, s.known.Number)
		obs, hasObs := observations[pkey]
		decision := string(s.known.Review)
		if hasObs && obs.Review.Decision != "" {
			decision = obs.Review.Decision
		}
		if !o.needsReviewRefresh(pkey, s.known, decision, hasObs, now) {
			continue
		}
		review, err := o.provider.FetchReviewThreads(ctx, ports.SCMPRRef{Repo: s.repo, Number: s.known.Number, URL: s.known.URL})
		if err != nil {
			o.logger.Error("scm observer: review refresh failed", "pr", s.known.URL, "err", err)
			continue
		}
		if !hasObs {
			obs = observationFromLocal(s.repo, s.known)
		}
		if review.Decision != "" {
			obs.Review.Decision = review.Decision
		}
		obs.Review.Threads = review.Threads
		obs.ObservedAt = now
		observations[pkey] = obs
		subjectsByPR[pkey] = s
		reviewRefreshed[pkey] = true
		o.cacheSetTime(o.Cache.LastReviewPollAt, &o.Cache.lastReviewPollOrder, pkey, now)
	}
}

func (o *Observer) needsReviewRefresh(key string, local domain.PullRequest, decision string, hasObs bool, now time.Time) bool {
	if local.ReviewHash == "" {
		return true
	}
	if decision == string(domain.ReviewChangesRequest) {
		last := o.Cache.LastReviewPollAt[key]
		return last.IsZero() || now.Sub(last) >= o.reviewInterval
	}
	if hasObs && decision != string(local.Review) {
		return true
	}
	if local.ReviewHash != "" && string(local.Review) == string(domain.ReviewChangesRequest) && decision != string(domain.ReviewChangesRequest) {
		return true
	}
	return false
}

func (o *Observer) prepareForPersistence(obs ports.SCMObservation, local domain.PullRequest, reviewFetched bool, now time.Time) ports.SCMObservation {
	metadataHash := metadataSemanticHash(obs)
	ciHash := ciSemanticHash(obs.CI)
	reviewHash := local.ReviewHash
	if reviewFetched || local.ReviewHash == "" || obs.Review.Decision != string(local.Review) {
		reviewHash = reviewSemanticHash(obs.Review)
	}
	obs.Changed = ports.SCMChanged{
		Metadata: metadataHash != local.MetadataHash,
		CI:       ciHash != local.CIHash,
		Review:   reviewHash != local.ReviewHash,
	}
	obs.PR.State = firstNonEmpty(obs.PR.State, normalizePRState(obs.PR.Draft, obs.PR.Merged, obs.PR.Closed))
	obs.ObservedAt = firstTime(obs.ObservedAt, now)
	return obs
}

func domainFromObservation(sessionID domain.SessionID, obs ports.SCMObservation, local domain.PullRequest, reviewFetched bool, now time.Time) (domain.PullRequest, []domain.PullRequestCheck, []domain.PullRequestReviewThread, []domain.PullRequestComment) {
	metadataHash := metadataSemanticHash(obs)
	ciHash := ciSemanticHash(obs.CI)
	reviewHash := reviewSemanticHash(obs.Review)
	reviewDecision := domain.ReviewDecision(firstNonEmpty(obs.Review.Decision, string(domain.ReviewNone)))
	if !reviewFetched && local.ReviewHash != "" && reviewDecision == local.Review {
		reviewHash = local.ReviewHash
	}
	reviewObservedAt := local.ReviewObservedAt
	if reviewFetched || reviewObservedAt.IsZero() {
		reviewObservedAt = obs.ObservedAt
	}
	pr := domain.PullRequest{
		URL:                      firstNonEmpty(obs.PR.URL, obs.PR.HTMLURL),
		SessionID:                sessionID,
		Number:                   obs.PR.Number,
		Draft:                    obs.PR.Draft,
		Merged:                   obs.PR.Merged,
		Closed:                   obs.PR.Closed,
		CI:                       domain.CIState(firstNonEmpty(obs.CI.Summary, string(domain.CIUnknown))),
		Review:                   reviewDecision,
		Mergeability:             domain.Mergeability(firstNonEmpty(obs.Mergeability.State, string(domain.MergeUnknown))),
		UpdatedAt:                now,
		Provider:                 obs.Provider,
		Host:                     obs.Host,
		Repo:                     obs.Repo,
		SourceBranch:             obs.PR.SourceBranch,
		TargetBranch:             obs.PR.TargetBranch,
		HeadSHA:                  obs.PR.HeadSHA,
		Title:                    obs.PR.Title,
		Additions:                obs.PR.Additions,
		Deletions:                obs.PR.Deletions,
		ChangedFiles:             obs.PR.ChangedFiles,
		Author:                   obs.PR.Author,
		BaseSHA:                  obs.PR.BaseSHA,
		MergeCommitSHA:           obs.PR.MergeCommitSHA,
		ProviderState:            obs.PR.ProviderState,
		ProviderMergeable:        obs.PR.ProviderMergeable,
		ProviderMergeStateStatus: obs.PR.ProviderMergeStateStatus,
		HTMLURL:                  obs.PR.HTMLURL,
		CreatedAtProvider:        obs.PR.CreatedAtProvider,
		UpdatedAtProvider:        obs.PR.UpdatedAtProvider,
		MergedAtProvider:         obs.PR.MergedAtProvider,
		ClosedAtProvider:         obs.PR.ClosedAtProvider,
		MetadataHash:             metadataHash,
		CIHash:                   ciHash,
		ReviewHash:               reviewHash,
		ObservedAt:               obs.ObservedAt,
		CIObservedAt:             obs.ObservedAt,
		ReviewObservedAt:         reviewObservedAt,
	}
	checks := make([]domain.PullRequestCheck, 0, len(obs.CI.Checks))
	for _, ch := range obs.CI.Checks {
		checks = append(checks, domain.PullRequestCheck{Name: ch.Name, CommitHash: obs.CI.HeadSHA, Status: domain.PRCheckStatus(ch.Status), Conclusion: ch.Conclusion, URL: ch.URL, Details: ch.ProviderID, LogTail: ch.LogTail, CreatedAt: now})
	}
	threads := make([]domain.PullRequestReviewThread, 0, len(obs.Review.Threads))
	commentCount := 0
	for _, th := range obs.Review.Threads {
		commentCount += len(th.Comments)
	}
	comments := make([]domain.PullRequestComment, 0, commentCount)
	for _, th := range obs.Review.Threads {
		threads = append(threads, domain.PullRequestReviewThread{ThreadID: th.ID, Path: th.Path, Line: th.Line, Resolved: th.Resolved, IsBot: th.IsBot, SemanticHash: threadSemanticHash(th), UpdatedAt: now})
		for _, c := range th.Comments {
			comments = append(comments, domain.PullRequestComment{ThreadID: th.ID, ID: c.ID, Author: c.Author, File: th.Path, Line: th.Line, Body: c.Body, URL: c.URL, Resolved: th.Resolved, IsBot: c.IsBot || th.IsBot, CreatedAt: now})
		}
	}
	return pr, checks, threads, comments
}

func observationFromLocal(repo ports.SCMRepo, pr domain.PullRequest) ports.SCMObservation {
	return ports.SCMObservation{
		Fetched:      true,
		Provider:     firstNonEmpty(pr.Provider, repo.Provider),
		Host:         firstNonEmpty(pr.Host, repo.Host),
		Repo:         firstNonEmpty(pr.Repo, repoFullName(repo)),
		PR:           ports.SCMPRObservation{URL: pr.URL, Number: pr.Number, State: normalizePRState(pr.Draft, pr.Merged, pr.Closed), Draft: pr.Draft, Merged: pr.Merged, Closed: pr.Closed, SourceBranch: pr.SourceBranch, TargetBranch: pr.TargetBranch, HeadSHA: pr.HeadSHA, Title: pr.Title, Additions: pr.Additions, Deletions: pr.Deletions, ChangedFiles: pr.ChangedFiles, Author: pr.Author, BaseSHA: pr.BaseSHA, MergeCommitSHA: pr.MergeCommitSHA, ProviderState: pr.ProviderState, ProviderMergeable: pr.ProviderMergeable, ProviderMergeStateStatus: pr.ProviderMergeStateStatus, HTMLURL: pr.HTMLURL, CreatedAtProvider: pr.CreatedAtProvider, UpdatedAtProvider: pr.UpdatedAtProvider, MergedAtProvider: pr.MergedAtProvider, ClosedAtProvider: pr.ClosedAtProvider},
		CI:           ports.SCMCIObservation{Summary: string(pr.CI), HeadSHA: pr.HeadSHA},
		Review:       ports.SCMReviewObservation{Decision: string(pr.Review)},
		Mergeability: ports.SCMMergeabilityObservation{State: string(pr.Mergeability)},
	}
}

func chunks[T any](in []T, n int) [][]T {
	if n <= 0 || len(in) == 0 {
		return nil
	}
	out := make([][]T, 0, (len(in)+n-1)/n)
	for len(in) > 0 {
		end := n
		if len(in) < end {
			end = len(in)
		}
		out = append(out, in[:end])
		in = in[end:]
	}
	return out
}

func metadataSemanticHash(obs ports.SCMObservation) string {
	return stableHash(map[string]any{"provider": obs.Provider, "host": obs.Host, "repo": obs.Repo, "pr": obs.PR, "mergeability": obs.Mergeability})
}

func ciSemanticHash(ci ports.SCMCIObservation) string {
	h := stableHash(map[string]any{"summary": ci.Summary, "head": ci.HeadSHA, "checks": ci.Checks, "failed": ci.FailedChecks, "tail": ci.FailureLogTail})
	if ci.FailedFingerprint != "" {
		return ci.FailedFingerprint + ":" + h
	}
	return h
}

func reviewSemanticHash(review ports.SCMReviewObservation) string {
	return stableHash(review)
}

func threadSemanticHash(th ports.SCMReviewThreadObservation) string {
	return stableHash(th)
}

func stableHash(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		b = []byte(fmt.Sprintf("%#v", v))
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func prKeyFromObs(obs ports.SCMObservation) string {
	if obs.Repo == "" || obs.PR.Number <= 0 {
		return ""
	}
	return obs.Provider + ":" + obs.Host + ":" + obs.Repo + "#" + fmt.Sprint(obs.PR.Number)
}

func prKey(repo ports.SCMRepo, number int) string {
	base := repo.Provider + ":" + repo.Host + ":" + repoFullName(repo)
	if number <= 0 {
		return base
	}
	return base + "#" + fmt.Sprint(number)
}

func commitKey(repo ports.SCMRepo, sha string) string { return prKey(repo, 0) + "@" + sha }

func repoFullName(repo ports.SCMRepo) string {
	if repo.Repo != "" {
		return repo.Repo
	}
	return repo.Owner + "/" + repo.Name
}

func ownerOf(full string) string {
	parts := strings.SplitN(full, "/", 2)
	if len(parts) == 2 {
		return parts[0]
	}
	return ""
}

func nameOf(full string) string {
	parts := strings.SplitN(full, "/", 2)
	if len(parts) == 2 {
		return parts[1]
	}
	return full
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func firstTime(a, b time.Time) time.Time {
	if !a.IsZero() {
		return a
	}
	return b
}

func normalizePRState(draft, merged, closed bool) string {
	switch {
	case merged:
		return string(domain.PRStateMerged)
	case closed:
		return string(domain.PRStateClosed)
	case draft:
		return string(domain.PRStateDraft)
	default:
		return string(domain.PRStateOpen)
	}
}

func scrubLine(s string) string {
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\r", " ")
	return strings.TrimSpace(s)
}

func (o *Observer) cacheSetString(m map[string]string, order *[]string, key, value string) {
	if _, ok := m[key]; !ok {
		*order = append(*order, key)
	}
	m[key] = value
	o.evictStrings(m, order)
}

func (o *Observer) cacheSetTime(m map[string]time.Time, order *[]string, key string, value time.Time) {
	if _, ok := m[key]; !ok {
		*order = append(*order, key)
	}
	m[key] = value
	for len(*order) > o.Cache.max {
		evict := (*order)[0]
		*order = (*order)[1:]
		delete(m, evict)
	}
}

func (o *Observer) evictStrings(m map[string]string, order *[]string) {
	for len(*order) > o.Cache.max {
		evict := (*order)[0]
		*order = (*order)[1:]
		delete(m, evict)
	}
}
