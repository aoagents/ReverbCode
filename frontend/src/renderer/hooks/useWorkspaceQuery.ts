import { useQuery } from "@tanstack/react-query";
import type { components } from "../../api/schema";
import { apiClient, apiErrorMessage, hasTrustedApiBaseUrl } from "../lib/api-client";
import { mockWorkspaces } from "../lib/mock-data";
import {
	type PRState,
	type PullRequestFacts,
	toAgentProvider,
	toSessionStatus,
	type WorkspaceSummary,
} from "../types/workspace";

function toPRState(state: string): PRState {
	if (state === "draft" || state === "merged" || state === "closed") {
		return state;
	}

	return "open";
}

function toPullRequestFacts(pr: components["schemas"]["SessionPRFacts"]): PullRequestFacts {
	return {
		url: pr.url,
		number: pr.number,
		state: toPRState(pr.state),
		ci: pr.ci,
		review: pr.review,
		mergeability: pr.mergeability,
		reviewComments: pr.reviewComments,
		updatedAt: pr.updatedAt,
	};
}

export const workspaceQueryKey = ["workspaces"] as const;

const usePreviewData = import.meta.env.VITE_NO_ELECTRON === "1";

async function fetchWorkspaces(): Promise<WorkspaceSummary[]> {
	if (usePreviewData) {
		return mockWorkspaces;
	}

	if (!hasTrustedApiBaseUrl()) {
		return [];
	}

	const [{ data: projectsData, error: projectsError }, { data: sessionsData, error: sessionsError }] =
		await Promise.all([apiClient.GET("/api/v1/projects"), apiClient.GET("/api/v1/sessions")]);

	if (projectsError) {
		throw projectsError instanceof Error
			? projectsError
			: new Error(apiErrorMessage(projectsError, "Could not load projects"));
	}

	if (sessionsError) {
		throw sessionsError instanceof Error
			? sessionsError
			: new Error(apiErrorMessage(sessionsError, "Could not load sessions"));
	}

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
				kind: session.kind === "orchestrator" ? "orchestrator" : session.kind === "worker" ? "worker" : undefined,
				branch: session.branch ?? `session/${session.id}`,
				status: toSessionStatus(session.status, session.isTerminated),
				createdAt: session.createdAt,
				updatedAt: session.updatedAt,
				previewUrl: session.previewUrl,
				previewRevision: session.previewRevision,
				prs: (session.prs ?? []).map(toPullRequestFacts),
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
