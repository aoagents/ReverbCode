package gitworktree

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestWithDefaultTimeoutNoExistingDeadline(t *testing.T) {
	ctx := context.Background()
	wrapped, cancel := withDefaultTimeout(ctx, defaultCommandTimeout)
	defer cancel()
	deadline, ok := wrapped.Deadline()
	if !ok {
		t.Fatal("expected deadline, got none")
	}
	until := time.Until(deadline)
	if until <= 0 || until > defaultCommandTimeout {
		t.Fatalf("deadline %v not in (0, %v]", until, defaultCommandTimeout)
	}
}

func TestWithDefaultTimeoutPreservesExistingDeadline(t *testing.T) {
	parent, parentCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer parentCancel()
	wrapped, cancel := withDefaultTimeout(parent, defaultCommandTimeout)
	defer cancel()
	if wrapped != parent {
		t.Fatal("expected same context when deadline already set")
	}
}

func TestCommandArgs(t *testing.T) {
	repo := "/repo"
	path := "/managed/proj/sess"
	branch := "feature/test"

	cases := []struct {
		name string
		got  []string
		want []string
	}{
		{"check ref", checkRefFormatBranchArgs(repo, branch), []string{"-C", repo, "check-ref-format", "--branch", branch}},
		{"rev parse", revParseVerifyArgs(repo, "origin/main"), []string{"-C", repo, "rev-parse", "--verify", "--quiet", "origin/main"}},
		{"add existing", worktreeAddBranchArgs(repo, path, branch), []string{"-C", repo, "worktree", "add", path, branch}},
		{"add new", worktreeAddNewBranchArgs(repo, branch, path, "origin/main"), []string{"-C", repo, "worktree", "add", "-b", branch, path, "origin/main"}},
		// No --force: a dirty worktree must cause `git worktree remove` to fail so
		// the post-prune safety check surfaces the refusal instead of deleting
		// uncommitted agent work (review item RA).
		{"remove", worktreeRemoveArgs(repo, path), []string{"-C", repo, "worktree", "remove", path}},
		{"prune", worktreePruneArgs(repo), []string{"-C", repo, "worktree", "prune"}},
		{"list", worktreeListPorcelainArgs(repo), []string{"-C", repo, "worktree", "list", "--porcelain"}},
		{"remote get-url", remoteGetURLOriginArgs(repo), []string{"-C", repo, "remote", "get-url", "origin"}},
		{"fetch origin quiet", fetchOriginQuietArgs(repo), []string{"-C", repo, "fetch", "origin", "--quiet"}},
		{"remove force", worktreeRemoveForceArgs(repo, path), []string{"-C", repo, "worktree", "remove", "--force", path}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if !reflect.DeepEqual(tc.got, tc.want) {
				t.Fatalf("args = %#v, want %#v", tc.got, tc.want)
			}
		})
	}
}

func TestBaseRefCandidatesWithOrigin(t *testing.T) {
	got := baseRefCandidates("feature/test", "main", true)
	want := []string{"origin/feature/test", "origin/main", "feature/test"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("candidates = %#v, want %#v", got, want)
	}

	got = baseRefCandidates("feature/test", "upstream/main", true)
	want = []string{"origin/feature/test", "upstream/main", "feature/test"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("qualified candidates = %#v, want %#v", got, want)
	}
}

func TestBaseRefCandidatesWithoutOrigin(t *testing.T) {
	got := baseRefCandidates("feature/test", "main", false)
	want := []string{"feature/test"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("no-origin plain default = %#v, want %#v", got, want)
	}

	got = baseRefCandidates("feature/test", "upstream/main", false)
	want = []string{"upstream/main", "feature/test"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("no-origin qualified default = %#v, want %#v", got, want)
	}
}

