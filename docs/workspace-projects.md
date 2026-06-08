# Workspace projects: provisioning deep dive

This note expands the scoping discussion for supporting one AO project backed by
multiple sibling git repositories under one parent folder. It focuses on the two
realistic provisioning choices:

1. **Composed child worktrees**: keep the parent as a plain folder and run
   `git worktree add` inside each targeted child repository.
2. **Parent repo with submodules**: initialize or use a parent git repository,
   register each child as a submodule, then create session worktrees from the
   parent repository.

The current recommendation is **Option 1 for the first implementation**. Option
2 is viable only when the user intentionally wants submodule semantics for the
workspace itself; it should not be AO's default conversion path.

## Current AO shape that constrains the design

AO currently assumes one registered project points at one git repository:

- `projects` stores one `path` and one `repo_origin_url`.
- `domain.ProjectRecord` mirrors that single-repo row.
- `ports.WorkspaceConfig` contains `ProjectID`, `SessionID`, and one `Branch`.
- `ports.WorkspaceInfo` returns one workspace `Path` and one `Branch`.
- `gitworktree.Workspace` resolves `ProjectID -> repo path`, then creates one
  managed worktree at `<managed-root>/<project>/<session>`.
- `domain.SessionMetadata` stores one `branch` and one `workspace_path`.
- `session_manager.Manager` calls `workspace.Create` once during spawn and
  `workspace.Destroy` once during kill/cleanup.
- The SCM observer can remain session-centric because PR ownership is already
  `PR -> session`; workspace support mainly changes how projects expose their
  repositories to the observer.

Workspace support therefore cannot be only a project-registration tweak. It
needs a workspace-aware project model and a workspace adapter response that can
represent one composite session root plus N child git worktrees.

## Terms

- **Single-repo project**: current project shape; `project.path` is a git repo.
- **Workspace project**: a project whose canonical path is a parent folder
  containing registered child git repositories.
- **Workspace repo**: one named child repository under a workspace project, such
  as `cli`, `api`, or `pf`.
- **Session target**: one workspace repo selected for a session.
- **Composite workspace**: the session root directory handed to the agent. For a
  multi-target workspace session it contains root context files plus selected
  target directories, each target directory being a real git checkout/worktree.

## Option 1: composed child worktrees

Shape:

```txt
canonical/project-abc/          # may be plain, non-git parent
  package.json                  # root context file, not versioned by AO
  cli/                          # git repo
  api/                          # git repo

managed/project-abc/project-abc-7/
  package.json                  # copied root context file
  cli/                          # git worktree of canonical cli
  api/                          # git worktree of canonical api
```

For each selected target, AO runs the equivalent of:

```bash
git -C <canonical>/<target> worktree add <session-root>/<target> <branch>
```

### Why this fits AO better

- It extends the existing `gitworktree` adapter instead of introducing a second
  source-control model.
- It preserves native git worktree behavior: shared object database, normal
  credentials/hooks/LFS/submodule behavior inside each child repo, and cheap
  provisioning.
- It keeps the parent folder non-invasive. AO does not write `.git/`,
  `.gitmodules`, or commits into the user's workspace root.
- Per-repo PRs stay natural. Each target repo has its own origin and branch, and
  the current `PR -> session` attribution still works.
- Cleanup remains worktree-based: remove each child worktree without `--force`,
  then prune. If one target refuses removal because it is dirty or locked, the
  composite cleanup is skipped/partial and retried later, matching AO's current
  safety posture.

### Costs and required AO changes

- AO, not git, owns the composition. The DB must record which child worktrees
  make up a composite workspace.
- The agent's cwd is a non-git parent containing child git repos. Agent adapters
  and prompts must not assume `WorkspacePath` itself is a git repo.
- Root files outside child repos are not protected by git. They need an explicit
  policy; otherwise cleanup can silently delete changes or concurrent sessions
  can overwrite each other.
- Branch operations are per target. The first cut should use one shared branch
  name across selected targets (`ao/<session-id>`), while storing the branch per
  target so per-target branches remain possible later.
- If a user manually removes one child worktree, restore/destroy must detect a
  partially broken composite rather than treating the session as healthy.

### Root-file policy for first implementation

