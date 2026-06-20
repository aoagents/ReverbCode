import { apiClient, apiErrorMessage } from "./api-client";

/** Spawn the project's orchestrator session via the daemon API. */
export async function spawnOrchestrator(projectId: string): Promise<string> {
	const { data, error, response } = await apiClient.POST("/api/v1/orchestrators", {
		body: { projectId },
	});

	if (error || !data?.orchestrator?.id) {
		const fallback = `Failed to spawn orchestrator (${response.status})`;
		throw new Error(apiErrorMessage(error, fallback));
	}

	return data.orchestrator.id;
}
