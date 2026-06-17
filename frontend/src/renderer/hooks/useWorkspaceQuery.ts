import { useQuery } from "@tanstack/react-query";
import { apiClient } from "../lib/api-client";
import { mockWorkspaces } from "../lib/mock-data";
import { toAgentProvider, toSessionStatus, type WorkspaceSession, type WorkspaceSummary } from "../types/workspace";

export const workspaceQueryKey = ["workspaces"] as const;
const usePreviewData = import.meta.env.VITE_NO_ELECTRON === "1";

// GET /sessions/{sessionId}/pr is the single source of truth for PR facts — no
// PR data rides on the session list — so we hydrate each session's lightweight
// {number, state} here, centrally, for every consumer (Summary, Board, PR page,
// Sidebar) that reads this query's cache. A per-session failure is treated as
// "no PR" rather than failing the whole workspace query.
async function fetchSessionPR(sessionId: string): Promise<WorkspaceSession["pullRequest"]> {
	const { data, error } = await apiClient.GET("/api/v1/sessions/{sessionId}/pr", {
		params: { path: { sessionId } },
	});
	if (error) return undefined;
	const pr = data?.prs?.[0];
	return pr ? { number: pr.number, state: pr.state } : undefined;
}

async function fetchWorkspaces(): Promise<WorkspaceSummary[]> {
	if (usePreviewData) {
		return mockWorkspaces;
	}

	const [{ data: projectsData, error: projectsError }, { data: sessionsData, error: sessionsError }] =
		await Promise.all([apiClient.GET("/api/v1/projects"), apiClient.GET("/api/v1/sessions")]);

	if (projectsError || sessionsError) throw projectsError ?? sessionsError;

	const sessions = sessionsData?.sessions ?? [];
	// Skip terminated sessions — their PRs are archived and the call is wasted.
	const prBySession = new Map(
		await Promise.all(
			sessions
				.filter((session) => !session.isTerminated)
				.map(async (session) => [session.id, await fetchSessionPR(session.id)] as const),
		),
	);

	return (projectsData?.projects ?? []).map((project) => ({
		id: project.id,
		name: project.name,
		path: project.path,
		sessions: sessions
			.filter((session) => session.projectId === project.id)
			.map((session) => ({
				id: session.id,
				terminalHandleId: session.terminalHandleId,
				workspaceId: project.id,
				workspaceName: project.name,
				title: session.displayName ?? session.issueId ?? session.id,
				provider: toAgentProvider(session.harness),
				kind: session.kind === "orchestrator" ? "orchestrator" : session.kind === "worker" ? "worker" : undefined,
				branch: `session/${session.id}`,
				status: toSessionStatus(session.status, session.isTerminated),
				createdAt: session.createdAt,
				updatedAt: session.updatedAt,
				pullRequest: prBySession.get(session.id),
			})),
	}));
}

// Shared so route loaders can prefetch via queryClient.ensureQueryData (paired
// with the router's defaultPreload: "intent") and the hook reads the same cache.
export const workspaceQueryOptions = {
	queryKey: workspaceQueryKey,
	queryFn: fetchWorkspaces,
	retry: 1,
	refetchInterval: 15_000,
};

export function useWorkspaceQuery() {
	return useQuery(workspaceQueryOptions);
}