Root files in a non-git parent should be **context snapshots, not synchronized
outputs**:

1. At workspace creation, copy root-level files and directories that are not
   registered workspace repos and not `.git` into the composite root.
2. Mark copied files/directories read-only as a best-effort signal.
3. Store a small manifest of copied root paths and hashes for the session.
4. Do not copy session root modifications back to the canonical parent.
5. On cleanup, compare the manifest. If root context files changed, skip cleanup
   with a clear "unsupported root edits" reason rather than deleting them.
6. If root files must be edited as durable project output, require the parent to
   be a real git repo and model it as another target/repo instead of relying on
   copy-back semantics.

This avoids last-write-wins and preserves AO's hard rule not to destroy dirty
workspaces. Permissions alone are insufficient because the agent runs as the
same user and can change modes back.

## Option 2: parent repo with submodules

Shape:

```txt
canonical/project-abc/          # AO or user initializes this as a git repo
  .gitmodules
  package.json
  cli/                          # submodule gitlink
  api/                          # submodule gitlink

managed/project-abc/project-abc-7/
  package.json                  # versioned by parent
  cli/                          # initialized submodule checkout
  api/                          # initialized submodule checkout
```

Provisioning is roughly:

```bash
git -C <parent> worktree add <session-root> <parent-branch>
git -C <session-root> -c protocol.file.allow=always submodule update --init --recursive
```

### What it solves

- Root files become versioned in the parent repo, so the root-file conflict
  problem moves into git.
- The composite workspace is a single top-level git artifact.
- A parent commit can pin the exact child repo SHAs for reproducibility.

### Why it should not be the default

- It violates the already-discussed metadata direction: AO DB only, no AO-owned
  metadata in the parent folder. `.gitmodules` and gitlink commits become
  user-visible workspace metadata.
- It is invasive. Registering a plain folder would create `.git/`, `.gitmodules`,
  index entries, and commits in a directory the user did not necessarily intend
  to make a repo.
- It imposes submodule UX: detached HEADs after update, gitlink bumps whenever a
  child advances, and "submodule out of sync" states.
- Local sibling repositories require file-protocol allowances in common flows
  (`protocol.file.allow=always`). That is awkward to explain and easy to miss.
- It does not actually remove the need for child-repo metadata. If AO opens PRs
  against `cli` and `api`, the SCM observer still needs each child repo's origin,
  branch, and PR linkage.
- Session branch semantics split into parent branch and child branches. A single
  `sessions.branch` no longer describes the work.
- If AO opens only a parent PR with gitlink bumps, reviewers cannot review the
  child code changes in the usual repo PRs. If AO opens child PRs, parent
  gitlink bumps become extra bookkeeping.

Option 2 is useful only when the team already wants a versioned workspace
manifest and accepts submodule workflows. AO can later support that as an
advanced registration mode or by treating an existing parent repo as a normal
single-repo project with submodules. It should not be the first workspace
implementation.

## Schema direction

Prefer explicit tables over a JSON column because session-target lookups and SCM
observer enumeration are first-class operations.

Minimum durable additions:

```sql
ALTER TABLE projects ADD COLUMN kind TEXT NOT NULL DEFAULT 'single_repo'
  CHECK (kind IN ('single_repo', 'workspace'));

CREATE TABLE workspace_repos (
  project_id      TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
  name            TEXT NOT NULL,
  relative_path   TEXT NOT NULL,
  repo_origin_url TEXT NOT NULL DEFAULT '',
  registered_at   TIMESTAMP NOT NULL,
  PRIMARY KEY (project_id, name),
  UNIQUE (project_id, relative_path)
);

CREATE TABLE session_targets (
  session_id    TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
  repo_name     TEXT NOT NULL,
  branch        TEXT NOT NULL,
  worktree_path TEXT NOT NULL,
  PRIMARY KEY (session_id, repo_name)
);
```

Keep `sessions.workspace_path` as the composite root handed to the runtime and
agent. Keep `sessions.branch` as the default/shared branch for compatibility,
but treat `session_targets.branch` as authoritative for workspace projects.

If root context manifests are stored durably, add either a separate
`session_root_files` table or a metadata blob dedicated to cleanup safety. Do
not overload display status or activity state with root-file dirtiness.

