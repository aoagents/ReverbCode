package scm_test

import (
	"context"
	"errors"
	"testing"

	scmgithub "github.com/aoagents/agent-orchestrator/backend/internal/adapters/scm/github"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/scm"
)

// fakeProvider is a configurable fake that records calls.
type fakeProvider struct {
	mergeErr             error
	unresolvedThreadIDs  []string
	listErr              error
	resolveErr           error
	mergedPR             int
	resolvedThreads      []string
}

func (f *fakeProvider) MergePR(_ context.Context, _, _ string, prNumber int) error {
	if f.mergeErr != nil {
		return f.mergeErr
	}
	f.mergedPR = prNumber
	return nil
}

func (f *fakeProvider) ListUnresolvedThreadIDs(_ context.Context, _, _ string, _ int) ([]string, error) {
	return f.unresolvedThreadIDs, f.listErr
}

func (f *fakeProvider) ResolveThread(_ context.Context, threadID string) error {
	if f.resolveErr != nil {
		return f.resolveErr
	}
	f.resolvedThreads = append(f.resolvedThreads, threadID)
	return nil
}

func newSvc(p *fakeProvider) *scm.PRService {
	return scm.NewPRService("owner", "repo", p)
}

// ---- Merge tests ----

func TestPRService_Merge_HappyPath(t *testing.T) {
	p := &fakeProvider{}
	svc := newSvc(p)

	res, err := svc.Merge(context.Background(), "42")
	if err != nil {
		t.Fatalf("Merge: unexpected error: %v", err)
	}
	if res.PRNumber != 42 {
		t.Errorf("PRNumber = %d, want 42", res.PRNumber)
	}
	if res.Method != "squash" {
		t.Errorf("Method = %q, want squash", res.Method)
	}
	if p.mergedPR != 42 {
		t.Errorf("provider received PR %d, want 42", p.mergedPR)
	}
}

func TestPRService_Merge_InvalidID(t *testing.T) {
	p := &fakeProvider{}
	svc := newSvc(p)

	_, err := svc.Merge(context.Background(), "not-a-number")
	if !errors.Is(err, scm.ErrPRNotFound) {
		t.Errorf("err = %v, want ErrPRNotFound", err)
	}
}

func TestPRService_Merge_NotFound(t *testing.T) {
	p := &fakeProvider{mergeErr: scmgithub.ErrNotFound}
	svc := newSvc(p)

	_, err := svc.Merge(context.Background(), "1")
	if !errors.Is(err, scm.ErrPRNotFound) {
		t.Errorf("err = %v, want ErrPRNotFound", err)
	}
}

func TestPRService_Merge_NotMergeable(t *testing.T) {
	for _, provErr := range []error{
		scmgithub.ErrNotMergeable,
		// wrapped ErrNotMergeable (as classifyError produces)
		errors.New("wrapped: " + scmgithub.ErrNotMergeable.Error()),
	} {
		p := &fakeProvider{mergeErr: provErr}
		if errors.Is(provErr, scmgithub.ErrNotMergeable) {
			_, err := newSvc(p).Merge(context.Background(), "1")
			if !errors.Is(err, scm.ErrPRNotMergeable) {
				t.Errorf("mergeErr=%v: err = %v, want ErrPRNotMergeable", provErr, err)
			}
		}
	}
}

func TestPRService_Merge_Preconditions(t *testing.T) {
	p := &fakeProvider{mergeErr: scmgithub.ErrUnprocessable}
	svc := newSvc(p)

	_, err := svc.Merge(context.Background(), "1")
	if !errors.Is(err, scm.ErrPRPreconditions) {
		t.Errorf("err = %v, want ErrPRPreconditions", err)
	}
}

// ---- ResolveComments tests ----

func TestPRService_ResolveComments_ExplicitIDs(t *testing.T) {
	p := &fakeProvider{}
	svc := newSvc(p)

	res, err := svc.ResolveComments(context.Background(), "42", []string{"T_1", "T_2"})
	if err != nil {
		t.Fatalf("ResolveComments: unexpected error: %v", err)
	}
	if res.Resolved != 2 {
		t.Errorf("Resolved = %d, want 2", res.Resolved)
	}
	if len(p.resolvedThreads) != 2 || p.resolvedThreads[0] != "T_1" {
		t.Errorf("resolvedThreads = %v, want [T_1 T_2]", p.resolvedThreads)
	}
}

func TestPRService_ResolveComments_All(t *testing.T) {
	p := &fakeProvider{unresolvedThreadIDs: []string{"T_1", "T_2", "T_3"}}
	svc := newSvc(p)

	res, err := svc.ResolveComments(context.Background(), "42", nil)
	if err != nil {
		t.Fatalf("ResolveComments: unexpected error: %v", err)
	}
	if res.Resolved != 3 {
		t.Errorf("Resolved = %d, want 3", res.Resolved)
	}
}

func TestPRService_ResolveComments_NothingToResolve(t *testing.T) {
	p := &fakeProvider{unresolvedThreadIDs: []string{}} // no unresolved threads
	svc := newSvc(p)

	_, err := svc.ResolveComments(context.Background(), "42", nil)
	if !errors.Is(err, scm.ErrNothingToResolve) {
		t.Errorf("err = %v, want ErrNothingToResolve", err)
	}
}

func TestPRService_ResolveComments_PRNotFound(t *testing.T) {
	p := &fakeProvider{listErr: scmgithub.ErrNotFound}
	svc := newSvc(p)

	_, err := svc.ResolveComments(context.Background(), "42", nil)
	if !errors.Is(err, scm.ErrPRNotFound) {
		t.Errorf("err = %v, want ErrPRNotFound", err)
	}
}

func TestPRService_ResolveComments_InvalidID(t *testing.T) {
	p := &fakeProvider{}
	svc := newSvc(p)

	_, err := svc.ResolveComments(context.Background(), "abc", nil)
	if !errors.Is(err, scm.ErrPRNotFound) {
		t.Errorf("err = %v, want ErrPRNotFound", err)
	}
}

// Compile-time: PRService satisfies ports.PRService.
var _ ports.PRService = (*scm.PRService)(nil)
