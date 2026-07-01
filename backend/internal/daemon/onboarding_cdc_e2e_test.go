package daemon

import (
	"context"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/cdc"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
)

// TestE2E_OnboardingFunnelThroughStore drives the whole funnel wiring against a
// real SQLite store: a PR write fires the migration-0006 trigger -> change_log
// -> poller -> broadcaster -> startOnboardingCDC subscriber -> sink. It proves
// the funnel emits pr_raised/first_pr_raised on creation and
// pr_merged/first_pr_merged when the same PR row transitions to merged, with the
// once-per-install milestones gated through the durable milestoneStore.
func TestE2E_OnboardingFunnelThroughStore(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	s, err := sqlite.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })

	now := time.Now().UTC().Truncate(time.Second)
	if err := s.UpsertProject(ctx, domain.ProjectRecord{ID: "mer", Path: "/m", RegisteredAt: now}); err != nil {
		t.Fatal(err)
	}
	rec, err := s.CreateSession(ctx, domain.SessionRecord{
		ProjectID: "mer", Kind: domain.KindWorker,
		Activity:  domain.Activity{State: domain.ActivityActive, LastActivityAt: now},
		CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}

	sink := &recordingSink{}
	ms := newMilestoneStore(dir)
	bcast := cdc.NewBroadcaster()
	unsub := startOnboardingCDC(bcast, sink, ms, nil)
	defer unsub()

	p := cdc.NewPoller(s, bcast, cdc.PollerConfig{})

	// pr_created -> pr_raised (activation) + first_pr_raised.
	if err := s.WritePR(ctx, domain.PullRequest{URL: "pr1", SessionID: rec.ID, UpdatedAt: now}, nil, nil); err != nil {
		t.Fatal(err)
	}
	if err := p.Poll(ctx); err != nil {
		t.Fatal(err)
	}

	// pr_updated with merged state -> pr_merged (success) + first_pr_merged.
	if err := s.WritePR(ctx, domain.PullRequest{URL: "pr1", SessionID: rec.ID, Merged: true, UpdatedAt: now.Add(time.Minute)}, nil, nil); err != nil {
		t.Fatal(err)
	}
	if err := p.Poll(ctx); err != nil {
		t.Fatal(err)
	}

	for _, want := range []string{
		"ao.session.pr_raised",
		"ao.onboarding.first_pr_raised",
		"ao.session.pr_merged",
		"ao.onboarding.first_pr_merged",
	} {
		if got := sink.count(want); got != 1 {
			t.Fatalf("%s count = %d, want 1 (names=%v)", want, got, sink.names())
		}
	}

	// The merged milestone is durable: a fresh store over the same dir treats
	// first_pr_merged as already claimed.
	reloaded := newMilestoneStore(dir)
	if !reloaded.claimed("first_pr_merged") {
		t.Fatal("first_pr_merged should persist across milestoneStore reload")
	}
}