## Port and adapter changes

Likely port changes:

```go
type WorkspaceConfig struct {
    ProjectID domain.ProjectID
    SessionID domain.SessionID
    Branch    string
    Targets   []string // empty means single-repo project or all/default targets by policy
}

type WorkspaceInfo struct {
    Path      string // composite root
    Branch    string // default/shared branch
    SessionID domain.SessionID
    ProjectID domain.ProjectID
    Targets   []WorkspaceTargetInfo
}

type WorkspaceTargetInfo struct {
    Name string
    Path string
    Branch string
}
```

`gitworktree.RepoResolver` should become a project/workspace resolver that can
return either:

- one single-repo path for `single_repo`, or
- a workspace root plus named child repos for `workspace`.

For single-repo projects, the adapter keeps today's exact behavior. For
workspace projects, it creates the composite root, copies root context files,
then creates one child worktree per selected target.

## Registration behavior

- `ao project add --path <git-repo>` remains the current single-repo flow.
- `ao project add --path <plain-folder>` without `--as-workspace` should return
  a typed error that includes detected child repos. The UI can use that to show
  the interactive "register whole folder or pick one" prompt.
- `ao project add --path <plain-folder> --as-workspace` registers a workspace
  project with every detected child git repo unless explicit target-selection
  flags are added later.
- A folder with no child git repos remains invalid and should suggest `git init`.
- Non-git child folders are skipped by the first CLI implementation. Offering
  to `git init` them is a UI flow or a later explicit CLI flag; the daemon should
  not mutate child folders implicitly.

## Spawn behavior

- Add `--targets cli,api` to the spawn API/CLI.
- For single-repo projects, `--targets` is invalid.
- For workspace projects, targets must name registered workspace repos.
- If targets are omitted, choose a product policy explicitly. The safest first
  cut is to require `--targets` for workspace projects so AO never accidentally
  provisions every repo in a large workspace.
- Use one shared branch name by default: `ao/<session-id>` in every selected
  target. Store per-target rows anyway.

## Cleanup and restore

Create/restore/destroy must be all-target aware:

- `Create`: create composite root, copy root context, then create each child
  worktree. If target N fails, remove already-created child worktrees and leave
  no registered half-session when possible.
- `Restore`: verify every target worktree still exists and is registered. If one
  is missing but its directory is empty, recreate it. If a path exists and is not
  the registered worktree, refuse restore.
- `Destroy`: call `git worktree remove` for every target without `--force`, prune
  each child repo, and only remove the composite root when every child removal
  succeeds and root context is unchanged.
- `Cleanup`: if one target refuses removal, preserve the composite root and
  report/skip the session for retry. Do not delete root files or sibling targets
  just because some targets cleaned up successfully.

## Implementation slices

1. **Project model and detection**
   - Add `projects.kind` and `workspace_repos`.
   - Detect direct child git repos during `project.Add`.
   - Add `--as-workspace` to CLI/API input.
   - Return workspace repo names in project get/list details.

2. **Session target schema**
   - Add `session_targets` and store methods.
   - Extend service/HTTP/CLI spawn DTOs with `targets`.
   - Validate target names before creating a workspace.

3. **Composite workspace adapter**
   - Extend `gitworktree.Workspace` to branch on project kind.
   - Reuse existing single-repo create/restore/destroy code for each child.
   - Add integration tests for two sibling repos, partial cleanup refusal, and
     restore after one child worktree is manually removed.

4. **Root context safety**
   - Copy root context files with a manifest.
   - Refuse cleanup when root context files changed.
   - Document that root edits require a git-backed parent/target.

5. **SCM observer enumeration**
   - Read workspace child origins from `workspace_repos`.
   - Preserve PR-to-session attribution. No per-subrepo activity source is
     needed.

## Decision checkpoint

Before coding past slice 1, confirm these product decisions:

1. Workspace spawn without `--targets`: reject, or default to all repos?
2. Root files: accept context-only snapshots with no writeback?
3. Branch naming: one shared `ao/<session-id>` branch in every target for V1?

If those answers are accepted, Option 1 is implementable as an incremental
extension of the current daemon. Option 2 should remain a documented advanced
alternative, not the default architecture.
