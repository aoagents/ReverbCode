import type { WorkspaceSummary } from "../types/workspace";

export const mockWorkspaces: WorkspaceSummary[] = [
	{
		id: "api-gateway",
		name: "api-gateway",
		path: "/Users/me/api-gateway",
		sessions: [
			{
				id: "refactor-mux",
				terminalHandleId: "refactor-mux/terminal_0",
				workspaceId: "api-gateway",
				workspaceName: "api-gateway",
				title: "refactor-mux",
				provider: "claude-code",
				branch: "feat/refactor-mux",
				status: "working",
				updatedAt: "now",
				changedFiles: [
					{
						path: "internal/mux/terminal_mux.go",
						additions: 42,
						deletions: 8,
					},
				],
				commitMessage: "refactor terminal mux",
			},
		],
	},
	{
		id: "webgl-preview",
		name: "webgl-preview",
		path: "/Users/me/webgl-preview",
		sessions: [
			{
				id: "fix-webgl-fallback",
				workspaceId: "webgl-preview",
				workspaceName: "webgl-preview",
				title: "fix-webgl-fallback",
				provider: "codex",
				branch: "fix/webgl-fallback",
				status: "needs_input",
				updatedAt: "now",
			},
		],
	},
];
