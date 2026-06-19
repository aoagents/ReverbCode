import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { apiClient, apiErrorMessage } from "../lib/api-client";
import { workspaceQueryKey } from "./useWorkspaceQuery";

export const importStatusQueryKey = ["import-status"] as const;
const usePreviewData = import.meta.env.VITE_NO_ELECTRON === "1";

export type ImportStatus = { available: boolean; legacyRoot: string };

export type ImportReport = {
	projectsImported: number;
	projectsSkipped: number;
	notes?: string[];
};

async function fetchImportStatus(): Promise<ImportStatus> {
	const { data, error } = await apiClient.GET("/api/v1/import");
	if (error) throw new Error(apiErrorMessage(error));
	return { available: data?.available ?? false, legacyRoot: data?.legacyRoot ?? "" };
}

// useImportStatus polls the daemon for the first-run import offer. The offer
// only appears on a fresh, un-imported database and retires the moment data
// lands, so a slow poll is plenty. A daemon that doesn't implement the endpoint
// (501) or is unreachable resolves to "no offer" rather than surfacing an
// error. This is an opt-in convenience, never a blocker.
export function useImportStatus() {
	return useQuery({
		queryKey: importStatusQueryKey,
		queryFn: fetchImportStatus,
		enabled: !usePreviewData,
		refetchInterval: 30_000,
		retry: 1,
		throwOnError: false,
	});
}

// useRunImport triggers the legacy import through the live daemon and, on
// success, invalidates both the import status (so the offer retires) and the
// workspace query (so the imported projects appear).
export function useRunImport() {
	const queryClient = useQueryClient();
	return useMutation<ImportReport, Error>({
		mutationFn: async () => {
			const { data, error } = await apiClient.POST("/api/v1/import");
			if (error) throw new Error(apiErrorMessage(error));
			return (data?.report ?? {}) as ImportReport;
		},
		onSuccess: () => {
			void queryClient.invalidateQueries({ queryKey: importStatusQueryKey });
			void queryClient.invalidateQueries({ queryKey: workspaceQueryKey });
		},
	});
}
