package reviewrunner

import (
	"context"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	reviewsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/review"
)

type fakeReviewer struct {
	gotInv ports.ReviewInvocation
	spec   ports.ReviewCommandSpec
	err    error
}

func (f *fakeReviewer) ReviewCommand(_ context.Context, inv ports.ReviewInvocation) (ports.ReviewCommandSpec, error) {
	f.gotInv = inv
	return f.spec, f.err
}

type fakeResolver struct {
	reviewer ports.Reviewer
	ok       bool
}

func (f fakeResolver) Reviewer(domain.ReviewerHarness) (ports.Reviewer, bool) {
	return f.reviewer, f.ok
}

type fakeRuntime struct {
	cfg     ports.RuntimeConfig
	created bool
}

func (f *fakeRuntime) Create(_ context.Context, cfg ports.RuntimeConfig) (ports.RuntimeHandle, error) {
	f.created = true
	f.cfg = cfg
	return ports.RuntimeHandle{ID: "h1"}, nil
}
func (f *fakeRuntime) Destroy(context.Context, ports.RuntimeHandle) error         { return nil }
func (f *fakeRuntime) IsAlive(context.Context, ports.RuntimeHandle) (bool, error) { return true, nil }

func TestRunLaunchesResolvedReviewer(t *testing.T) {
	reviewer := &fakeReviewer{spec: ports.ReviewCommandSpec{
		Argv: []string{"greptile", "review"},
		Env:  map[string]string{"GREPTILE_MODE": "ci"},
	}}
	rt := &fakeRuntime{}
	r := New(fakeResolver{reviewer: reviewer, ok: true}, rt)

	err := r.Run(context.Background(), reviewsvc.RunSpec{
		WorkerID:      "mer-1",
		Harness:       domain.ReviewerHarness("greptile"),
		WorkspacePath: "/ws/mer-1",
		PRURL:         "https://github.com/o/r/pull/1",
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	// The reviewer adapter receives the invocation (PR + worktree + reviewer id).
	if reviewer.gotInv.PRURL != "https://github.com/o/r/pull/1" || reviewer.gotInv.WorkspacePath != "/ws/mer-1" || reviewer.gotInv.ReviewerID != "review-mer-1" {
		t.Fatalf("invocation = %+v", reviewer.gotInv)
	}
	// The runtime launches the adapter's argv over the worker's worktree.
	if !rt.created || rt.cfg.WorkspacePath != "/ws/mer-1" || rt.cfg.Argv[0] != "greptile" {
		t.Fatalf("runtime cfg = %+v created=%v", rt.cfg, rt.created)
	}
	// AO_REVIEW_WORKER is added; adapter env is preserved.
	if rt.cfg.Env["AO_REVIEW_WORKER"] != "mer-1" || rt.cfg.Env["GREPTILE_MODE"] != "ci" {
		t.Fatalf("env = %v", rt.cfg.Env)
	}
}

func TestRunErrorsWhenNoReviewerAdapter(t *testing.T) {
	r := New(fakeResolver{ok: false}, &fakeRuntime{})
	err := r.Run(context.Background(), reviewsvc.RunSpec{Harness: "nope", WorkspacePath: "/ws"})
	if err == nil || !strings.Contains(err.Error(), "no reviewer adapter") {
		t.Fatalf("err = %v, want no-adapter error", err)
	}
}
