export type SessionStatus = "running" | "needs_input" | "stopped" | "failed";

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

export type WorkspaceSession = {
  id: string;
  workspaceId: string;
  workspaceName: string;
  title: string;
  provider: AgentProvider;
  branch: string;
  status: SessionStatus;
  updatedAt: string;
};

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
