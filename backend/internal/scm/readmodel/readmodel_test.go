package readmodel

import (
	"context"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/scm/store"
)

func TestAttachLatestSCMAddsSnapshotWithoutChangingDerivedStatus(t *testing.T) {
	ctx := context.Background()
	st := store.NewMemoryStore()
	session := domain.Session{
		SessionRecord: domain.SessionRecord{ID: "s1", ProjectID: "p1"},
		Status:        domain.StatusWorking,
	}
	subj := domain.SCMSubject{SessionID: "s1", ProjectID: "p1", Provider: domain.SCMProviderGitHub, Host: "github.com", Repo: "o/r", Branch: "feat/27", PRNumber: 7}
	if _, _, err := st.SaveSnapshot(ctx, domain.SCMSnapshot{SessionID: "s1", Subject: subj, PR: &domain.SCMPullRequest{Number: 7, State: domain.PROpen}, CI: domain.SCMCI{Summary: "passing"}}); err != nil {
		t.Fatal(err)
	}
	out, err := AttachLatestSCM(ctx, st, []domain.Session{session})
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 1 || out[0].SCM == nil || out[0].SCM.PR == nil || out[0].SCM.PR.Number != 7 {
		t.Fatalf("missing SCM snapshot: %+v", out)
	}
	if out[0].Status != domain.StatusWorking {
		t.Fatalf("status changed: %s", out[0].Status)
	}
}
