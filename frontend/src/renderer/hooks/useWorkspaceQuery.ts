import { useQuery } from "@tanstack/react-query";
import { apiClient } from "../lib/api-client";
import { mockWorkspaces } from "../lib/mock-data";
import { toAgentProvider, toSessionStatus, type WorkspaceSummary } from "../types/workspace";

export const workspaceQueryKey = ["workspaces"] as const;
const usePreviewData = import.meta.env.VITE_NO_ELECTRON === "1";

async function fetchWorkspaces(): Promise<WorkspaceSummary[]> {
	if (usePreviewData) {
		return mockWorkspaces;
	}

	const [{ data: projectsData, error: projectsError }, { data: sessionsData, error: sessionsError }] =
		await Promise.all([apiClient.GET("/api/v1/projects"), apiClient.GET("/api/v1/sessions")]);

	if (projectsError || sessionsError) throw projectsError ?? sessionsError;

	return (projectsData?.projects ?? []).map((project) => ({
		id: project.id,
		name: project.name,
		path: project.path,
		sessions: (sessionsData?.sessions ?? [])
			.filter((session) => session.projectId === project.id)
			.map((session) => ({
				id: session.id,
				terminalHandleId: session.terminalHandleId,
				workspaceId: project.id,
				workspaceName: project.name,
				title: session.displayName ?? session.issueId ?? session.id,
				provider: toAgentProvider(session.harness),
				branch: "",
				status: toSessionStatus(session.status, session.isTerminated),
				updatedAt: new Date(session.updatedAt).toLocaleTimeString([], { hour: "numeric", minute: "2-digit" }),
			})),
	}));
}

export function useWorkspaceQuery() {
	return useQuery({
		queryKey: workspaceQueryKey,
		queryFn: fetchWorkspaces,
		retry: 1,
		refetchInterval: 15_000,
	});
}