func TestHasOriginRemote(t *testing.T) {
	ws, err := New(Options{ManagedRoot: t.TempDir(), RepoResolver: StaticRepoResolver{"p": "/repo"}})
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	var lastArgs []string
	ws.run = func(_ context.Context, _ string, args ...string) ([]byte, error) {
		lastArgs = append([]string{}, args...)
		return []byte("git@github.com:foo/bar.git\n"), nil
	}
	if !ws.hasOriginRemote(context.Background(), "/repo") {
		t.Fatal("expected true when remote get-url succeeds")
	}
	if want := []string{"-C", "/repo", "remote", "get-url", "origin"}; !reflect.DeepEqual(lastArgs, want) {
		t.Fatalf("args = %#v, want %#v", lastArgs, want)
	}
	ws.run = func(context.Context, string, ...string) ([]byte, error) {
		return nil, errors.New("fatal: no such remote 'origin'")
	}
	if ws.hasOriginRemote(context.Background(), "/repo") {
		t.Fatal("expected false when remote get-url fails")
	}
}

func TestParseWorktreePorcelain(t *testing.T) {
	input := strings.Join([]string{
		"worktree /repo",
		"HEAD abc123",
		"branch refs/heads/main",
		"",
		"worktree /managed/proj/sess1",
		"HEAD def456",
		"branch refs/heads/feature/test",
		"",
		"worktree /managed/proj/sess2",
		"HEAD 789abc",
		"detached",
		"",
		"worktree /bare",
		"bare",
		"",
	}, "\n")

	recs, err := parseWorktreePorcelain(input)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(recs) != 4 {
		t.Fatalf("len = %d, want 4: %#v", len(recs), recs)
	}
	if recs[1].Path != "/managed/proj/sess1" || recs[1].Branch != "feature/test" {
		t.Fatalf("normal record = %#v", recs[1])
	}
	if !recs[2].Detached || recs[2].Branch != "" {
		t.Fatalf("detached record = %#v", recs[2])
	}
	if !recs[3].Bare {
		t.Fatalf("bare record = %#v", recs[3])
	}
}

func TestManagedPathSafety(t *testing.T) {
	root := t.TempDir()
	ws, err := New(Options{ManagedRoot: root, RepoResolver: StaticRepoResolver{"proj": root}})
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	path, err := ws.managedPath("proj", "sess")
	if err != nil {
		t.Fatalf("managed path: %v", err)
	}
	if want := filepath.Join(ws.managedRoot, "proj", "sess"); path != want {
		t.Fatalf("path = %q, want %q", path, want)
	}
	if _, err := ws.validateManagedPath(filepath.Join(root, "..", "outside")); !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("outside error = %v, want ErrUnsafePath", err)
	}
	if _, err := ws.validateManagedPath("relative/path"); !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("relative error = %v, want ErrUnsafePath", err)
	}
}

// TestValidateConfigRejectsPathEscapingIDs covers review item RB: filepath.Join
// in managedPath cleans `..` segments before validateManagedPath sees them, so a
// session id of "../other" would stay inside managedRoot while jumping projects.
// validateConfig must reject these at the source — before any path is composed.
func TestValidateConfigRejectsPathEscapingIDs(t *testing.T) {
	root := t.TempDir()
	ws, err := New(Options{ManagedRoot: root, RepoResolver: StaticRepoResolver{"proj": root}})
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	cases := []struct {
		name string
		cfg  ports.WorkspaceConfig
	}{
		{"session contains slash escapes project root", ports.WorkspaceConfig{ProjectID: "proj", SessionID: "../other", Branch: "main"}},
		{"session is .. is rejected", ports.WorkspaceConfig{ProjectID: "proj", SessionID: "..", Branch: "main"}},
		{"session is . is rejected", ports.WorkspaceConfig{ProjectID: "proj", SessionID: ".", Branch: "main"}},
		{"session contains backslash is rejected", ports.WorkspaceConfig{ProjectID: "proj", SessionID: `evil\sess`, Branch: "main"}},
		{"project contains slash escapes managed root", ports.WorkspaceConfig{ProjectID: "../proj", SessionID: "sess", Branch: "main"}},
		{"project is .. is rejected", ports.WorkspaceConfig{ProjectID: "..", SessionID: "sess", Branch: "main"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Create rejects it directly through validateConfig.
			if _, err := ws.Create(context.Background(), tc.cfg); !errors.Is(err, ErrUnsafePath) {
				t.Fatalf("Create err = %v, want ErrUnsafePath", err)
			}
			// Restore also goes through validateConfig, so the same guarantee holds.
			if _, err := ws.Restore(context.Background(), tc.cfg); !errors.Is(err, ErrUnsafePath) {
				t.Fatalf("Restore err = %v, want ErrUnsafePath", err)
			}
		})
	}
}

