import { apiClient } from "./api-client";

/** Spawn the project's orchestrator session via the daemon API. */
export async function spawnOrchestrator(projectId: string): Promise<string> {
	const { data, error, response } = await apiClient.POST("/api/v1/orchestrators", {
		body: { projectId },
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
