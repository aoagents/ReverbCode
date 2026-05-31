package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// Store is a small durable JSON implementation of ports.SCMStore. It is meant
// to back the first Go daemon milestone before a SQL adapter exists: all writes
// are serialized, semantic snapshot revisions are assigned atomically, and the
// file is replaced with rename for crash-safe updates on a single filesystem.
type Store struct {
	mu    sync.Mutex
	path  string
	clock func() time.Time
	data  persisted
}

var _ ports.SCMStore = (*Store)(nil)

type Option func(*Store)

func WithClock(clock func() time.Time) Option {
	return func(s *Store) {
		if clock != nil {
			s.clock = clock
		}
	}
}

func NewMemoryStore(opts ...Option) *Store {
	s := &Store{clock: time.Now, data: emptyPersisted()}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

func NewFileStore(path string, opts ...Option) (*Store, error) {
	s := &Store{path: path, clock: time.Now, data: emptyPersisted()}
	for _, opt := range opts {
		opt(s)
	}
	if err := s.load(); err != nil {
		return nil, err
	}
	return s, nil
}

type persisted struct {
	Subjects      map[string]domain.SCMSubject            `json:"subjects"`
	Snapshots     map[string][]domain.SCMSnapshot         `json:"snapshots"`
	ProviderCache map[string]domain.SCMProviderCacheEntry `json:"providerCache"`
	PollStates    map[string]domain.SCMPollState          `json:"pollStates"`
}

func emptyPersisted() persisted {
	return persisted{
		Subjects:      map[string]domain.SCMSubject{},
		Snapshots:     map[string][]domain.SCMSnapshot{},
		ProviderCache: map[string]domain.SCMProviderCacheEntry{},
		PollStates:    map[string]domain.SCMPollState{},
	}
}

func (s *Store) load() error {
	if s.path == "" {
		return nil
	}
	b, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("scm store read %s: %w", s.path, err)
	}
	if len(b) == 0 {
		return nil
	}
	var data persisted
	if err := json.Unmarshal(b, &data); err != nil {
		return fmt.Errorf("scm store parse %s: %w", s.path, err)
	}
	s.data = normalize(data)
	return nil
}

func normalize(d persisted) persisted {
	if d.Subjects == nil {
		d.Subjects = map[string]domain.SCMSubject{}
	}
	if d.Snapshots == nil {
		d.Snapshots = map[string][]domain.SCMSnapshot{}
	}
	if d.ProviderCache == nil {
		d.ProviderCache = map[string]domain.SCMProviderCacheEntry{}
	}
	if d.PollStates == nil {
		d.PollStates = map[string]domain.SCMPollState{}
	}
	return d
}

func (s *Store) persistLocked() error {
	if s.path == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(s.data, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

func (s *Store) now() time.Time {
	if s.clock == nil {
		return time.Now()
	}
	return s.clock()
}

func (s *Store) UpsertSubject(_ context.Context, subject domain.SCMSubject) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.upsertSubjectLocked(subject)
	return s.persistLocked()
}

func (s *Store) upsertSubjectLocked(subject domain.SCMSubject) domain.SCMSubject {
	now := s.now()
	if subject.CreatedAt.IsZero() {
		if existing, ok := s.data.Subjects[subject.SubjectKey()]; ok && !existing.CreatedAt.IsZero() {
			subject.CreatedAt = existing.CreatedAt
		} else {
			subject.CreatedAt = now
		}
	}
	subject.UpdatedAt = now
	s.data.Subjects[subject.SubjectKey()] = subject
	return subject
}

func (s *Store) GetSubject(_ context.Context, sessionID domain.SessionID) (domain.SCMSubject, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	subj, ok := s.data.Subjects[string(sessionID)]
	return subj, ok, nil
}

func (s *Store) ListSubjects(_ context.Context, filter domain.SCMSubjectFilter) ([]domain.SCMSubject, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]domain.SCMSubject, 0, len(s.data.Subjects))
	for _, subj := range s.data.Subjects {
		if filter.ProjectID != "" && subj.ProjectID != filter.ProjectID {
			continue
		}
		if filter.Provider != "" && subj.Provider != filter.Provider {
			continue
		}
		if filter.Host != "" && normalizeHost(filter.Host) != normalizeHost(subj.Host) {
			continue
		}
		if filter.Repo != "" && lower(filter.Repo) != lower(subj.Repo) {
			continue
		}
		out = append(out, subj)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].SessionID < out[j].SessionID })
	return out, nil
}

