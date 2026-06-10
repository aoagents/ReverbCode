import { useNavigate, useParams, useRouterState } from "@tanstack/react-router";
import { ChevronRight, GitPullRequest, PanelLeft, Plus, Search, Settings, Waypoints } from "lucide-react";
import { useState } from "react";
import { attentionZone, type WorkspaceSession, type WorkspaceSummary } from "../types/workspace";
import { useUiStore } from "../stores/ui-store";
import { aoBridge } from "../lib/bridge";
import { useEventsConnection } from "../hooks/useEventsConnection";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuShortcut,
  DropdownMenuTrigger,
} from "./ui/dropdown-menu";
import { Tooltip, TooltipContent, TooltipTrigger } from "./ui/tooltip";
import { cn } from "../lib/utils";

type SidebarProps = {
  daemonStatus: { state: string; message?: string };
  workspaceError?: string;
  workspaces: WorkspaceSummary[];
  onCreateProject: (input: { path: string }) => Promise<void>;
  onNewWorker: (projectId: string) => void;
};

// Selection state comes from the URL: which project/session is active is the
// route params, and clicks navigate rather than mutate a store.
function useSelection() {
  const navigate = useNavigate();
  const params = useParams({ strict: false }) as { projectId?: string; sessionId?: string };
  const pathname = useRouterState({ select: (state) => state.location.pathname });
  return {
    isHome: pathname === "/",
    activeProjectId: params.projectId,
    activeSessionId: params.sessionId,
    goHome: () => void navigate({ to: "/" }),
    goPrs: () => void navigate({ to: "/prs" }),
    goReview: () => void navigate({ to: "/review" }),
    goSettings: (projectId: string) =>
      void navigate({ to: "/projects/$projectId/settings", params: { projectId } }),
    goProject: (projectId: string) => void navigate({ to: "/projects/$projectId", params: { projectId } }),
    goSession: (projectId: string, sessionId: string) =>
      void navigate({ to: "/projects/$projectId/sessions/$sessionId", params: { projectId, sessionId } }),
  };
}

// agent-orchestrator's SessionDot: 6px dot, neutral grey at rest, orange +
// breathe while the agent is working. Other attention zones stay neutral here
// (the board carries the richer colour coding).
function SessionDot({ session }: { session: WorkspaceSession }) {
  const working = attentionZone(session) === "working";
  return (
    <span
      aria-hidden="true"
      className={cn("mt-px h-1.5 w-1.5 shrink-0 rounded-full", working ? "animate-status-pulse bg-working" : "bg-[#444951]")}
    />
  );
}

