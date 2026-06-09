import type { WorkspaceSummary } from "../types/workspace";

export const mockWorkspaces: WorkspaceSummary[] = [
  {
    id: "agent-orchestrator",
    name: "agent-orchestrator-1",
    path: "/Users/ashishhuddar/agent-orchestrator-1",
    type: "main",
    accentColor: "#7dd3fc",
    diff: { additions: 68, deletions: 14 },
    pullRequest: { number: 156, state: "open" },
    sessions: [
      {
        id: "ao-shell-scaffold",
        workspaceId: "agent-orchestrator",
        workspaceName: "agent-orchestrator-1",
        title: "Desktop shell scaffold",
        provider: "codex",
        branch: "codex/electron-stack-scaffold",
        status: "running",
        updatedAt: "now",
      },
      {
        id: "ao-api-contract",
        workspaceId: "agent-orchestrator",
        workspaceName: "agent-orchestrator-1",
        title: "Daemon bridge wiring",
        provider: "opencode",
        branch: "main",
        status: "needs_input",
        updatedAt: "12m",
      },
    ],
  },
  {
    id: "vinesight-web",
    name: "vinesight-web",
    path: "/Users/ashishhuddar/vinesight-web",
    type: "worktree",
    diff: { additions: 31, deletions: 7 },
    sessions: [
      {
        id: "consultant-access",
        workspaceId: "vinesight-web",
        workspaceName: "vinesight-web",
        title: "Consultant access review",
        provider: "claude-code",
        branch: "feature/consultant-access",
        status: "stopped",
        updatedAt: "1h",
      },
    ],
  },
];
