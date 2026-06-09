import { useQueryClient } from "@tanstack/react-query";
import { useEffect } from "react";
import { Group, Panel, Separator } from "react-resizable-panels";
import { Sidebar } from "./components/Sidebar";
import { TerminalPane } from "./components/TerminalPane";
import { Badge } from "./components/ui/badge";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "./components/ui/tabs";
import { TooltipProvider } from "./components/ui/tooltip";
import { useDaemonStatus } from "./hooks/useDaemonStatus";
import { useWorkspaceQuery, workspaceQueryKey } from "./hooks/useWorkspaceQuery";
import { apiClient } from "./lib/api-client";
import { Theme, useUiStore } from "./stores/ui-store";
import { toAgentProvider, type AgentProvider, type WorkspaceSession, type WorkspaceSummary } from "./types/workspace";

type AppProps = {
  routeSessionId?: string;
  routeWorkspaceId?: string;
};

function systemTheme(): Theme {
  return window.matchMedia("(prefers-color-scheme: light)").matches ? "light" : "dark";
}

export function App({ routeSessionId, routeWorkspaceId }: AppProps) {
  const queryClient = useQueryClient();
  const { selectedSessionId, selectedWorkspaceId, isSidebarOpen, theme, layout, selectWorkspace, setLayout, setSystemTheme, toggleSidebar } = useUiStore();
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
    queryClient.setQueryData<WorkspaceSummary[]>(workspaceQueryKey, (current = workspaces) => updater(current));
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
    const { data, error } = await apiClient.POST("/api/v1/sessions", {
      body: {
        projectId: input.projectId,
        kind: "worker",
        harness: input.harness,
        prompt: input.prompt,
        branch: input.branch || undefined,
      },
    });

    if (error || !data?.session) {
      console.error("Task creation failed:", error ?? "no session returned");
      return;
    }

    const session = data.session;

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

  const sidebar = (
    <Sidebar
      daemonStatus={daemonStatus}
      onCreateProject={createProject}
      onCreateTask={createTask}
      workspaces={workspaces}
    />
  );

  return (
    <TooltipProvider>
      <div className="flex h-screen bg-background text-foreground">
        {isSidebarOpen ? (
          <Group orientation="horizontal" className="h-screen w-full" defaultLayout={layout} onLayoutChanged={setLayout}>
            <Panel id="sidebar" minSize="14rem" maxSize="26rem" defaultSize="17.25rem" className="min-w-0">
              {sidebar}
            </Panel>
            <Separator className="w-px cursor-col-resize bg-border transition-colors hover:bg-ring data-[resizing]:bg-ring" />
            <Panel id="main" minSize="24rem" className="min-w-0">
              <WorkbenchMain session={selectedSession} theme={theme} />
            </Panel>
          </Group>
        ) : (
          <>
            <div className="w-14 shrink-0 overflow-hidden">{sidebar}</div>
            <WorkbenchMain session={selectedSession} theme={theme} />
          </>
        )}
      </div>
    </TooltipProvider>
  );
}

function statusLabel(status: WorkspaceSession["status"]): string {
  switch (status) {
    case "running":
      return "Running";
    case "needs_input":
      return "Needs input";
    case "failed":
      return "Failed";
    default:
      return "Stopped";
  }
}

function SessionStatusBadge({ status }: { status: WorkspaceSession["status"] }) {
  const variant = status === "running" ? "success" : status === "needs_input" ? "warning" : status === "failed" ? "outline" : "muted";
  return <Badge variant={variant}>{statusLabel(status)}</Badge>;
}

function SessionDetails({ session }: { session?: WorkspaceSession }) {
  if (!session) {
    return <p className="text-sm text-muted-foreground">No session selected.</p>;
  }
  return (
    <dl className="grid grid-cols-[7rem_1fr] gap-y-2 text-sm">
      <dt className="text-muted-foreground">Provider</dt>
      <dd>{session.provider}</dd>
      <dt className="text-muted-foreground">Branch</dt>
      <dd className="font-mono text-xs">{session.branch || "—"}</dd>
      <dt className="text-muted-foreground">Status</dt>
      <dd>
        <SessionStatusBadge status={session.status} />
      </dd>
      <dt className="text-muted-foreground">Workspace</dt>
      <dd className="truncate">{session.workspaceName}</dd>
      <dt className="text-muted-foreground">Updated</dt>
      <dd>{session.updatedAt}</dd>
    </dl>
  );
}

function WorkbenchMain({ session, theme }: { session?: WorkspaceSession; theme: Theme }) {
  return (
    <Tabs defaultValue="terminal" className="flex h-full min-h-0 min-w-0 flex-1 flex-col">
      <header className="flex h-12 shrink-0 items-center justify-between gap-3 border-b border-border px-4">
        <div className="min-w-0">
          <p className="text-[11px] font-medium uppercase tracking-wide text-muted-foreground">
            {session?.workspaceName ?? "No workspace"}
          </p>
          <h1 className="truncate text-sm font-semibold">{session?.title ?? "Open a session"}</h1>
        </div>
        <div className="flex shrink-0 items-center gap-3">
          {session ? <SessionStatusBadge status={session.status} /> : null}
          <TabsList>
            <TabsTrigger value="terminal">Terminal</TabsTrigger>
            <TabsTrigger value="details">Details</TabsTrigger>
          </TabsList>
        </div>
      </header>
      {/* forceMount keeps the terminal (and its live PTY WebSocket) alive when the
          Details tab is active; Radix sets hidden on the inactive content. */}
      <TabsContent value="terminal" forceMount className="min-h-0 flex-1 data-[state=inactive]:hidden">
        <TerminalPane session={session} theme={theme} />
      </TabsContent>
      <TabsContent value="details" className="min-h-0 flex-1 overflow-auto p-4">
        <SessionDetails session={session} />
      </TabsContent>
    </Tabs>
  );
}