// TestValidateConfigAcceptsBenignIDs is a positive guard so the rejection rule
// above does not creep into normal session/project naming. Hyphens, underscores,
// dots inside (e.g. "foo.bar"), and digits all stay allowed.
func TestValidateConfigAcceptsBenignIDs(t *testing.T) {
	cases := []ports.WorkspaceConfig{
		{ProjectID: "proj-1", SessionID: "sess_2", Branch: "main"},
		{ProjectID: "foo.bar", SessionID: "abc-42", Branch: "main"},
		{ProjectID: "p", SessionID: "..hidden", Branch: "main"}, // leading dots != ".."
	}
	for i, cfg := range cases {
		if err := validateConfig(cfg); err != nil {
			t.Errorf("case %d %+v: unexpected error: %v", i, cfg, err)
		}
	}
}

func TestRestoreAutoCleansUnregisteredResidueAndProceeds(t *testing.T) {
	root := t.TempDir()
	repo := t.TempDir()
	ws, err := New(Options{ManagedRoot: root, RepoResolver: StaticRepoResolver{"proj": repo}})
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	path := filepath.Join(ws.managedRoot, "proj", "sess")
	if err := mkdirFile(path, "leftover.txt"); err != nil {
		t.Fatalf("seed path: %v", err)
	}
	ws.run = func(_ context.Context, _ string, args ...string) ([]byte, error) {
		joined := strings.Join(args, " ")
		switch {
		case strings.Contains(joined, "worktree list --porcelain"):
			// Residue is NOT registered.
			return []byte("worktree " + repo + "\nbranch refs/heads/main\n"), nil
		case strings.Contains(joined, "check-ref-format"):
			return nil, nil
		case strings.Contains(joined, "remote get-url origin"):
			return nil, errors.New("no origin")
		case strings.Contains(joined, "rev-parse --verify --quiet refs/heads/feature/one"):
			return nil, &exec.ExitError{ProcessState: stubExitState(1)}
		case strings.Contains(joined, "rev-parse --verify --quiet"):
			return []byte("abc\n"), nil
		case strings.Contains(joined, "worktree add"):
			return nil, nil
		default:
			return nil, nil
		}
	}
	info, err := ws.Restore(context.Background(), ports.WorkspaceConfig{ProjectID: "proj", SessionID: "sess", Branch: "feature/one"})
	if err != nil {
		t.Fatalf("restore: %v", err)
	}
	if info.Path != path {
		t.Fatalf("info.Path = %q, want %q", info.Path, path)
	}
	if _, statErr := os.Stat(filepath.Join(path, "leftover.txt")); !os.IsNotExist(statErr) {
		t.Fatalf("expected leftover.txt to be removed, stat err = %v", statErr)
	}
}

