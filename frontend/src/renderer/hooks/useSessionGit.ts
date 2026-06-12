// The Git review rail's data + actions: live workspace status from
// GET /sessions/{id}/git, and stage/discard/commit mutations that refetch it.
// The daemon owns all git mechanics; this hook only moves wire shapes.

import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useCallback, useState } from "react";
import { apiClient } from "../lib/api-client";
import { apiErrorMessage } from "../lib/api-errors";

export const sessionGitQueryKey = (sessionId: string) => ["session-git", sessionId] as const;

const REFETCH_MS = 5_000;

export function useSessionGit(sessionId: string | undefined) {
	const queryClient = useQueryClient();
	const [isMutating, setIsMutating] = useState(false);
	const [actionError, setActionError] = useState<string | null>(null);

	const statusQuery = useQuery({
		queryKey: sessionGitQueryKey(sessionId ?? "none"),
		enabled: Boolean(sessionId),
		refetchInterval: REFETCH_MS,
		retry: 1,
		queryFn: async () => {
			const { data, error } = await apiClient.GET("/api/v1/sessions/{sessionId}/git", {
				params: { path: { sessionId: sessionId ?? "" } },
			});
			if (error || !data) throw new Error(apiErrorMessage(error, "Could not load git status"));
			return data;
		},
	});

	const refetchStatus = useCallback(() => {
		if (sessionId) void queryClient.invalidateQueries({ queryKey: sessionGitQueryKey(sessionId) });
	}, [queryClient, sessionId]);

	const runAction = useCallback(
		async (action: () => Promise<void>) => {
			if (!sessionId || isMutating) return false;
			setActionError(null);
			setIsMutating(true);
			try {
				await action();
				return true;
			} catch (err) {
				setActionError(err instanceof Error ? err.message : "Git action failed");
				return false;
			} finally {
				setIsMutating(false);
				refetchStatus();
			}
		},
		[isMutating, refetchStatus, sessionId],
	);

	const stageAll = useCallback(
		() =>
			runAction(async () => {
				const { error } = await apiClient.POST("/api/v1/sessions/{sessionId}/git/stage", {
					params: { path: { sessionId: sessionId ?? "" } },
				});
				if (error) throw new Error(apiErrorMessage(error, "Could not stage changes"));
			}),
		[runAction, sessionId],
	);

	const discardAll = useCallback(
		() =>
			runAction(async () => {
				const { error } = await apiClient.POST("/api/v1/sessions/{sessionId}/git/discard", {
					params: { path: { sessionId: sessionId ?? "" } },
				});
				if (error) throw new Error(apiErrorMessage(error, "Could not discard changes"));
			}),
		[runAction, sessionId],
	);

	const commitAndPush = useCallback(
		(message: string, description: string) =>
			runAction(async () => {
				const fullMessage = description.trim() ? `${message.trim()}\n\n${description.trim()}` : message.trim();
				const { data, error } = await apiClient.POST("/api/v1/sessions/{sessionId}/git/commit", {
					params: { path: { sessionId: sessionId ?? "" } },
					body: { message: fullMessage, push: true },
				});
				if (error) throw new Error(apiErrorMessage(error, "Could not commit"));
				// The commit landed even when the push leg fails (the daemon returns
				// the SHA with a pushError warning); surface that as a non-fatal
				// notice instead of dropping it, since the work is already committed.
				if (data?.pushError) {
					setActionError(`Committed ${data.sha.slice(0, 7)} but push failed: ${data.pushError}`);
				}
			}),
		[runAction, sessionId],
	);

	return {
		status: statusQuery.data,
		statusError: statusQuery.isError ? apiErrorMessage(statusQuery.error, "Could not load git status") : null,
		isLoading: statusQuery.isLoading,
		isMutating,
		actionError,
		stageAll,
		discardAll,
		commitAndPush,
	};
}
