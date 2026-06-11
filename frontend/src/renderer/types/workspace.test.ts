import { describe, expect, it } from "vitest";
import {
	sessionIsActive,
	sessionNeedsAttention,
	toAgentProvider,
	toSessionStatus,
	workerDisplayStatus,
	workerStatusPulses,
	type WorkspaceSession,
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
		...overrides,
	};
}

describe("toSessionStatus", () => {
	it("passes through a known status", () => {
		expect(toSessionStatus("mergeable")).toBe("mergeable");
		expect(toSessionStatus("no_signal")).toBe("no_signal");
	});

	it("overrides to terminated when the session is terminated", () => {
		expect(toSessionStatus("working", true)).toBe("terminated");
	});

	it("falls back to working for an unknown status", () => {
		expect(toSessionStatus("bogus")).toBe("working");
	});

	it("falls back to working when status is undefined", () => {
		expect(toSessionStatus(undefined)).toBe("working");
	});
});

describe("workerDisplayStatus", () => {
	it("prefers an explicit displayStatus override", () => {
		expect(workerDisplayStatus(sessionWith({ status: "ci_failed", displayStatus: "done" }))).toBe("done");
	});

	it.each([
		["needs_input", "needs_you"],
		["changes_requested", "needs_you"],
		["review_pending", "needs_you"],
		["no_signal", "needs_you"],
		["ci_failed", "ci_failed"],
		["approved", "mergeable"],
		["mergeable", "mergeable"],
		["merged", "done"],
		["terminated", "done"],
		["working", "working"],
		["idle", "working"],
	] as const)("maps %s to %s", (status, expected) => {
		expect(workerDisplayStatus(sessionWith({ status }))).toBe(expected);
	});
});

describe("sessionIsActive", () => {
	it("is false for merged and terminated", () => {
		expect(sessionIsActive(sessionWith({ status: "merged" }))).toBe(false);
		expect(sessionIsActive(sessionWith({ status: "terminated" }))).toBe(false);
	});

	it("is true for in-progress statuses", () => {
		expect(sessionIsActive(sessionWith({ status: "working" }))).toBe(true);
		expect(sessionIsActive(sessionWith({ status: "pr_open" }))).toBe(true);
	});
});

describe("sessionNeedsAttention", () => {
	it.each(["needs_input", "changes_requested", "review_pending", "ci_failed"] as const)("is true for %s", (status) => {
		expect(sessionNeedsAttention(sessionWith({ status }))).toBe(true);
	});

	it("is false for statuses that don't need the user", () => {
		expect(sessionNeedsAttention(sessionWith({ status: "working" }))).toBe(false);
		expect(sessionNeedsAttention(sessionWith({ status: "mergeable" }))).toBe(false);
	});
});

describe("workerStatusPulses", () => {
	it("pulses only for working and needs_you", () => {
		expect(workerStatusPulses("working")).toBe(true);
		expect(workerStatusPulses("needs_you")).toBe(true);
		expect(workerStatusPulses("mergeable")).toBe(false);
		expect(workerStatusPulses("done")).toBe(false);
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