func TestRestoreStillRefusesRegisteredResidue(t *testing.T) {
	// Safety invariant: even if path is non-empty, if it IS a registered
	// worktree we MUST NOT rmSync it (would destroy registered work).
	// The findWorktree shortcut should catch this and return the registered
	// info, never reaching the residue branch.
	root := t.TempDir()
	repo := t.TempDir()
	ws, err := New(Options{ManagedRoot: root, RepoResolver: StaticRepoResolver{"proj": repo}})
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	path := filepath.Join(ws.managedRoot, "proj", "sess")
	if err := mkdirFile(path, "agent-work.txt"); err != nil {
		t.Fatalf("seed path: %v", err)
	}
	ws.run = func(context.Context, string, ...string) ([]byte, error) {
		// Path IS registered — findWorktree shortcut should fire.
		return []byte("worktree " + path + "\nbranch refs/heads/feature/one\n"), nil
	}
	info, err := ws.Restore(context.Background(), ports.WorkspaceConfig{ProjectID: "proj", SessionID: "sess", Branch: "feature/one"})
	if err != nil {
		t.Fatalf("restore registered: %v", err)
	}
	if info.Branch != "feature/one" {
		t.Fatalf("info.Branch = %q, want feature/one", info.Branch)
	}
	if _, statErr := os.Stat(filepath.Join(path, "agent-work.txt")); statErr != nil {
		t.Fatalf("agent-work.txt MUST be preserved (data-loss guard): %v", statErr)
	}
}

func TestDestroyRefusesStillRegisteredPathAndPreservesDirectory(t *testing.T) {
	root := t.TempDir()
	repo := t.TempDir()
	ws, err := New(Options{ManagedRoot: root, RepoResolver: StaticRepoResolver{"proj": repo}})
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	path := filepath.Join(ws.managedRoot, "proj", "sess")
	if err := mkdirFile(path, "keep.txt"); err != nil {
		t.Fatalf("seed path: %v", err)
	}
	var removeArgs []string
	ws.run = func(_ context.Context, _ string, args ...string) ([]byte, error) {
		joined := strings.Join(args, " ")
		switch {
		case strings.Contains(joined, "worktree remove"):
			removeArgs = append([]string{}, args...)
			return []byte("locked"), errors.New("remove failed")
		case strings.Contains(joined, "worktree prune"):
			return nil, nil
		case strings.Contains(joined, "worktree list --porcelain"):
			return []byte("worktree " + path + "\nbranch refs/heads/feature/one\n"), nil
		default:
			return nil, nil
		}
	}
	err = ws.Destroy(context.Background(), ports.WorkspaceInfo{Path: path, ProjectID: "proj", SessionID: "sess", Branch: "feature/one"})
	if err == nil || !strings.Contains(err.Error(), "still registered") {
		t.Fatalf("destroy error = %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(path, "keep.txt")); statErr != nil {
		t.Fatalf("expected directory to be preserved: %v", statErr)
	}
	// Belt-and-braces: --force must NEVER be passed to `git worktree remove` from
	// Destroy. If it ever is, dirty worktrees would be deleted instead of routed
	// to Skipped by the Session Manager's Cleanup (review item RA).
	for _, a := range removeArgs {
		if a == "--force" || a == "-f" {
			t.Fatalf("git worktree remove was called with %q; --force must never be passed", a)
		}
	}
}

func mkdirFile(dir, name string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, name), []byte("data"), 0o644)
}

func containsArgs(calls [][]string, sub []string) bool {
	return indexOfArgs(calls, sub) >= 0
}

func indexOfArgs(calls [][]string, sub []string) int {
	for i, c := range calls {
		joined := strings.Join(c, " ")
		if strings.Contains(joined, strings.Join(sub, " ")) {
			return i
		}
	}
	return -1
}

// stubExitState spawns a tiny shell command that exits with the requested
// code so the returned *os.ProcessState carries a real exit code — avoids
// constructing os/exec internals. Depends on a POSIX `sh` in PATH; if/when
// Windows CI is added this needs a platform branch.
func stubExitState(code int) *os.ProcessState {
	cmd := exec.Command("sh", "-c", fmt.Sprintf("exit %d", code))
	_ = cmd.Run()
	return cmd.ProcessState
}