func (s *Store) DeleteSubject(_ context.Context, sessionID domain.SessionID) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.data.Subjects, string(sessionID))
	return s.persistLocked()
}

func (s *Store) SaveSnapshot(_ context.Context, snapshot domain.SCMSnapshot) (domain.SCMSnapshot, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if snapshot.SessionID == "" {
		snapshot.SessionID = snapshot.Subject.SessionID
	}
	if snapshot.SessionID == "" {
		return domain.SCMSnapshot{}, false, fmt.Errorf("scm store: snapshot missing session id")
	}
	if snapshot.Subject.SessionID == "" {
		snapshot.Subject.SessionID = snapshot.SessionID
	}
	if snapshot.ObservedAt.IsZero() {
		snapshot.ObservedAt = s.now()
	}
	if snapshot.Freshness == "" {
		snapshot.Freshness = domain.SCMFreshnessFresh
	}
	hash, err := domain.ComputeSCMSemanticHash(snapshot)
	if err != nil {
		return domain.SCMSnapshot{}, false, err
	}
	snapshot.SemanticHash = hash

	key := string(snapshot.SessionID)
	history := s.data.Snapshots[key]
	if len(history) > 0 {
		latest := history[len(history)-1]
		if latest.SemanticHash == snapshot.SemanticHash {
			if subjectNeedsTimestampRepair(s.data.Subjects[snapshot.Subject.SubjectKey()]) {
				s.upsertSubjectLocked(snapshot.Subject)
				if err := s.persistLocked(); err != nil {
					return domain.SCMSnapshot{}, false, err
				}
			}
			return latest, false, nil
		}
		snapshot.Revision = latest.Revision + 1
	} else {
		snapshot.Revision = 1
	}
	snapshot.Subject = s.upsertSubjectLocked(snapshot.Subject)
	s.data.Snapshots[key] = append(history, cloneSnapshot(snapshot))
	if err := s.persistLocked(); err != nil {
		return domain.SCMSnapshot{}, false, err
	}
	return snapshot, true, nil
}

func subjectNeedsTimestampRepair(subject domain.SCMSubject) bool {
	return subject.SessionID == "" || subject.CreatedAt.IsZero() || subject.UpdatedAt.IsZero()
}

func (s *Store) GetLatestSnapshot(_ context.Context, sessionID domain.SessionID) (domain.SCMSnapshot, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	history := s.data.Snapshots[string(sessionID)]
	if len(history) == 0 {
		return domain.SCMSnapshot{}, false, nil
	}
	return cloneSnapshot(history[len(history)-1]), true, nil
}

func (s *Store) ListLatestSnapshots(_ context.Context, project domain.ProjectID) ([]domain.SCMSnapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]domain.SCMSnapshot, 0, len(s.data.Snapshots))
	for id, history := range s.data.Snapshots {
		if len(history) == 0 {
			continue
		}
		latest := history[len(history)-1]
		if project != "" {
			if subj, ok := s.data.Subjects[id]; ok && subj.ProjectID != project {
				continue
			}
			if latest.Subject.ProjectID != "" && latest.Subject.ProjectID != project {
				continue
			}
		}
		out = append(out, cloneSnapshot(latest))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].SessionID < out[j].SessionID })
	return out, nil
}

