package daemon

import (
	"context"
	"errors"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func TestParseOwnerRepo(t *testing.T) {
	cases := []struct {
		in          string
		owner, repo string
		wantErr     bool
	}{
		{"https://github.com/octocat/hello.git", "octocat", "hello", false},
		{"https://github.com/octocat/hello", "octocat", "hello", false},
		{"git@github.com:octocat/hello.git", "octocat", "hello", false},
		{"git@github.com:octocat/hello", "octocat", "hello", false},
		{"ssh://git@github.com/octocat/hello.git", "octocat", "hello", false},
		{"https://github.com/octocat/hello.git\n", "octocat", "hello", false},
		{"https://gitlab.com/octocat/hello.git", "", "", true},
		{"git@bitbucket.org:octocat/hello.git", "", "", true},
		{"", "", "", true},
		{"not a url", "", "", true},
	}
	for _, tc := range cases {
		owner, repo, err := parseOwnerRepo(tc.in)
		if tc.wantErr {
			if err == nil {
				t.Errorf("parseOwnerRepo(%q): expected error, got %s/%s", tc.in, owner, repo)
			}
			continue
		}
		if err != nil {
			t.Errorf("parseOwnerRepo(%q): %v", tc.in, err)
			continue
		}
		if owner != tc.owner || repo != tc.repo {
			t.Errorf("parseOwnerRepo(%q) = %s/%s, want %s/%s", tc.in, owner, repo, tc.owner, tc.repo)
		}
	}
}

type fakeProjectStore struct {
	rec domain.ProjectRecord
	ok  bool
	err error
}

func (f fakeProjectStore) GetProject(context.Context, string) (domain.ProjectRecord, bool, error) {
	return f.rec, f.ok, f.err
}

func TestGitRepoResolver_ResolvesFromOrigin(t *testing.T) {
	r := gitRepoResolver{
		store: fakeProjectStore{rec: domain.ProjectRecord{ID: "p1", Path: "/repo"}, ok: true},
		git: func(_ context.Context, repoPath string) (string, error) {
			if repoPath != "/repo" {
				t.Errorf("git called with %q, want /repo", repoPath)
			}
			return "git@github.com:octocat/hello.git\n", nil
		},
	}
	owner, repo, err := r.RepoIdent(context.Background(), "p1")
	if err != nil {
		t.Fatalf("RepoIdent: %v", err)
	}
	if owner != "octocat" || repo != "hello" {
		t.Fatalf("got %s/%s, want octocat/hello", owner, repo)
	}
}

func TestGitRepoResolver_UnknownProject(t *testing.T) {
	r := gitRepoResolver{store: fakeProjectStore{ok: false}}
	if _, _, err := r.RepoIdent(context.Background(), "p1"); err == nil {
		t.Fatal("expected error for unknown project")
	}
}

func TestGitRepoResolver_GitFailureSurfaces(t *testing.T) {
	r := gitRepoResolver{
		store: fakeProjectStore{rec: domain.ProjectRecord{Path: "/repo"}, ok: true},
		git:   func(context.Context, string) (string, error) { return "", errors.New("no origin") },
	}
	if _, _, err := r.RepoIdent(context.Background(), "p1"); err == nil {
		t.Fatal("expected error when git fails")
	}
}