export function Sidebar({ daemonStatus, workspaceError, workspaces, onCreateProject, onNewWorker }: SidebarProps) {
  const { isSidebarOpen, toggleSidebar } = useUiStore();
  const selection = useSelection();
  const eventsConnection = useEventsConnection();
  // Disclosure state: projects are expanded by default; a project id present in
  // this set is collapsed (sessions hidden).
  const [collapsedIds, setCollapsedIds] = useState<ReadonlySet<string>>(() => new Set());
  const toggleCollapsed = (id: string) =>
    setCollapsedIds((prev) => {
      const next = new Set(prev);
      next.has(id) ? next.delete(id) : next.add(id);
      return next;
    });

  if (!isSidebarOpen) {
    return <CollapsedRail workspaces={workspaces} onCreateProject={onCreateProject} />;
  }

  return (
    <aside className="flex h-full w-[244px] shrink-0 flex-col border-r border-border bg-[#08090b] px-[7px] pt-3.5 pb-0 text-sidebar-foreground">
      {/* Brand (project-sidebar__brand) */}
      <div className="flex shrink-0 items-center gap-2.5 px-2 pb-[18px]">
        <button
          aria-label="Orchestrator board"
          className="grid h-[22px] w-[22px] shrink-0 place-items-center rounded-[6px] bg-accent text-accent-foreground"
          onClick={selection.goHome}
          type="button"
        >
          <Waypoints className="h-3.5 w-3.5" aria-hidden="true" />
        </button>
        <span className="min-w-0 flex-1 truncate text-[14px] font-bold tracking-[-0.015em] text-foreground">
          Reverb<b className="font-normal text-passive"> / </b>Code
        </span>
        <Tooltip>
          <TooltipTrigger asChild>
            <button
              aria-label="Collapse sidebar"
              className="grid h-7 w-7 shrink-0 place-items-center rounded-md text-passive transition-colors hover:bg-white/[0.04] hover:text-foreground"
              onClick={toggleSidebar}
              type="button"
            >
              <PanelLeft className="h-[15px] w-[15px]" aria-hidden="true" />
            </button>
          </TooltipTrigger>
          <TooltipContent>Collapse sidebar · ⌘B</TooltipContent>
        </Tooltip>
      </div>

      {/* Section label (project-sidebar__nav-label) */}
      <div className="flex shrink-0 items-center justify-between px-2 pb-2">
        <span className="text-[10.5px] font-semibold uppercase tracking-[0.09em] text-[#444951]">Projects</span>
        <CreateProjectButton onCreateProject={onCreateProject} />
      </div>

      {/* Tree (project-sidebar__tree) */}
      <div className="min-h-0 flex-1 overflow-y-auto">
        {workspaceError ? (
          <div className="px-2 py-3">
            <p className="text-[12px] text-foreground">Could not load projects.</p>
            <p className="mt-1 text-[11px] text-passive">{workspaceError}</p>
          </div>
        ) : workspaces.length === 0 ? (
          <div className="px-2 py-3">
            <p className="text-[12px] text-passive">No projects yet.</p>
            <p className="mt-1 text-[11px] text-passive">
              Click <span className="text-foreground">+</span> above to register a git repo.
            </p>
          </div>
        ) : (
          workspaces.map((workspace) => (
            <ProjectRow
              key={workspace.id}
              workspace={workspace}
              expanded={!collapsedIds.has(workspace.id)}
              selection={selection}
              onToggle={() => toggleCollapsed(workspace.id)}
              onNewWorker={() => onNewWorker(workspace.id)}
            />
          ))
        )}
      </div>

      {/* Footer (project-sidebar__footer) — single Settings menu */}
      <div className="mt-auto flex shrink-0 items-center border-t border-border pt-3">
        <DropdownMenu>
          <DropdownMenuTrigger asChild>
            <button
              aria-label="Settings"
              className="flex items-center gap-2.5 rounded-md p-2 text-[13px] font-medium text-passive transition-colors hover:bg-white/[0.04] hover:text-foreground data-[state=open]:bg-white/[0.04] data-[state=open]:text-foreground [&_svg]:size-[15px] [&_svg]:text-passive"
              type="button"
            >
              <Settings aria-hidden="true" />
              <span className="tracking-[-0.01em]">Settings</span>
            </button>
          </DropdownMenuTrigger>
          <DropdownMenuContent align="start" side="top">
            <DropdownMenuItem onSelect={selection.goPrs}>
              <GitPullRequest aria-hidden="true" />
              Pull requests
            </DropdownMenuItem>
            <DropdownMenuItem onSelect={selection.goReview}>
              <Settings aria-hidden="true" />
              Reviews
            </DropdownMenuItem>
            <DropdownMenuItem disabled>
              <Search aria-hidden="true" />
              Search
              <DropdownMenuShortcut>⌘K</DropdownMenuShortcut>
            </DropdownMenuItem>
            {selection.activeProjectId && (
              <>
                <DropdownMenuSeparator />
                <DropdownMenuItem onSelect={() => selection.goSettings(selection.activeProjectId!)}>
                  <Settings aria-hidden="true" />
                  Project settings
                </DropdownMenuItem>
              </>
            )}
          </DropdownMenuContent>
        </DropdownMenu>
        <Tooltip>
          <TooltipTrigger asChild>
            <span
              aria-label={`Daemon ${daemonStatus.state}`}
              className={cn(
                "ml-auto mr-1.5 h-1.5 w-1.5 rounded-full",
                daemonStatus.state === "running" && eventsConnection !== "disconnected" ? "bg-success" : "bg-amber",
              )}
            />
          </TooltipTrigger>
          <TooltipContent side="top">
            daemon {daemonStatus.state}
            {eventsConnection === "disconnected" && " · events offline"}
          </TooltipContent>
        </Tooltip>
      </div>
    </aside>
  );
}

