package observer

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// Observer coordinates provider polling, durable snapshot writes, event fanout
// and lifecycle application. It owns no provider-specific logic.
type Observer struct {
	Store     ports.SCMStore
	LCM       ports.LifecycleManager
	Providers map[domain.SCMProvider]ports.SCMProvider
	Clock     func() time.Time

	// OnSnapshot is the first-pass in-process fanout hook for API/dashboard live
	// updates. Durable outbox/replay can replace it later without changing
	// provider adapters.
	OnSnapshot func(context.Context, domain.SCMSnapshot) error
}

var _ ports.SCMObserver = (*Observer)(nil)

func New(store ports.SCMStore, lcm ports.LifecycleManager, providers ...ports.SCMProvider) *Observer {
	o := &Observer{Store: store, LCM: lcm, Providers: map[domain.SCMProvider]ports.SCMProvider{}, Clock: time.Now}
	for _, p := range providers {
		if p != nil {
			o.Providers[p.Provider()] = p
		}
	}
	return o
}

func (o *Observer) RefreshSession(ctx context.Context, sessionID domain.SessionID) error {
	subj, ok, err := o.Store.GetSubject(ctx, sessionID)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("scm observer: subject %s not found", sessionID)
	}
	return o.Refresh(ctx, []domain.SCMSubject{subj})
}

func (o *Observer) Invalidate(ctx context.Context, subject domain.SCMSubject, reason string) error {
	prefix := domain.SCMProviderCachePrefix{SCMProviderCacheScope: subject.CacheScope()}
	if err := o.Store.DeleteProviderCache(ctx, prefix); err != nil {
		return err
	}
	return o.Refresh(ctx, []domain.SCMSubject{subject})
}

func (o *Observer) Refresh(ctx context.Context, subjects []domain.SCMSubject) error {
	if o.Store == nil {
		return fmt.Errorf("scm observer: nil store")
	}
	if o.Clock == nil {
		o.Clock = time.Now
	}
	byProvider := map[domain.SCMProvider][]domain.SCMSubject{}
	defaultProvider, hasDefaultProvider := o.singleProvider()
	var firstErr error
	for _, subj := range subjects {
		if subj.SessionID == "" {
			continue
		}
		if subj.Provider == "" && hasDefaultProvider {
			subj.Provider = defaultProvider
		}
		if subj.Provider == "" {
			if firstErr == nil {
				firstErr = &domain.SCMError{Kind: domain.SCMErrorUnsupported, Operation: "observe", Message: "subject provider is required when observer has zero or multiple providers"}
			}
			continue
		}
		byProvider[subj.Provider] = append(byProvider[subj.Provider], subj)
	}

	for providerID, group := range byProvider {
		provider := o.Providers[providerID]
		if provider == nil {
			if firstErr == nil {
				firstErr = &domain.SCMError{Kind: domain.SCMErrorUnsupported, Operation: "observe", Message: fmt.Sprintf("provider %q not registered", providerID)}
			}
			continue
		}
		res, err := provider.ObserveSessions(ctx, ports.SCMObserveRequest{Subjects: group, Now: o.Clock()}, o.Store)
		for _, subj := range res.Subjects {
			if upsertErr := o.Store.UpsertSubject(ctx, subj); upsertErr != nil {
				return upsertErr
			}
		}
		for _, st := range res.PollStates {
			if pollErr := o.Store.PutPollState(ctx, st); pollErr != nil {
				return pollErr
			}
		}
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			saved := map[domain.SessionID]bool{}
			for _, snap := range res.Snapshots {
				if saveErr := o.saveAndApply(ctx, snap); saveErr != nil && firstErr == nil {
					firstErr = saveErr
				}
				saved[snap.SessionID] = true
			}
			for _, subj := range group {
				if saved[subj.SessionID] {
					continue
				}
				if saveErr := o.saveAndApply(ctx, unavailableSnapshot(subj, o.Clock(), err)); saveErr != nil && firstErr == nil {
					firstErr = saveErr
				}
			}
			continue
		}
		for _, snap := range res.Snapshots {
			if saveErr := o.saveAndApply(ctx, snap); saveErr != nil {
				return saveErr
			}
		}
	}
	return firstErr
}

