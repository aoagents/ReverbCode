package pr

import (
	"context"
	"errors"
	"testing"

	scmgithub "github.com/aoagents/agent-orchestrator/backend/internal/adapters/scm/github"
)

type fakeProvider struct {
	mergeErr      error
	threadIDs     []string
	listErr       error
	resolveErr    error
	resolvedCalls []string
}

func (f *fakeProvider) MergePR(_ context.Context, _, _ string, _ int) error {
	return f.mergeErr
}

func (f *fakeProvider) ListUnresolvedThreadIDs(_ context.Context, _, _ string, _ int) ([]string, error) {
	return f.threadIDs, f.listErr
}

func (f *fakeProvider) ResolveThread(_ context.Context, threadID string) error {
	f.resolvedCalls = append(f.resolvedCalls, threadID)
	return f.resolveErr
}

func newTestService(p *fakeProvider) *ActionService {
	return NewActionService("owner", "repo", p)
}

// ---- Merge ----

func TestMerge_HappyPath(t *testing.T) {
	svc := newTestService(&fakeProvider{})
	res, err := svc.Merge(context.Background(), "42")
	if err != nil {
		t.Fatal(err)
	}
	if res.PRNumber != 42 || res.Method != "squash" {
		t.Errorf("res = %+v, want {42 squash}", res)
	}
}

func TestMerge_InvalidID(t *testing.T) {
	svc := newTestService(&fakeProvider{})
	_, err := svc.Merge(context.Background(), "abc")
	if !errors.Is(err, ErrPRNotFound) {
		t.Errorf("err = %v, want ErrPRNotFound", err)
	}
}

func TestMerge_NotFound(t *testing.T) {
	svc := newTestService(&fakeProvider{mergeErr: scmgithub.ErrNotFound})
	_, err := svc.Merge(context.Background(), "1")
	if !errors.Is(err, ErrPRNotFound) {
		t.Errorf("err = %v, want ErrPRNotFound", err)
	}
}

func TestMerge_NotMergeable(t *testing.T) {
	svc := newTestService(&fakeProvider{mergeErr: scmgithub.ErrNotMergeable})
	_, err := svc.Merge(context.Background(), "1")
	if !errors.Is(err, ErrPRNotMergeable) {
		t.Errorf("err = %v, want ErrPRNotMergeable", err)
	}
}

func TestMerge_Preconditions(t *testing.T) {
	svc := newTestService(&fakeProvider{mergeErr: scmgithub.ErrUnprocessable})
	_, err := svc.Merge(context.Background(), "1")
	if !errors.Is(err, ErrPRPreconditions) {
		t.Errorf("err = %v, want ErrPRPreconditions", err)
	}
}

// ---- ResolveComments ----

func TestResolveComments_ExplicitIDs(t *testing.T) {
	p := &fakeProvider{}
	svc := newTestService(p)
	res, err := svc.ResolveComments(context.Background(), "1", []string{"T_A", "T_B"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Resolved != 2 {
		t.Errorf("resolved = %d, want 2", res.Resolved)
	}
	if len(p.resolvedCalls) != 2 {
		t.Errorf("resolve calls = %v, want [T_A T_B]", p.resolvedCalls)
	}
}

func TestResolveComments_ResolveAll(t *testing.T) {
	p := &fakeProvider{threadIDs: []string{"T_1", "T_2", "T_3"}}
	svc := newTestService(p)
	res, err := svc.ResolveComments(context.Background(), "1", nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.Resolved != 3 {
		t.Errorf("resolved = %d, want 3", res.Resolved)
	}
}

func TestResolveComments_NothingToResolve(t *testing.T) {
	p := &fakeProvider{threadIDs: []string{}}
	svc := newTestService(p)
	_, err := svc.ResolveComments(context.Background(), "1", nil)
	if !errors.Is(err, ErrNothingToResolve) {
		t.Errorf("err = %v, want ErrNothingToResolve", err)
	}
}

func TestResolveComments_PRNotFound(t *testing.T) {
	p := &fakeProvider{listErr: scmgithub.ErrNotFound}
	svc := newTestService(p)
	_, err := svc.ResolveComments(context.Background(), "1", nil)
	if !errors.Is(err, ErrPRNotFound) {
		t.Errorf("err = %v, want ErrPRNotFound", err)
	}
}

func TestResolveComments_ExplicitIDs_PRNotFound(t *testing.T) {
	// Explicit IDs supplied but the PR itself doesn't exist: the existence
	// probe must surface ErrPRNotFound so the path param is not a no-op.
	p := &fakeProvider{listErr: scmgithub.ErrNotFound}
	svc := newTestService(p)
	_, err := svc.ResolveComments(context.Background(), "99", []string{"T_A"})
	if !errors.Is(err, ErrPRNotFound) {
		t.Errorf("err = %v, want ErrPRNotFound", err)
	}
}

func TestResolveComments_InvalidID(t *testing.T) {
	svc := newTestService(&fakeProvider{})
	_, err := svc.ResolveComments(context.Background(), "bad", nil)
	if !errors.Is(err, ErrPRNotFound) {
		t.Errorf("err = %v, want ErrPRNotFound", err)
	}
}
