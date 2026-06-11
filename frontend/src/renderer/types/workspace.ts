export type SessionStatus =
	| "working"
	| "pr_open"
	| "draft"
	| "ci_failed"
	| "review_pending"
	| "changes_requested"
	| "approved"
	| "mergeable"
	| "merged"
	| "needs_input"
	| "idle"
	| "no_signal"
	| "terminated";

const sessionStatuses = new Set<SessionStatus>([
	"working",
	"pr_open",
	"draft",
	"ci_failed",
	"review_pending",
	"changes_requested",
	"approved",
	"mergeable",
	"merged",
	"needs_input",
	"idle",
	"no_signal",
	"terminated",
]);

export function toSessionStatus(status?: string, isTerminated = false): SessionStatus {
	if (isTerminated) return "terminated";
	return status && sessionStatuses.has(status as SessionStatus) ? (status as SessionStatus) : "working";
}

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
	terminalHandleId?: string;
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
		case "needs_input":
		case "changes_requested":
		case "review_pending":
		// no_signal: the daemon has never heard from this agent — a human
		// should look at the pane, so it surfaces as needs-you.
		case "no_signal":
			return "needs_you";
		case "ci_failed":
			return "ci_failed";
		case "approved":
		case "mergeable":
			return "mergeable";
		case "merged":
		case "terminated":
			return "done";
		default:
			return "working";
	}
}

export function sessionIsActive(session: WorkspaceSession): boolean {
	return session.status !== "merged" && session.status !== "terminated";
}

export function sessionNeedsAttention(session: WorkspaceSession): boolean {
	return (
		session.status === "needs_input" ||
		session.status === "changes_requested" ||
		session.status === "review_pending" ||
		session.status === "ci_failed"
	);
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
