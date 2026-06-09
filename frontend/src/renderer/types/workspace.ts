type SessionStatus = "running" | "needs_input" | "stopped" | "failed";

export type AgentProvider =
  | "codex"
  | "claude-code"
  | "opencode"
  | "aider"
  | "grok"
  | "droid"
  | "amp"
  | "agy"
  | "crush"
  | "cursor"
  | "qwen"
  | "copilot"
  | "goose"
  | "auggie"
  | "continue"
  | "devin"
  | "cline"
  | "kimi"
  | "kiro"
  | "kilocode"
  | "vibe"
  | "pi"
  | "autohand";

/** A file in a worker's worktree diff (drives the Git review rail). */
export type ChangedFile = {
  path: string;
  additions: number;
  deletions: number;
  staged?: boolean;
};

export type WorkspaceSession = {
  id: string;
  workspaceId: string;
  workspaceName: string;
  title: string;
  provider: AgentProvider;
  branch: string;
  status: SessionStatus;
  updatedAt: string;
  /** The session's git diff against its base, when known. */
  changedFiles?: ChangedFile[];
  /** Pre-filled commit subject for the Git rail, when known. */
  commitMessage?: string;
  pullRequest?: {
    number: number;
    state: "open" | "draft" | "merged" | "closed";
  };
  /**
   * Display status as derived by the daemon at read time. Optional override; when
   * absent it is derived from {@link SessionStatus} via {@link workerDisplayStatus}.
   */
  displayStatus?: WorkerDisplayStatus;
};

/** Glanceable worker status. Maps 1:1 to the accent colors in DESIGN.md. */
export type WorkerDisplayStatus = "working" | "needs_you" | "mergeable" | "ci_failed" | "done";

export function workerDisplayStatus(session: WorkspaceSession): WorkerDisplayStatus {
  if (session.displayStatus) return session.displayStatus;
  switch (session.status) {
    case "running":
      return "working";
    case "needs_input":
      return "needs_you";
    case "failed":
      return "ci_failed";
    default:
      return "done";
  }
}

export const workerStatusLabel: Record<WorkerDisplayStatus, string> = {
  working: "working",
  needs_you: "needs you",
  mergeable: "mergeable",
  ci_failed: "ci failed",
  done: "done",
};

/** Whether a status should breathe (alive/working). */
export function workerStatusPulses(status: WorkerDisplayStatus): boolean {
  return status === "working" || status === "needs_you";
}

export type WorkspaceSummary = {
  id: string;
  name: string;
  path: string;
  type?: "main" | "worktree";
  accentColor?: string;
  diff?: {
    additions: number;
    deletions: number;
  };
  pullRequest?: {
    number: number;
    state: "open" | "draft" | "merged" | "closed";
  };
  sessions: WorkspaceSession[];
};

export function toAgentProvider(provider?: string): AgentProvider {
  switch (provider) {
    case "claude-code":
    case "opencode":
    case "aider":
    case "grok":
    case "droid":
    case "amp":
    case "agy":
    case "crush":
    case "cursor":
    case "qwen":
    case "copilot":
    case "goose":
    case "auggie":
    case "continue":
    case "devin":
    case "cline":
    case "kimi":
    case "kiro":
    case "kilocode":
    case "vibe":
    case "pi":
    case "autohand":
      return provider;
    default:
      return "codex";
  }
}
