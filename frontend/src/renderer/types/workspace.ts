/**
 * The five display states the dashboard renders, derived server-side and sent
 * verbatim. Each maps to one distinct move a human makes when scanning a wall
 * of agents (see {@link STATUS_META}). Finer PR detail lives in the inspector,
 * not in the glanceable status.
 */
export type SessionStatus = "working" | "needs_input" | "ready" | "stalled" | "idle";

const sessionStatuses = new Set<SessionStatus>(["working", "needs_input", "ready", "stalled", "idle"]);

export function toSessionStatus(status?: string): SessionStatus {
	return status && sessionStatuses.has(status as SessionStatus) ? (status as SessionStatus) : "working";
}

/**
 * The one shared status definition every surface reads from: the pill, the
 * board columns, the sidebar dot, and the card badge. `tone` is a theme var so
 * each state tracks the light/dark palette; `breathe` is whether it pulses
 * (alive); `attention` is whether it needs a human to unblock progress.
 */
export const STATUS_META: Record<SessionStatus, { label: string; tone: string; breathe: boolean; attention: boolean }> =
	{
		working: { label: "Working", tone: "var(--orange)", breathe: true, attention: false },
		needs_input: { label: "Needs input", tone: "var(--amber)", breathe: false, attention: true },
		ready: { label: "Ready", tone: "var(--green)", breathe: false, attention: false },
		stalled: { label: "Stalled", tone: "var(--red)", breathe: false, attention: true },
		idle: { label: "Idle", tone: "var(--fg-muted)", breathe: false, attention: false },
	};

/** Board columns left→right by human-action urgency. */
export const statusOrder: SessionStatus[] = ["needs_input", "stalled", "ready", "working", "idle"];

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

export type SessionKind = "worker" | "orchestrator";

/** Lifecycle state of a single pull request, mirrors the daemon's enum. */
export type PRState = "open" | "draft" | "merged" | "closed";

/**
 * One attributed pull request, mirroring the daemon's SessionPRFacts wire shape.
 * A session can own many (e.g. a stack), so {@link WorkspaceSession.prs} is a
 * list. The wire carries no source/target branch or parent pointer, so the UI
 * renders a flat list of PRs, not a stack tree.
 */
export type PullRequestFacts = {
	url: string;
	number: number;
	state: PRState;
	ci: string;
	review: string;
	mergeability: string;
	reviewComments: boolean;
	updatedAt: string;
};

export type WorkspaceSession = {
	id: string;
	terminalHandleId?: string;
	workspaceId: string;
	workspaceName: string;
	title: string;
	provider: AgentProvider;
	kind?: SessionKind;
	branch: string;
	status: SessionStatus;
	/** Whether the session is torn down. Drives liveness, not the display status
	 * (a terminated session reads {@link SessionStatus} `idle`). */
	isTerminated?: boolean;
	/** ISO timestamp from the daemon; used for relative time in the inspector. */
	createdAt?: string;
	/** ISO timestamp from the daemon. */
	updatedAt: string;
	/** The session's git diff against its base, when known. */
	changedFiles?: ChangedFile[];
	/** Pre-filled commit subject for the Git rail, when known. */
	commitMessage?: string;
	/**
	 * The session's attributed pull requests. One session can own many (a stack
	 * or independent PRs); empty when none are open yet. Status aggregation is
	 * done server-side, so {@link status} already reflects all of these.
	 */
	prs: PullRequestFacts[];
};

// Open PRs (actionable) sort above merged/closed; ties break by number.
const prStateRank: Record<PRState, number> = { open: 0, draft: 1, merged: 2, closed: 3 };

/** A session's PRs ordered actionable-first (open, draft, merged, closed). */
export function sortedPRs(session: WorkspaceSession): PullRequestFacts[] {
	return [...session.prs].sort((a, b) => prStateRank[a.state] - prStateRank[b.state] || a.number - b.number);
}

/** PRs still in flight (open or draft). */
export function openPRs(session: WorkspaceSession): PullRequestFacts[] {
	return session.prs.filter((pr) => pr.state === "open" || pr.state === "draft");
}

export function mergedPRCount(session: WorkspaceSession): number {
	return session.prs.filter((pr) => pr.state === "merged").length;
}

/** The highest-priority PR for compact one-line surfaces (board card, sidebar). */
export function primaryPR(session: WorkspaceSession): PullRequestFacts | undefined {
	return sortedPRs(session)[0];
}

export function isOrchestratorSession(session: WorkspaceSession): boolean {
	return session.kind === "orchestrator" || session.id.endsWith("-orchestrator");
}

/**
 * The project's LIVE orchestrator, if any. Terminated orchestrator rows stay in
 * the session list (the daemon returns all sessions, ordered by spawn number),
 * so an earlier dead orchestrator must not shadow a live one: its zellij
 * session is deleted and attaching to it dead-ends in an instant
 * "[process exited]". No live orchestrator means undefined, so the topbar offers
 * Spawn instead of navigating to a dead session.
 */
export function findProjectOrchestrator(
	workspaces: WorkspaceSummary[],
	projectId: string,
): WorkspaceSession | undefined {
	const workspace = workspaces.find((w) => w.id === projectId);
	return workspace?.sessions.find((session) => isOrchestratorSession(session) && sessionIsActive(session));
}

export function workerSessions(sessions: WorkspaceSession[]): WorkspaceSession[] {
	return sessions.filter((s) => !isOrchestratorSession(s));
}

/** Whether the session is still live (not torn down). */
export function sessionIsActive(session: WorkspaceSession): boolean {
	return !session.isTerminated;
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
