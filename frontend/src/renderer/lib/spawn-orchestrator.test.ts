import { describe, expect, it, vi, beforeEach } from "vitest";
import { spawnOrchestrator } from "./spawn-orchestrator";
import { apiClient } from "./api-client";

vi.mock("./api-client", () => ({
	apiClient: { POST: vi.fn() },
}));

describe("spawnOrchestrator", () => {
	beforeEach(() => vi.clearAllMocks());

	it("sends clean:true through to the request body when asked", async () => {
		(apiClient.POST as ReturnType<typeof vi.fn>).mockResolvedValue({
			data: { orchestrator: { id: "proj-9" } },
			error: undefined,
			response: { status: 201 },
		});
		const id = await spawnOrchestrator("proj", true);
		expect(id).toBe("proj-9");
		expect(apiClient.POST).toHaveBeenCalledWith("/api/v1/orchestrators", {
			body: { projectId: "proj", clean: true },
		});
	});

	it("defaults clean to false / omitted for the existing call sites", async () => {
		(apiClient.POST as ReturnType<typeof vi.fn>).mockResolvedValue({
			data: { orchestrator: { id: "proj-1" } },
			error: undefined,
			response: { status: 201 },
		});
		await spawnOrchestrator("proj");
		expect(apiClient.POST).toHaveBeenCalledWith("/api/v1/orchestrators", {
			body: { projectId: "proj", clean: false },
		});
	});
});