type Selection = ReturnType<typeof useSelection>;

function ProjectRow({
  workspace,
  expanded,
  selection,
  onToggle,
  onNewWorker,
}: {
  workspace: WorkspaceSummary;
  expanded: boolean;
  selection: Selection;
  onToggle: () => void;
  onNewWorker: () => void;
}) {
  const projectActive = selection.activeProjectId === workspace.id && !selection.activeSessionId;

  const onProjectClick = () => {
    if (!expanded) {
      onToggle();
      selection.goProject(workspace.id);
    } else if (projectActive) {
      onToggle();
    } else {
      selection.goProject(workspace.id);
    }
  };

  return (
    <div className="mb-px">
      {/* project-sidebar__proj-row */}
      <div className="group relative flex items-center rounded-[5px] hover:bg-white/[0.04]">
        <button
          aria-current={projectActive ? "page" : undefined}
          aria-expanded={expanded}
          className={cn(
            "flex min-w-0 flex-1 items-center gap-[9px] rounded-[5px] px-1.5 py-[7px] text-left transition-[padding] group-hover:pr-[34px]",
            projectActive && "bg-white/[0.07]",
          )}
          onClick={onProjectClick}
          type="button"
        >
          <ChevronRight
            className={cn(
              "h-[9px] w-[9px] shrink-0 text-[#444951] transition-transform",
              expanded && "rotate-90",
            )}
            strokeWidth={2.5}
            aria-hidden="true"
          />
          <span
            className={cn(
              "min-w-0 flex-1 truncate text-[13px]",
              projectActive ? "font-semibold text-foreground" : "font-medium text-muted-foreground",
            )}
          >
            {workspace.name}
          </span>
          <span className="shrink-0 font-mono text-[11px] text-passive group-hover:opacity-0">
            {workspace.sessions.length}
          </span>
        </button>
        {/* project-sidebar__proj-actions — reveal over the count slot on hover */}
        <div className="absolute right-1.5 flex items-center opacity-0 transition-opacity group-hover:opacity-100 group-focus-within:opacity-100">
          <NewWorkerButton onClick={onNewWorker} projectName={workspace.name} />
        </div>
      </div>

      {/* project-sidebar__sessions */}
      {expanded && workspace.sessions.length > 0 && (
        <div className="pb-2 pl-1 pt-0.5">
          {workspace.sessions.map((session) => {
            const active = selection.activeSessionId === session.id;
            return (
              <button
                aria-current={active ? "page" : undefined}
                aria-label={`Open ${session.title}`}
                className={cn(
                  "flex w-full items-center gap-[9px] rounded-[5px] py-[5px] pl-2 pr-1.5 text-left transition-colors",
                  active ? "bg-white/[0.07]" : "hover:bg-white/[0.04]",
                )}
                key={session.id}
                onClick={() => selection.goSession(workspace.id, session.id)}
                type="button"
              >
                <SessionDot session={session} />
                <span className="min-w-0 flex-1">
                  <span
                    className={cn("block truncate text-[12px]", active ? "text-foreground" : "text-muted-foreground")}
                  >
                    {session.title}
                  </span>
                  <span className="block truncate font-mono text-[10px] text-[#444951]">{session.id}</span>
                </span>
              </button>
            );
          })}
        </div>
      )}
    </div>
  );
}

