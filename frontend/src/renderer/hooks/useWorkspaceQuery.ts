import { useQuery } from "@tanstack/react-query";
import { apiClient } from "../lib/api-client";
import { mockWorkspaces } from "../lib/mock-data";
import { toAgentProvider, type WorkspaceSession, type WorkspaceSummary } from "../types/workspace";

export const workspaceQueryKey = ["workspaces"] as const;

function sessionStatus(session: { isTerminated: boolean; status: string }): WorkspaceSession["status"] {
  if (session.isTerminated) return "stopped";
  if (session.status === "waiting_input") return "needs_input";
  if (session.status === "failed") return "failed";
  return "running";
}

async function fetchWorkspaces(): Promise<WorkspaceSummary[]> {
  const [{ data: projectsData, error: projectsError }, { data: sessionsData, error: sessionsError }] = await Promise.all([
    apiClient.GET("/api/v1/projects"),
    apiClient.GET("/api/v1/sessions"),
  ]);

  if (projectsError || sessionsError) throw projectsError ?? sessionsError;

  return (projectsData?.projects ?? []).map((project) => ({
    id: project.id,
    name: project.name,
    path: project.resolveError ?? project.id,
    sessions: (sessionsData?.sessions ?? [])
      .filter((session) => session.projectId === project.id)
      .map((session) => ({
        id: session.id,
        workspaceId: project.id,
        workspaceName: project.name,
        title: session.displayName ?? session.issueId ?? session.id,
        provider: toAgentProvider(session.harness),
        branch: "",
        status: sessionStatus(session),
        updatedAt: new Date(session.updatedAt).toLocaleTimeString([], { hour: "numeric", minute: "2-digit" }),
      })),
  }));
}

export function useWorkspaceQuery() {
  return useQuery({
    queryKey: workspaceQueryKey,
    queryFn: async () => {
      try {
        const workspaces = await fetchWorkspaces();
        return workspaces.length > 0 ? workspaces : mockWorkspaces;
      } catch {
        return mockWorkspaces;
      }
    },
  });
}
