import { afterEach, describe, expect, it, vi } from "vitest";
import { setApiBaseUrl } from "./api-client";
import { spawnOrchestrator } from "./spawn-orchestrator";

describe("spawnOrchestrator", () => {
	afterEach(() => {
		vi.restoreAllMocks();
		setApiBaseUrl("http://127.0.0.1:3001");
	});

	it("throws the daemon error envelope message when spawn fails", async () => {
		vi.spyOn(globalThis, "fetch").mockResolvedValue(
			new Response(
				JSON.stringify({
					error: "internal_error",
					code: "orchestrator_spawn_failed",
					message: "worktree has uncommitted changes",
					requestId: "req-123",
				}),
				{
					status: 500,
					headers: { "Content-Type": "application/json" },
				},
			),
		);

		await expect(spawnOrchestrator("project-1")).rejects.toThrow("worktree has uncommitted changes");
	});

	it("falls back to the response status when no error envelope is available", async () => {
		vi.spyOn(globalThis, "fetch").mockResolvedValue(
			new Response(JSON.stringify({}), {
				status: 200,
				headers: { "Content-Type": "application/json" },
			}),
		);

		await expect(spawnOrchestrator("project-1")).rejects.toThrow("Failed to spawn orchestrator (200)");
	});
});
