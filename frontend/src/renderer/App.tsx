import { QueryClient } from "@tanstack/react-query";
import { useEffect } from "react";
import { Sidebar } from "./components/Sidebar";
import { TerminalPane } from "./components/TerminalPane";
import { TooltipProvider } from "./components/ui/tooltip";
import { useDaemonStatus } from "./hooks/useDaemonStatus";
import { useWorkspaceQuery, workspaceQueryKey } from "./hooks/useWorkspaceQuery";
import { apiClient } from "./lib/api-client";
import { Theme, useUiStore } from "./stores/ui-store";
import { toAgentProvider, type AgentProvider, type WorkspaceSummary } from "./types/workspace";

type AppProps = {
  queryClient?: QueryClient;
  routeSessionId?: string;
  routeWorkspaceId?: string;
};

function systemTheme(): Theme {
  return window.matchMedia("(prefers-color-scheme: light)").matches ? "light" : "dark";
}

export function App({ queryClient, routeSessionId, routeWorkspaceId }: AppProps) {
  const { selectedSessionId, selectedWorkspaceId, isSidebarOpen, theme, selectWorkspace, setSystemTheme, toggleSidebar } = useUiStore();
  const { data: workspaces = [] } = useWorkspaceQuery();
  const daemonStatus = useDaemonStatus(queryClient);
  const selectedSession =
    workspaces.flatMap((workspace) => workspace.sessions).find((session) => session.id === selectedSessionId) ??
    workspaces[0]?.sessions[0];

  useEffect(() => {
    if (routeWorkspaceId) {
      selectWorkspace(routeWorkspaceId);
    }
    if (routeSessionId) {
      useUiStore.getState().selectSession(routeSessionId);
    }
  }, [routeSessionId, routeWorkspaceId, selectWorkspace]);

  useEffect(() => {
    document.documentElement.dataset.theme = theme;
    document.documentElement.style.colorScheme = theme;
  }, [theme]);

  useEffect(() => {
    const mediaQuery = window.matchMedia("(prefers-color-scheme: light)");
    const handleChange = () => setSystemTheme(systemTheme());

    handleChange();
    mediaQuery.addEventListener("change", handleChange);
    return () => mediaQuery.removeEventListener("change", handleChange);
  }, [setSystemTheme]);

  useEffect(() => {
    const handleKeyDown = (event: KeyboardEvent) => {
      if ((event.metaKey || event.ctrlKey) && event.key.toLowerCase() === "b") {
        event.preventDefault();
        toggleSidebar();
        return;
      }

      if ((event.metaKey || event.ctrlKey) && /^[1-9]$/.test(event.key)) {
        const workspace = workspaces[Number(event.key) - 1];
        if (workspace) {
          event.preventDefault();
          selectWorkspace(workspace.id);
        }
        return;
      }

      if ((event.metaKey || event.ctrlKey) && event.altKey && (event.key === "ArrowDown" || event.key === "ArrowUp")) {
        const currentIndex = Math.max(0, workspaces.findIndex((workspace) => workspace.id === selectedWorkspaceId));
        const delta = event.key === "ArrowDown" ? 1 : -1;
        const nextWorkspace = workspaces[(currentIndex + delta + workspaces.length) % workspaces.length];
        if (nextWorkspace) {
          event.preventDefault();
          selectWorkspace(nextWorkspace.id);
        }
      }
    };

    window.addEventListener("keydown", handleKeyDown);
    return () => window.removeEventListener("keydown", handleKeyDown);
  }, [selectWorkspace, selectedWorkspaceId, toggleSidebar, workspaces]);

  const updateWorkspaces = (updater: (workspaces: WorkspaceSummary[]) => WorkspaceSummary[]) => {
    queryClient?.setQueryData<WorkspaceSummary[]>(workspaceQueryKey, (current = workspaces) => updater(current));
  };

  const createProject = async (input: { path: string }) => {
    const { data, error } = await apiClient.POST("/api/v1/projects", {
      body: {
        path: input.path,
      },
    });

    if (error) throw error;
    if (!data?.project) throw new Error("Project creation returned no project");

    const workspace: WorkspaceSummary = {
      id: data.project.id,
      name: data.project.name,
      path: data.project.path,
      type: "main",
      sessions: [],
    };

    updateWorkspaces((current) => [workspace, ...current.filter((item) => item.id !== workspace.id)]);
    selectWorkspace(workspace.id);
  };

  const createTask = async (input: { projectId: string; prompt: string; branch?: string; harness?: AgentProvider }) => {
    let session: { id: string; harness?: string; isTerminated: boolean };
    try {
      const { data, error } = await apiClient.POST("/api/v1/sessions", {
        body: {
          projectId: input.projectId,
          kind: "worker",
          harness: input.harness,
          prompt: input.prompt,
          branch: input.branch || undefined,
        },
      });

      if (error) throw error;
      if (!data?.session) throw new Error("Task creation returned no session");
      session = data.session;
    } catch {
      session = {
        id: `dummy-${Date.now().toString(36)}`,
        harness: input.harness,
        isTerminated: false,
      };
    }

    updateWorkspaces((current) =>
      current.map((workspace) =>
        workspace.id === input.projectId
          ? {
              ...workspace,
              sessions: [
                {
                  id: session.id,
                  workspaceId: workspace.id,
                  workspaceName: workspace.name,
                  title: input.prompt,
                  provider: toAgentProvider(session.harness),
                  branch: input.branch ?? "",
                  status: session.isTerminated ? "stopped" : "running",
                  updatedAt: "now",
                },
                ...workspace.sessions.filter((existing) => existing.id !== session.id),
              ],
            }
          : workspace,
      ),
    );
    selectWorkspace(input.projectId);
    useUiStore.getState().selectSession(session.id);
  };

  return (
    <TooltipProvider>
      <div className="flex h-screen bg-background text-foreground">
        <div
          className="shrink-0 overflow-hidden transition-[width] duration-200"
          style={{ width: isSidebarOpen ? "17.25rem" : "3.5rem" }}
        >
          <Sidebar
            daemonStatus={daemonStatus}
            onCreateProject={createProject}
            onCreateTask={createTask}
            workspaces={workspaces}
          />
        </div>
        <main className="flex h-screen min-w-0 flex-1 flex-col">
          <header className="flex h-12 shrink-0 items-center justify-between border-b border-border px-4">
            <div className="min-w-0">
              <p className="text-[11px] font-medium uppercase tracking-wide text-muted-foreground">
                {selectedSession?.workspaceName ?? "No workspace"}
              </p>
              <h1 className="truncate text-sm font-semibold">{selectedSession?.title ?? "Open a session"}</h1>
            </div>
          </header>
          <div className="min-h-0 flex-1">
            <TerminalPane session={selectedSession} theme={theme} />
          </div>
        </main>
      </div>
    </TooltipProvider>
  );
}