func (o *Observer) singleProvider() (domain.SCMProvider, bool) {
	if len(o.Providers) != 1 {
		return "", false
	}
	for id := range o.Providers {
		return id, true
	}
	return "", false
}

func (o *Observer) saveAndApply(ctx context.Context, snap domain.SCMSnapshot) error {
	saved, changed, err := o.Store.SaveSnapshot(ctx, snap)
	if err != nil {
		return err
	}
	if !changed {
		return nil
	}
	if o.OnSnapshot != nil {
		if err := o.OnSnapshot(ctx, saved); err != nil {
			return err
		}
	}
	if o.LCM != nil {
		if err := o.LCM.ApplySCMObservation(ctx, saved.SessionID, FactsFromSnapshot(saved)); err != nil {
			return err
		}
	}
	return nil
}

func unavailableSnapshot(subj domain.SCMSubject, now time.Time, err error) domain.SCMSnapshot {
	d := domain.SCMDiagnostic{Operation: "observe", ErrorKind: domain.SCMErrorUnavailable, Message: errString(err)}
	if se, ok := err.(*domain.SCMError); ok {
		d.ErrorKind = se.Kind
		d.StatusCode = se.StatusCode
		d.Operation = se.Operation
	}
	return domain.SCMSnapshot{SessionID: subj.SessionID, Subject: subj, Freshness: domain.SCMFreshnessUnavailable, ObservedAt: now, Diagnostics: []domain.SCMDiagnostic{d}}
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// FactsFromSnapshot projects normalized SCM snapshots into the existing LCM DTO.
func FactsFromSnapshot(s domain.SCMSnapshot) ports.SCMFacts {
	f := ports.SCMFacts{ObservedAt: s.ObservedAt, Fetched: s.Freshness != domain.SCMFreshnessUnavailable}
	if !f.Fetched {
		return f
	}
	if s.PR == nil {
		f.PRState = domain.PRNone
		return f
	}
	f.PRState = s.PR.State
	f.Draft = s.PR.Draft
	f.PRNumber = s.PR.Number
	f.PRURL = s.PR.URL
	f.CISummary = ciSummary(s.CI.Summary)
	f.ReviewDecision = reviewDecision(s.Review.Decision)
	f.Mergeability = ports.Mergeability{
		Mergeable:   s.Mergeability.Mergeable,
		CIPassing:   s.Mergeability.CIPassing,
		Approved:    s.Mergeability.Approved,
		NoConflicts: s.Mergeability.NoConflicts,
		Conflict:    s.Mergeability.Conflict,
		BehindBase:  s.Mergeability.BehindBase,
		Unknown:     mergeabilityUnknown(s.Mergeability),
		Blockers:    append([]string(nil), s.Mergeability.Blockers...),
	}
	for _, check := range s.CI.Checks {
		if failedCompletedCheck(check) {
			f.CIFailedChecks = append(f.CIFailedChecks, ports.CICheck{Name: check.Name, Status: check.Status, Conclusion: check.Conclusion, URL: check.URL, Details: check.Details, LogTail: check.LogTail})
		}
	}
	if s.CI.FailureLogTail != "" {
		tail := s.CI.FailureLogTail
		f.CIFailureLogTail = &tail
	}
	for _, th := range s.Review.UnresolvedThreads {
		for _, c := range th.Comments {
			f.PendingComments = append(f.PendingComments, ports.ReviewComment{Author: c.Author, Body: c.Body, IsBot: c.IsBot, URL: firstNonEmpty(c.URL, th.URL), Path: firstNonEmpty(c.Path, th.Path), Line: firstNonZero(c.Line, th.Line), ThreadID: firstNonEmpty(c.ThreadID, th.ID)})
		}
	}
	return f
}

func mergeabilityUnknown(m domain.SCMMergeability) bool {
	raw := strings.ToUpper(strings.TrimSpace(m.RawState))
	return raw == "" || raw == "UNKNOWN"
}

func ciSummary(s string) ports.CISummary {
	switch domain.NormalizeSCMCI(s) {
	case "passing":
		return ports.CIPassing
	case "failing":
		return ports.CIFailing
	case "pending":
		return ports.CIPending
	default:
		return ports.CINone
	}
}

func reviewDecision(s string) ports.ReviewDecision {
	switch domain.NormalizeSCMReviewDecision(s) {
	case "approved":
		return ports.ReviewApproved
	case "changes_requested":
		return ports.ReviewChangesRequested
	case "pending":
		return ports.ReviewPending
	default:
		return ports.ReviewNone
	}
}

func isFailedConclusion(s string) bool {
	s = strings.ToLower(strings.TrimSpace(s))
	return s == "failure" || s == "failed" || s == "failing" || s == "error" || s == "timed_out" || s == "cancelled" || s == "action_required"
}

func failedCompletedCheck(check domain.SCMCheck) bool {
	if check.Conclusion != "" {
		status := strings.ToLower(strings.TrimSpace(check.Status))
		if status == "" || status == "completed" || status == "complete" {
			return isFailedConclusion(check.Conclusion)
		}
		return false
	}
	return isFailedConclusion(check.Status)
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func firstNonZero(a, b int) int {
	if a != 0 {
		return a
	}
	return b
}

// SubjectConfig tells the observer how to bind sessions to SCM subjects.
type SubjectConfig struct {
	Provider       domain.SCMProvider
	Host           string
	Repo           string
	BaseBranch     string
	CredentialHash string
}

// SubjectsFromSessions creates/updates SCM subjects from active sessions. Repo
// and provider come from config; branch and known PR metadata can be present on
// the SessionRecord metadata. Terminal sessions are intentionally ignored.
func SubjectsFromSessions(sessions []domain.Session, cfg SubjectConfig) []domain.SCMSubject {
	out := make([]domain.SCMSubject, 0, len(sessions))
	for _, s := range sessions {
		if isTerminalSession(s.Lifecycle.Session.State) {
			continue
		}
		provider := domain.SCMProvider(metadataFirst(s.Metadata, "provider", "scm.provider"))
		if provider == "" {
			provider = cfg.Provider
		}
		if provider == "" {
			continue
		}
		host := metadataFirst(s.Metadata, "host", "scm.host")
		if host == "" {
			host = cfg.Host
		}
		providerPrefix := string(provider)
		branch := metadataFirst(s.Metadata, metadataKeys(providerPrefix, "branch")...)
		if branch == "" {
			continue
		}
		repo := metadataFirst(s.Metadata, append(metadataKeys(providerPrefix, "repo"), "repository")...)
		if repo == "" {
			repo = cfg.Repo
		}
		if repo == "" {
			continue
		}
		prURL := metadataFirst(s.Metadata, metadataKeys(providerPrefix, "prUrl", "prURL")...)
		prNumber := parsePRNumber(metadataFirst(s.Metadata, metadataKeys(providerPrefix, "prNumber")...), prURL)
		out = append(out, domain.SCMSubject{SessionID: s.ID, ProjectID: s.ProjectID, Provider: provider, Host: host, Repo: repo, Branch: branch, BaseBranch: cfg.BaseBranch, CredentialHash: cfg.CredentialHash, PRNumber: prNumber, PRURL: prURL})
	}
	return out
}

func isTerminalSession(s domain.SessionState) bool {
	return s == domain.SessionDone || s == domain.SessionTerminated
}

func metadataFirst(m map[string]string, keys ...string) string {
	for _, k := range keys {
		if v := strings.TrimSpace(m[k]); v != "" {
			return v
		}
	}
	return ""
}

func metadataKeys(providerPrefix string, names ...string) []string {
	keys := make([]string, 0, len(names)*3)
	for _, name := range names {
		keys = append(keys, name, "scm."+name)
		if providerPrefix != "" {
			keys = append(keys, providerPrefix+"."+name)
		}
	}
	return keys
}

func parsePRNumber(raw, prURL string) int {
	if raw != "" {
		if n, err := strconv.Atoi(raw); err == nil {
			return n
		}
	}
	if prURL == "" {
		return 0
	}
	u, err := url.Parse(prURL)
	if err != nil {
		return 0
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	for i := 0; i < len(parts)-1; i++ {
		switch parts[i] {
		case "pull", "pulls", "pull-requests", "merge_requests", "merge-requests":
			n, _ := strconv.Atoi(parts[i+1])
			return n
		}
	}
	return 0
}