func TestCreateFetchesOriginWhenPresent(t *testing.T) {
	ws, err := New(Options{ManagedRoot: t.TempDir(), RepoResolver: StaticRepoResolver{"proj": "/repo"}})
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	var calls [][]string
	ws.run = func(_ context.Context, _ string, args ...string) ([]byte, error) {
		calls = append(calls, append([]string{}, args...))
		joined := strings.Join(args, " ")
		switch {
		case strings.Contains(joined, "check-ref-format"):
			return nil, nil
		case strings.Contains(joined, "remote get-url origin"):
			return []byte("origin\n"), nil
		case strings.Contains(joined, "fetch origin --quiet"):
			return nil, nil
		case strings.Contains(joined, "rev-parse --verify --quiet refs/heads/feature/one"):
			return nil, &exec.ExitError{ProcessState: stubExitState(1)}
		case strings.Contains(joined, "rev-parse --verify --quiet"):
			return []byte("abc\n"), nil
		case strings.Contains(joined, "worktree add"):
			return nil, nil
		default:
			return nil, nil
		}
	}
	_, err = ws.Create(context.Background(), ports.WorkspaceConfig{ProjectID: "proj", SessionID: "sess", Branch: "feature/one"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	fetchIdx := indexOfArgs(calls, []string{"fetch", "origin", "--quiet"})
	addIdx := indexOfArgs(calls, []string{"worktree", "add"})
	if fetchIdx < 0 {
		t.Fatalf("expected git fetch origin --quiet call; calls=%v", calls)
	}
	if addIdx < 0 {
		t.Fatalf("expected git worktree add call; calls=%v", calls)
	}
	if fetchIdx > addIdx {
		t.Fatalf("expected fetch before worktree add: fetchIdx=%d addIdx=%d", fetchIdx, addIdx)
	}
}

func TestCreateSkipsFetchWhenNoOrigin(t *testing.T) {
	ws, err := New(Options{ManagedRoot: t.TempDir(), RepoResolver: StaticRepoResolver{"proj": "/repo"}})
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	var calls [][]string
	ws.run = func(_ context.Context, _ string, args ...string) ([]byte, error) {
		calls = append(calls, append([]string{}, args...))
		joined := strings.Join(args, " ")
		switch {
		case strings.Contains(joined, "check-ref-format"):
			return nil, nil
		case strings.Contains(joined, "remote get-url origin"):
			return nil, errors.New("fatal: no such remote 'origin'")
		case strings.Contains(joined, "rev-parse --verify --quiet refs/heads/feature/one"):
			return nil, &exec.ExitError{ProcessState: stubExitState(1)}
		case strings.Contains(joined, "rev-parse --verify --quiet"):
			return []byte("abc\n"), nil
		case strings.Contains(joined, "worktree add"):
			return nil, nil
		default:
			return nil, nil
		}
	}
	_, err = ws.Create(context.Background(), ports.WorkspaceConfig{ProjectID: "proj", SessionID: "sess", Branch: "feature/one"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if containsArgs(calls, []string{"fetch", "origin", "--quiet"}) {
		t.Fatalf("expected no fetch when origin absent; calls=%v", calls)
	}
}

func TestCreateContinuesWhenFetchFails(t *testing.T) {
	ws, err := New(Options{ManagedRoot: t.TempDir(), RepoResolver: StaticRepoResolver{"proj": "/repo"}})
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	ws.run = func(_ context.Context, _ string, args ...string) ([]byte, error) {
		joined := strings.Join(args, " ")
		switch {
		case strings.Contains(joined, "check-ref-format"):
			return nil, nil
		case strings.Contains(joined, "remote get-url origin"):
			return []byte("origin\n"), nil
		case strings.Contains(joined, "fetch origin --quiet"):
			return nil, errors.New("could not resolve host")
		case strings.Contains(joined, "rev-parse --verify --quiet refs/heads/feature/one"):
			return nil, &exec.ExitError{ProcessState: stubExitState(1)}
		case strings.Contains(joined, "rev-parse --verify --quiet"):
			return []byte("abc\n"), nil
		case strings.Contains(joined, "worktree add"):
			return nil, nil
		default:
			return nil, nil
		}
	}
	if _, err := ws.Create(context.Background(), ports.WorkspaceConfig{ProjectID: "proj", SessionID: "sess", Branch: "feature/one"}); err != nil {
		t.Fatalf("create with failing fetch: %v", err)
	}
}

func TestCreateCleansUpOrphanWhenAddFails(t *testing.T) {
	root := t.TempDir()
	repo := t.TempDir()
	ws, err := New(Options{ManagedRoot: root, RepoResolver: StaticRepoResolver{"proj": repo}})
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	var calls [][]string
	ws.run = func(_ context.Context, _ string, args ...string) ([]byte, error) {
		calls = append(calls, append([]string{}, args...))
		joined := strings.Join(args, " ")
		switch {
		case strings.Contains(joined, "check-ref-format"):
			return nil, nil
		case strings.Contains(joined, "remote get-url origin"):
			return nil, errors.New("no origin")
		case strings.Contains(joined, "rev-parse --verify --quiet refs/heads/feature/one"):
			return nil, &exec.ExitError{ProcessState: stubExitState(1)}
		case strings.Contains(joined, "rev-parse --verify --quiet"):
			return []byte("abc\n"), nil
		case strings.Contains(joined, "worktree add"):
			return nil, errors.New("fatal: <path> already exists")
		case strings.Contains(joined, "worktree remove --force"):
			return nil, nil
		default:
			return nil, nil
		}
	}
	_, err = ws.Create(context.Background(), ports.WorkspaceConfig{ProjectID: "proj", SessionID: "sess", Branch: "feature/one"})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "worktree add") {
		t.Fatalf("error should mention the add operation; got: %v", err)
	}
	if indexOfArgs(calls, []string{"worktree", "remove", "--force"}) < 0 {
		t.Fatalf("expected worktree remove --force cleanup; calls=%v", calls)
	}
}

func TestCreateCleanupSwallowsSecondaryError(t *testing.T) {
	root := t.TempDir()
	repo := t.TempDir()
	ws, err := New(Options{ManagedRoot: root, RepoResolver: StaticRepoResolver{"proj": repo}})
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	ws.run = func(_ context.Context, _ string, args ...string) ([]byte, error) {
		joined := strings.Join(args, " ")
		switch {
		case strings.Contains(joined, "check-ref-format"):
			return nil, nil
		case strings.Contains(joined, "remote get-url origin"):
			return nil, errors.New("no origin")
		case strings.Contains(joined, "rev-parse --verify --quiet refs/heads/feature/one"):
			return nil, &exec.ExitError{ProcessState: stubExitState(1)}
		case strings.Contains(joined, "rev-parse --verify --quiet"):
			return []byte("abc\n"), nil
		case strings.Contains(joined, "worktree add"):
			return nil, errors.New("fatal: add failed")
		case strings.Contains(joined, "worktree remove --force"):
			return nil, errors.New("fatal: cleanup also failed")
		default:
			return nil, nil
		}
	}
	_, err = ws.Create(context.Background(), ports.WorkspaceConfig{ProjectID: "proj", SessionID: "sess", Branch: "feature/one"})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "add failed") {
		t.Fatalf("original add error should surface; got: %v", err)
	}
}

func TestCreateCleanupRunsWithFreshContextWhenCallerCancelled(t *testing.T) {
	// When the caller's ctx is cancelled mid-add (e.g. HTTP request
	// aborted), cleanupOrphan MUST run with a fresh context. Reusing the
	// cancelled caller ctx — directly or via withDefaultTimeout — yields
	// an already-cancelled child, the `worktree remove --force` subprocess
	// is killed before it runs, and the orphan that cleanupOrphan exists
	// to prevent is left on disk.
	root := t.TempDir()
	repo := t.TempDir()
	ws, err := New(Options{ManagedRoot: root, RepoResolver: StaticRepoResolver{"proj": repo}})
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	var (
		cleanupCalled bool
		cleanupCtxErr error
	)
	ws.run = func(callerCtx context.Context, _ string, args ...string) ([]byte, error) {
		joined := strings.Join(args, " ")
		switch {
		case strings.Contains(joined, "check-ref-format"):
			return nil, nil
		case strings.Contains(joined, "remote get-url origin"):
			return nil, errors.New("no origin")
		case strings.Contains(joined, "rev-parse --verify --quiet refs/heads/feature/one"):
			return nil, &exec.ExitError{ProcessState: stubExitState(1)}
		case strings.Contains(joined, "rev-parse --verify --quiet"):
			return []byte("abc\n"), nil
		case strings.Contains(joined, "worktree add"):
			return nil, errors.New("fatal: add failed")
		case strings.Contains(joined, "worktree remove --force"):
			cleanupCalled = true
			cleanupCtxErr = callerCtx.Err()
			return nil, nil
		default:
			return nil, nil
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, _ = ws.Create(ctx, ports.WorkspaceConfig{ProjectID: "proj", SessionID: "sess", Branch: "feature/one"})
	if !cleanupCalled {
		t.Fatal("cleanup must run even when caller ctx is cancelled")
	}
	if cleanupCtxErr != nil {
		t.Fatalf("cleanup ctx must be fresh, got Err()=%v", cleanupCtxErr)
	}
}

func TestCreateCleansUpOrphanWhenAddFailsExistingBranch(t *testing.T) {
	// Covers the addWorktree localBranch=true path: refs/heads/<branch> exists,
	// so we skip resolveBaseRef and call `worktree add <path> <branch>` (no -b).
	// If that fails, cleanupOrphan must still fire.
	root := t.TempDir()
	repo := t.TempDir()
	ws, err := New(Options{ManagedRoot: root, RepoResolver: StaticRepoResolver{"proj": repo}})
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	var calls [][]string
	ws.run = func(_ context.Context, _ string, args ...string) ([]byte, error) {
		calls = append(calls, append([]string{}, args...))
		joined := strings.Join(args, " ")
		switch {
		case strings.Contains(joined, "check-ref-format"):
			return nil, nil
		case strings.Contains(joined, "remote get-url origin"):
			return nil, errors.New("no origin")
		case strings.Contains(joined, "rev-parse --verify --quiet refs/heads/feature/one"):
			// Local branch DOES exist — drives addWorktree into the
			// existing-branch path (worktree add <path> <branch>, no -b).
			return []byte("abc\n"), nil
		case strings.Contains(joined, "worktree add"):
			return nil, errors.New("fatal: <path> already exists")
		case strings.Contains(joined, "worktree remove --force"):
			return nil, nil
		default:
			return nil, nil
		}
	}
	_, err = ws.Create(context.Background(), ports.WorkspaceConfig{ProjectID: "proj", SessionID: "sess", Branch: "feature/one"})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "worktree add existing branch") {
		t.Fatalf("error should mention existing-branch add; got: %v", err)
	}
	if indexOfArgs(calls, []string{"worktree", "remove", "--force"}) < 0 {
		t.Fatalf("expected worktree remove --force cleanup; calls=%v", calls)
	}
	addIdx := indexOfArgs(calls, []string{"worktree", "add"})
	if addIdx < 0 {
		t.Fatal("no worktree add call recorded")
	}
	for _, a := range calls[addIdx] {
		if a == "-b" {
			t.Fatalf("localBranch=true path must not pass -b; calls[%d]=%v", addIdx, calls[addIdx])
		}
	}
}
