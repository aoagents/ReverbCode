package github

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func TestPostPRReview(t *testing.T) {
	tests := []struct {
		name      string
		verdict   domain.ReviewVerdict
		wantEvent string
	}{
		{"changes requested", domain.VerdictChangesRequested, "REQUEST_CHANGES"},
		{"approved", domain.VerdictApproved, "APPROVE"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFakeGH(t)
			f.on(http.MethodPost, "/repos/octocat/hello/pulls/42/reviews", func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(`{"id":1}`))
			})
			p := newProviderForTest(t, f)

			err := p.PostPRReview(ctx(), "https://github.com/octocat/hello/pull/42", tt.verdict, "please fix X")
			if err != nil {
				t.Fatalf("PostPRReview: %v", err)
			}
			if n := f.callsTo(http.MethodPost, "/repos/octocat/hello/pulls/42/reviews"); n != 1 {
				t.Fatalf("review POST count = %d, want 1", n)
			}
			var body struct {
				Event string `json:"event"`
				Body  string `json:"body"`
			}
			if err := json.Unmarshal([]byte(f.calls()[0].Body), &body); err != nil {
				t.Fatalf("decode body: %v", err)
			}
			if body.Event != tt.wantEvent || body.Body != "please fix X" {
				t.Fatalf("posted body = %+v, want event %q", body, tt.wantEvent)
			}
		})
	}
}

func TestPostPRReviewRejectsUnsubmittableVerdict(t *testing.T) {
	f := newFakeGH(t)
	p := newProviderForTest(t, f)
	err := p.PostPRReview(ctx(), "https://github.com/octocat/hello/pull/42", domain.VerdictNone, "")
	if err == nil || !strings.Contains(err.Error(), "verdict") {
		t.Fatalf("want verdict error, got %v", err)
	}
	if len(f.calls()) != 0 {
		t.Fatalf("expected no HTTP call for invalid verdict, got %d", len(f.calls()))
	}
}