function CreateProjectButton({ onCreateProject }: Pick<SidebarProps, "onCreateProject">) {
  const [error, setError] = useState<string | null>(null);
  const [isChoosingPath, setIsChoosingPath] = useState(false);

  const choosePath = async () => {
    setError(null);
    setIsChoosingPath(true);
    try {
      const selectedPath = await aoBridge.app.chooseDirectory();
      if (selectedPath) await onCreateProject({ path: selectedPath });
    } catch (err) {
      setError(err instanceof Error ? err.message : "Could not add project");
    } finally {
      setIsChoosingPath(false);
    }
  };

  return (
    <>
      <Tooltip>
        <TooltipTrigger asChild>
          <button
            aria-label="New project"
            className="grid h-[18px] w-[18px] place-items-center rounded-[4px] text-[#444951] transition-colors hover:bg-white/[0.04] hover:text-muted-foreground"
            disabled={isChoosingPath}
            onClick={choosePath}
            type="button"
          >
            <Plus className="h-[13px] w-[13px]" aria-hidden="true" />
          </button>
        </TooltipTrigger>
        <TooltipContent>{isChoosingPath ? "Opening…" : "New project"}</TooltipContent>
      </Tooltip>
      {error && (
        <span className="sr-only" role="status">
          {error}
        </span>
      )}
    </>
  );
}

function NewWorkerButton({ onClick, projectName }: { onClick: () => void; projectName: string }) {
  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <button
          aria-label={`New worker in ${projectName}`}
          className="grid h-[22px] w-[22px] shrink-0 place-items-center rounded-[5px] text-passive transition-colors hover:bg-white/[0.07] hover:text-foreground"
          onClick={(event) => {
            event.stopPropagation();
            onClick();
          }}
          type="button"
        >
          <Plus className="h-[13px] w-[13px]" aria-hidden="true" />
        </button>
      </TooltipTrigger>
      <TooltipContent>New worker in {projectName}</TooltipContent>
    </Tooltip>
  );
}

function CollapsedRail({
  workspaces,
  onCreateProject,
}: {
  workspaces: WorkspaceSummary[];
  onCreateProject: SidebarProps["onCreateProject"];
}) {
  const { isHome, activeProjectId, activeSessionId, goHome, goProject } = useSelection();
  const { toggleSidebar } = useUiStore();
  return (
    <aside className="flex h-full w-12 shrink-0 flex-col items-center border-r border-border bg-[#08090b] py-3.5">
      <Tooltip>
        <TooltipTrigger asChild>
          <button
            aria-label="Orchestrator board"
            className={cn(
              "grid h-9 w-9 place-items-center rounded-lg transition-colors",
              isHome ? "bg-white/[0.07]" : "hover:bg-white/[0.04]",
            )}
            onClick={goHome}
            type="button"
          >
            <span className="grid h-[22px] w-[22px] place-items-center rounded-[6px] bg-accent text-accent-foreground">
              <Waypoints className="h-3.5 w-3.5" aria-hidden="true" />
            </span>
          </button>
        </TooltipTrigger>
        <TooltipContent side="right">Orchestrator board</TooltipContent>
      </Tooltip>

      <div className="mt-2 flex min-h-0 flex-1 flex-col items-center gap-1 overflow-y-auto">
        {workspaces.map((workspace) => (
          <Tooltip key={workspace.id}>
            <TooltipTrigger asChild>
              <button
                aria-label={workspace.name}
                className={cn(
                  "grid h-9 w-9 place-items-center rounded-lg text-[13px] font-semibold transition-colors",
                  activeProjectId === workspace.id && !activeSessionId
                    ? "bg-white/[0.07] text-foreground"
                    : "text-muted-foreground hover:bg-white/[0.04]",
                )}
                onClick={() => goProject(workspace.id)}
                type="button"
              >
                {workspace.name.charAt(0).toUpperCase()}
              </button>
            </TooltipTrigger>
            <TooltipContent side="right">{workspace.name}</TooltipContent>
          </Tooltip>
        ))}
      </div>

      <div className="flex flex-col items-center gap-1 border-t border-border pt-2">
        <CreateProjectButton onCreateProject={onCreateProject} />
        <Tooltip>
          <TooltipTrigger asChild>
            <button
              aria-label="Expand sidebar"
              className="grid h-9 w-9 place-items-center rounded-lg text-passive transition-colors hover:bg-white/[0.04] hover:text-foreground"
              onClick={toggleSidebar}
              type="button"
            >
              <PanelLeft className="h-4 w-4" aria-hidden="true" />
            </button>
          </TooltipTrigger>
          <TooltipContent side="right">Expand sidebar · ⌘B</TooltipContent>
        </Tooltip>
      </div>
    </aside>
  );
}
