import { useNavigate } from "@tanstack/react-router";
import { CenterPane } from "./CenterPane";
import { SideRail } from "./SideRail";
import { Topbar } from "./Topbar";
import { useUiStore } from "../stores/ui-store";
import { useShell } from "../lib/shell-context";
import { useWorkspaceQuery } from "../hooks/useWorkspaceQuery";

type SessionViewProps = {
  sessionId: string;
  /** When entered via /projects/$projectId/sessions/... — used for the back-nav target. */
  projectId?: string;
};

// The session detail screen: the persistent terminal + git rail. Rendered by
// both the project-scoped and cross-project session routes. The terminal lives
// here (not in the shell) — switching sessions only changes route params, so
// TanStack Router keeps this component mounted and the terminal re-points its
// mux without remounting (useTerminalSession). Leaving for the board unmounts
// it; the server's output ring replays on return.
export function SessionView({ sessionId, projectId }: SessionViewProps) {
  const navigate = useNavigate();
  const workspaceQuery = useWorkspaceQuery();
  const workspaces = workspaceQuery.data ?? [];
  const { workbenchTab, setWorkbenchTab, toggleSidebar, theme } = useUiStore();
  const { daemonStatus, openSpawn } = useShell();

  const session = workspaces.flatMap((workspace) => workspace.sessions).find((s) => s.id === sessionId);
  const workspace =
    (session && workspaces.find((w) => w.id === session.workspaceId)) ??
    (projectId ? workspaces.find((w) => w.id === projectId) : undefined);

  const selectSession = (id: string, ws: string) =>
    void navigate({ to: "/projects/$projectId/sessions/$sessionId", params: { projectId: ws, sessionId: id } });

  if (!session && !workspaceQuery.isLoading) {
    return (
      <div className="grid h-full place-items-center bg-background p-6 text-center font-mono text-[12px] text-passive">
        Session not found. It may have been cleaned up — pick another from the sidebar.
      </div>
    );
  }

  const showSideRail = workbenchTab !== "terminal";

  return (
    <div className="flex h-full min-h-0 flex-col bg-background text-foreground">
      <Topbar
        onNewWorker={() => openSpawn(workspace?.id)}
        onSetWorkbenchTab={setWorkbenchTab}
        onToggleSidebar={toggleSidebar}
        session={session}
        view="session"
        workbenchTab={workbenchTab}
        workspace={workspace ?? undefined}
      />
      <div className="flex min-h-0 flex-1">
        <CenterPane daemonReady={daemonStatus.state === "ready"} session={session} theme={theme} view="session" />
        {showSideRail && (
          <SideRail onSelectSession={selectSession} session={session} view="session" workspaces={workspaces} />
        )}
      </div>
    </div>
  );
}
