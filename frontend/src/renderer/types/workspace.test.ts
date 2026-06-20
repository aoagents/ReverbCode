import { describe, expect, it } from "vitest";
import {
	findProjectOrchestrator,
	sessionIsActive,
	STATUS_META,
	statusOrder,
	toAgentProvider,
	toSessionStatus,
	openPRs,
	mergedPRCount,
	primaryPR,
	sortedPRs,
	type PRState,
	type PullRequestFacts,
	type SessionStatus,
	type WorkspaceSession,
	type WorkspaceSummary,
} from "./workspace";

function sessionWith(overrides: Partial<WorkspaceSession>): WorkspaceSession {
	return {
		id: "sess-1",
		workspaceId: "ws-1",
		workspaceName: "my-app",
		title: "fix-bug",
		provider: "claude-code",
		branch: "feat/x",
		status: "working",
		updatedAt: "2026-01-01T00:00:00Z",
		prs: [],
		...overrides,
	};
}

const pr = (overrides: Partial<PullRequestFacts> & { number: number; state: PRState }): PullRequestFacts => ({
	url: `https://example.com/pr/${overrides.number}`,
	ci: "passing",
	review: "approved",
	mergeability: "mergeable",
	reviewComments: false,
	updatedAt: "2026-01-01T00:00:00Z",
	...overrides,
});

describe("toSessionStatus", () => {
	it("passes through each of the five known states", () => {
		for (const status of statusOrder) {
			expect(toSessionStatus(status)).toBe(status);
		}
	});

	it("falls back to working for an unknown status", () => {
		expect(toSessionStatus("bogus")).toBe("working");
	});

	it("falls back to working when status is undefined", () => {
		expect(toSessionStatus(undefined)).toBe("working");
	});
});

describe("STATUS_META", () => {
	it("covers all five states and orders the board by urgency", () => {
		expect(Object.keys(STATUS_META).sort()).toEqual(["idle", "needs_input", "ready", "stalled", "working"]);
		expect(statusOrder).toEqual(["needs_input", "stalled", "ready", "working", "idle"]);
	});

	it("breathes only for working (the live state)", () => {
		expect(STATUS_META.working.breathe).toBe(true);
		for (const status of ["needs_input", "ready", "stalled", "idle"] as const) {
			expect(STATUS_META[status].breathe).toBe(false);
		}
	});

	it("flags attention for exactly needs_input and stalled", () => {
		const attention = (statusOrder as SessionStatus[]).filter((s) => STATUS_META[s].attention).sort();
		expect(attention).toEqual(["needs_input", "stalled"]);
	});
});

describe("sessionIsActive", () => {
	it("is false only for terminated sessions", () => {
		expect(sessionIsActive(sessionWith({ isTerminated: true }))).toBe(false);
		expect(sessionIsActive(sessionWith({ isTerminated: false }))).toBe(true);
		expect(sessionIsActive(sessionWith({ status: "idle" }))).toBe(true);
	});
});

describe("findProjectOrchestrator", () => {
	function workspaceWith(sessions: WorkspaceSession[]): WorkspaceSummary {
		return { id: "skills", name: "skills", path: "/tmp/skills", sessions };
	}

	it("skips a terminated orchestrator that precedes the live one", () => {
		// Regression: the daemon lists sessions by spawn number, so a dead
		// orchestrator (zellij session deleted) sorts before its live successor.
		// Picking it sent the Orchestrator button to an instant "[process exited]".
		const dead = sessionWith({ id: "skills-4", kind: "orchestrator", isTerminated: true });
		const live = sessionWith({ id: "skills-5", kind: "orchestrator", status: "needs_input" });
		const worker = sessionWith({ id: "skills-6", kind: "worker", status: "working" });
		expect(findProjectOrchestrator([workspaceWith([dead, live, worker])], "skills")).toBe(live);
	});

	it("returns undefined when every orchestrator is terminated", () => {
		const dead = sessionWith({ id: "skills-4", kind: "orchestrator", isTerminated: true });
		expect(findProjectOrchestrator([workspaceWith([dead])], "skills")).toBeUndefined();
	});

	it("ignores live workers when looking for an orchestrator", () => {
		const worker = sessionWith({ id: "skills-6", kind: "worker", status: "working" });
		expect(findProjectOrchestrator([workspaceWith([worker])], "skills")).toBeUndefined();
	});

	it("returns undefined for an unknown project", () => {
		const live = sessionWith({ id: "skills-5", kind: "orchestrator", status: "working" });
		expect(findProjectOrchestrator([workspaceWith([live])], "other")).toBeUndefined();
	});
});

describe("toAgentProvider", () => {
	it("passes through a known provider", () => {
		expect(toAgentProvider("opencode")).toBe("opencode");
	});

	it("defaults unknown and undefined providers to codex", () => {
		expect(toAgentProvider("totally-unknown")).toBe("codex");
		expect(toAgentProvider(undefined)).toBe("codex");
	});
});

describe("PR helpers", () => {
	const session = sessionWith({
		prs: [
			pr({ number: 41, state: "open" }),
			pr({ number: 42, state: "draft" }),
			pr({ number: 40, state: "merged" }),
			pr({ number: 39, state: "closed" }),
		],
	});

	it("sortedPRs orders open, draft, merged, closed then by number", () => {
		expect(sortedPRs(session).map((p) => p.number)).toEqual([41, 42, 40, 39]);
	});

	it("openPRs returns open and draft only", () => {
		expect(
			openPRs(session)
				.map((p) => p.number)
				.sort(),
		).toEqual([41, 42]);
	});

	it("mergedPRCount counts merged PRs", () => {
		expect(mergedPRCount(session)).toBe(1);
	});

	it("primaryPR is the highest-priority PR (open before merged)", () => {
		expect(primaryPR(session)?.number).toBe(41);
	});

	it("primaryPR is undefined when there are no PRs", () => {
		expect(primaryPR(sessionWith({ prs: [] }))).toBeUndefined();
	});
});
