import { apiClient } from "./api-client";

/** Spawn the project's orchestrator session via the daemon API. When clean is
 *  true the daemon first tears down any active orchestrator for the project, then
 *  re-spawns one on the canonical branch (reattaching the existing branch). */
export async function spawnOrchestrator(projectId: string, clean = false): Promise<string> {
	const { data, error, response } = await apiClient.POST("/api/v1/orchestrators", {
		body: { projectId, clean },
	});

	if (error || !data?.orchestrator?.id) {
		const message =
			error && typeof error === "object" && "message" in error && typeof error.message === "string"
				? error.message
				: `Failed to spawn orchestrator (${response.status})`;
		throw new Error(message);
	}

	return data.orchestrator.id;
}
