package daemon

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/cdc"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// milestoneStore claims one-time onboarding milestones durably. The CDC
// subscriber runs in a single poller-driven goroutine, but the marker is
// persisted so a milestone already reached in a prior daemon run is never
// re-emitted after a restart. Keyed by an opaque name (e.g. "first_pr_raised"
// or "pr_merged:<url>"). It is the funnel's once-per-install gate, the CDC
// analogue of the store-derived first-ness checks in the session service.
type milestoneStore struct {
	mu   sync.Mutex
	path string
	seen map[string]struct{}
}

func newMilestoneStore(dataDir string) *milestoneStore {
	s := &milestoneStore{
		path: filepath.Join(dataDir, "telemetry_milestones.json"),
		seen: map[string]struct{}{},
	}
	if data, err := os.ReadFile(s.path); err == nil {
		var names []string
		if json.Unmarshal(data, &names) == nil {
			for _, n := range names {
				s.seen[n] = struct{}{}
			}
		}
	}
	return s
}

// claimed reports whether name was already recorded, without claiming it.
func (s *milestoneStore) claimed(name string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.seen[name]
	return ok
}

// claim records name and returns true only the first time it is seen.
func (s *milestoneStore) claim(name string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.seen[name]; ok {
		return false
	}
	s.seen[name] = struct{}{}
	names := make([]string, 0, len(s.seen))
	for n := range s.seen {
		names = append(names, n)
	}
	sort.Strings(names)
	if data, err := json.Marshal(names); err == nil {
		_ = os.WriteFile(s.path, data, 0o600)
	}
	return true
}

// prCDCPayload is the shape the pr_created/pr_updated triggers write into
// change_log (migration 0006). Only the fields the funnel needs are decoded.
type prCDCPayload struct {
	URL          string `json:"url"`
	Session      string `json:"session"`
	State        string `json:"state"`
	CI           string `json:"ci"`
	Review       string `json:"review"`
	Mergeability string `json:"mergeability"`
}

// startOnboardingCDC subscribes to the CDC broadcaster and turns PR row changes
// into funnel telemetry: pr_created -> pr_raised (activation), pr_updated with
// state=merged -> pr_merged (success), each paired with a once-per-install
// onboarding milestone. The broadcaster only pushes live events, so this is a
// best-effort live signal; the milestone marker keeps first-* events exactly
// once across restarts. The subscriber callback must not block.
func startOnboardingCDC(bcast *cdc.Broadcaster, sink ports.EventSink, milestones *milestoneStore, log *slog.Logger) func() {
	if bcast == nil || sink == nil || milestones == nil {
		return func() {}
	}
	return bcast.Subscribe(func(ev cdc.Event) {
		switch ev.Type {
		case cdc.EventPRCreated:
			emitPRRaised(sink, milestones, ev, log)
		case cdc.EventPRUpdated:
			emitPRMerged(sink, milestones, ev, log)
			emitPRReviewed(sink, milestones, ev, log)
		case cdc.EventPRReviewThreadResolved:
			emitPRRevised(sink, milestones, ev)
		}
	})
}

func decodePRPayload(ev cdc.Event, log *slog.Logger) (prCDCPayload, bool) {
	var p prCDCPayload
	if err := json.Unmarshal(ev.Payload, &p); err != nil {
		if log != nil {
			log.Warn("onboarding cdc: decode pr payload", "type", ev.Type, "seq", ev.Seq, "err", err)
		}
		return prCDCPayload{}, false
	}
	return p, true
}

func emitPRRaised(sink ports.EventSink, milestones *milestoneStore, ev cdc.Event, log *slog.Logger) {
	p, ok := decodePRPayload(ev, log)
	if !ok {
		return
	}
	payload := map[string]any{"state": p.State, "ci": p.CI, "review": p.Review, "mergeability": p.Mergeability}
	emitCDCTelemetry(sink, "ao.session.pr_raised", ev, payload)
	if milestones.claim("first_pr_raised") {
		emitCDCTelemetry(sink, "ao.onboarding.first_pr_raised", ev, map[string]any{"state": p.State})
	}
}

func emitPRMerged(sink ports.EventSink, milestones *milestoneStore, ev cdc.Event, log *slog.Logger) {
	p, ok := decodePRPayload(ev, log)
	if !ok || p.State != string(domain.PRStateMerged) {
		return
	}
	// pr_updated fires on any tracked-field change, and a merged PR can still
	// emit later updates (CI/review). Dedup the merge fact per PR URL so
	// pr_merged is one event per PR.
	if p.URL != "" && !milestones.claim("pr_merged:"+p.URL) {
		return
	}
	emitCDCTelemetry(sink, "ao.session.pr_merged", ev, map[string]any{"state": p.State})
	if milestones.claim("first_pr_merged") {
		emitCDCTelemetry(sink, "ao.onboarding.first_pr_merged", ev, map[string]any{})
	}
}

func emitPRReviewed(sink ports.EventSink, milestones *milestoneStore, ev cdc.Event, log *slog.Logger) {
	p, ok := decodePRPayload(ev, log)
	if !ok {
		return
	}
	if p.Review != string(domain.ReviewApproved) && p.Review != string(domain.ReviewChangesRequest) {
		return
	}
	// pr_updated fires on any tracked-field change; dedup per (PR, decision) so
	// each distinct human verdict is one pr_reviewed event.
	if p.URL != "" && !milestones.claim("pr_reviewed:"+p.URL+":"+p.Review) {
		return
	}
	emitCDCTelemetry(sink, "ao.session.pr_reviewed", ev, map[string]any{"decision": p.Review})
	if milestones.claim("first_pr_reviewed") {
		emitCDCTelemetry(sink, "ao.onboarding.first_pr_reviewed", ev, map[string]any{"decision": p.Review})
	}
}

// prThreadPayload is the pr_review_thread_resolved trigger shape (migration
// 0004): a resolved review thread is the cleanest "agent addressed feedback"
// signal available without tracking review history.
type prThreadPayload struct {
	PR     string `json:"pr"`
	Thread string `json:"thread"`
}

func emitPRRevised(sink ports.EventSink, milestones *milestoneStore, ev cdc.Event) {
	var p prThreadPayload
	if json.Unmarshal(ev.Payload, &p) != nil {
		return
	}
	// One revision signal per resolved thread.
	if p.Thread != "" && !milestones.claim("pr_revised:"+p.PR+":"+p.Thread) {
		return
	}
	emitCDCTelemetry(sink, "ao.session.pr_revised", ev, map[string]any{})
	if milestones.claim("first_pr_revised") {
		emitCDCTelemetry(sink, "ao.onboarding.first_pr_revised", ev, map[string]any{})
	}
}

func emitCDCTelemetry(sink ports.EventSink, name string, ev cdc.Event, payload map[string]any) {
	out := ports.TelemetryEvent{
		Name:       name,
		Source:     "cdc",
		OccurredAt: time.Now().UTC(),
		Level:      ports.TelemetryLevelInfo,
		Payload:    payload,
	}
	if ev.ProjectID != "" {
		projectID := domain.ProjectID(ev.ProjectID)
		out.ProjectID = &projectID
	}
	if ev.SessionID != "" {
		sessionID := domain.SessionID(ev.SessionID)
		out.SessionID = &sessionID
	}
	sink.Emit(context.Background(), out)
}