func (s *Store) GetProviderCache(_ context.Context, key domain.SCMProviderCacheKey) (domain.SCMProviderCacheEntry, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, ok := s.data.ProviderCache[key.String()]
	if !ok {
		return domain.SCMProviderCacheEntry{}, false, nil
	}
	if entry.Expired(s.now()) {
		delete(s.data.ProviderCache, key.String())
		_ = s.persistLocked()
		return domain.SCMProviderCacheEntry{}, false, nil
	}
	return cloneCacheEntry(entry), true, nil
}

func (s *Store) PutProviderCache(_ context.Context, entry domain.SCMProviderCacheEntry) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if entry.UpdatedAt.IsZero() {
		entry.UpdatedAt = s.now()
	}
	s.data.ProviderCache[entry.Key.String()] = cloneCacheEntry(entry)
	if entry.MaxEntries > 0 {
		s.pruneProviderCacheLocked(entry.Key.SCMProviderCacheScope, entry.Key.Namespace, entry.MaxEntries)
	}
	return s.persistLocked()
}

func (s *Store) pruneProviderCacheLocked(scope domain.SCMProviderCacheScope, namespace string, capN int) {
	if capN <= 0 {
		return
	}
	type keyedEntry struct {
		key   string
		entry domain.SCMProviderCacheEntry
	}
	entries := make([]keyedEntry, 0)
	prefix := domain.SCMProviderCachePrefix{SCMProviderCacheScope: scope, Namespace: namespace}
	for key, entry := range s.data.ProviderCache {
		if prefix.Matches(entry.Key) {
			entries = append(entries, keyedEntry{key: key, entry: entry})
		}
	}
	if len(entries) <= capN {
		return
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].entry.UpdatedAt.Before(entries[j].entry.UpdatedAt)
	})
	for _, old := range entries[:len(entries)-capN] {
		delete(s.data.ProviderCache, old.key)
	}
}

func (s *Store) DeleteProviderCache(_ context.Context, prefix domain.SCMProviderCachePrefix) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, entry := range s.data.ProviderCache {
		if prefix.Matches(entry.Key) {
			delete(s.data.ProviderCache, entry.Key.String())
		}
	}
	return s.persistLocked()
}

func (s *Store) GetPollState(_ context.Context, key domain.SCMPollStateKey) (domain.SCMPollState, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, ok := s.data.PollStates[key.String()]
	return st, ok, nil
}

func (s *Store) PutPollState(_ context.Context, state domain.SCMPollState) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data.PollStates[state.Key.String()] = state
	return s.persistLocked()
}

func cloneSnapshot(in domain.SCMSnapshot) domain.SCMSnapshot {
	out := in
	if in.PR != nil {
		pr := *in.PR
		out.PR = &pr
	}
	out.CI.Checks = append([]domain.SCMCheck(nil), in.CI.Checks...)
	out.Review.UnresolvedThreads = cloneThreads(in.Review.UnresolvedThreads)
	out.Review.BotComments = cloneThreads(in.Review.BotComments)
	out.Review.HumanComments = cloneThreads(in.Review.HumanComments)
	out.Mergeability.Blockers = append([]string(nil), in.Mergeability.Blockers...)
	if in.RateLimit != nil {
		rl := *in.RateLimit
		out.RateLimit = &rl
	}
	out.Diagnostics = append([]domain.SCMDiagnostic(nil), in.Diagnostics...)
	return out
}

func cloneThreads(in []domain.SCMReviewThread) []domain.SCMReviewThread {
	out := make([]domain.SCMReviewThread, len(in))
	for i, th := range in {
		out[i] = th
		out[i].Comments = append([]domain.SCMReviewComment(nil), th.Comments...)
	}
	return out
}

func cloneCacheEntry(in domain.SCMProviderCacheEntry) domain.SCMProviderCacheEntry {
	out := in
	out.Value = append([]byte(nil), in.Value...)
	return out
}

func lower(s string) string { return strings.ToLower(strings.TrimSpace(s)) }

func normalizeHost(h string) string {
	h = strings.TrimSpace(strings.ToLower(h))
	h = strings.TrimPrefix(h, "https://")
	h = strings.TrimPrefix(h, "http://")
	return strings.TrimSuffix(h, "/")
}
