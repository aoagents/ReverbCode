import {
  Archive,
  ArrowUp,
  Bot,
  ChevronLeft,
  ChevronRight,
  Circle,
  FolderGit2,
  FolderPlus,
  ListFilter,
  PanelLeft,
  Pin,
  Plus,
  Search,
  Settings,
  TerminalSquare,
} from "lucide-react";
import * as Dialog from "@radix-ui/react-dialog";
import { FormEvent, MouseEvent, useState } from "react";
import type { AgentProvider, WorkspaceSummary } from "../types/workspace";
import { useUiStore } from "../stores/ui-store";
import { aoBridge } from "../lib/bridge";
import { Tooltip, TooltipContent, TooltipTrigger } from "./ui/tooltip";
import { cn } from "../lib/utils";

type SidebarProps = {
  daemonStatus: { state: string; message?: string };
  onCreateProject: (input: { path: string }) => Promise<void>;
  onCreateTask: (input: { projectId: string; prompt: string; branch?: string; harness?: AgentProvider }) => Promise<void>;
  workspaces: WorkspaceSummary[];
};

const agentOptions: { value: AgentProvider; label: string }[] = [
  { value: "codex", label: "Codex" },
  { value: "claude-code", label: "Claude" },
  { value: "opencode", label: "OpenCode" },
  { value: "amp", label: "Amp" },
  { value: "goose", label: "Goose" },
  { value: "kiro", label: "Kiro" },
  { value: "kimi", label: "Kimi" },
  { value: "crush", label: "Crush" },
  { value: "vibe", label: "Vibe" },
];

export function Sidebar({ daemonStatus, onCreateProject, onCreateTask, workspaces }: SidebarProps) {
  const {
    isSidebarOpen,
    activePane,
    selectedSessionId,
    selectedWorkspaceId,
    setActivePane,
    toggleSidebar,
    selectSession,
    selectWorkspace,
  } = useUiStore();

  return (
    <aside
      className={cn(
        "flex h-screen min-w-0 flex-col border-r border-[#dedede] bg-[#f3f3f3] text-[#242424]",
        !isSidebarOpen && "items-center",
      )}
    >
      <div className={cn("flex h-[4.25rem] shrink-0 items-start px-3 pt-3", isSidebarOpen ? "justify-end" : "justify-center")}>
        {isSidebarOpen && (
          <div className="mr-8 flex items-center gap-5 text-[#707070]">
            <button className="grid h-8 w-8 place-items-center rounded-xl hover:bg-[#e8e8e8]" aria-label="Back" type="button">
              <ChevronLeft className="h-4 w-4" aria-hidden="true" />
            </button>
            <button className="grid h-8 w-8 place-items-center rounded-xl text-[#a4a4a4] hover:bg-[#e8e8e8]" aria-label="Forward" type="button">
              <ChevronRight className="h-4 w-4" aria-hidden="true" />
            </button>
          </div>
        )}
        <button
          aria-label="Toggle sidebar"
          aria-expanded={isSidebarOpen}
          className="grid h-9 w-9 shrink-0 place-items-center rounded-2xl bg-[#e7e7e7] text-[#2f2f2f] transition-colors hover:bg-[#dedede]"
          onClick={toggleSidebar}
          title="Toggle sidebar (⌘B)"
          type="button"
        >
          <PanelLeft className="h-4 w-4" aria-hidden="true" />
        </button>
      </div>

      {isSidebarOpen ? (
        <div className="px-3 pb-4">
          <button
            className={cn(
              "ao-sidebar-orchestrator flex h-7 w-full min-w-0 items-center gap-2.5 rounded-xl px-4 text-left font-medium text-[#666] transition-colors hover:bg-[#e8e8e8]",
              activePane === "terminal" && "bg-[#e5e5e5] text-[#202020]",
            )}
            onClick={() => setActivePane("terminal")}
            type="button"
          >
            <TerminalSquare className="h-4 w-4 shrink-0 text-[#696969]" aria-hidden="true" />
            <span className="min-w-0 flex-1 truncate">Orchestrator</span>
            <span className="ao-sidebar-status inline-flex shrink-0 items-center gap-1 font-semibold text-[#b8b8b8]">
              <Circle className={cn("h-1.5 w-1.5 fill-current", daemonStatus.state === "ready" ? "text-[#2fa84f]" : "text-[#c4a15c]")} />
              <span>{daemonStatus.state}</span>
            </span>
          </button>
        </div>
      ) : (
        <div className="px-2 pb-3">
          <Tooltip>
            <TooltipTrigger asChild>
              <button
                aria-label="Orchestrator"
                className={cn(
                  "grid h-9 w-9 place-items-center rounded-xl transition-colors hover:bg-[#e8e8e8]",
                  activePane === "terminal" && "bg-[#e5e5e5]",
                )}
                onClick={() => setActivePane("terminal")}
                type="button"
              >
                <TerminalSquare className="h-4 w-4 text-[#696969]" aria-hidden="true" />
              </button>
            </TooltipTrigger>
            <TooltipContent side="right">Orchestrator</TooltipContent>
          </Tooltip>
        </div>
      )}

      <div className={cn("min-h-0 flex-1 overflow-y-auto", isSidebarOpen ? "w-full px-3" : "w-full px-2 py-3")}>
        {isSidebarOpen && (
          <div className="mb-7 flex items-center justify-between px-4">
            <span className="text-[11px] font-medium uppercase tracking-[0.18em] text-[#b8b8b8]">Projects</span>
            <div className="flex items-center gap-4 text-[#656565]">
              <button aria-label="Filter projects" className="grid h-7 w-7 place-items-center rounded-lg hover:bg-[#e8e8e8]" type="button">
                <ListFilter className="h-4 w-4" aria-hidden="true" />
              </button>
              <CreateProjectButton onCreateProject={onCreateProject} />
            </div>
          </div>
        )}

        {workspaces.map((workspace) =>
          isSidebarOpen ? (
            <ExpandedWorkspace
              isActive={selectedWorkspaceId === workspace.id}
              key={workspace.id}
              onCreateTask={onCreateTask}
              onSelect={() => selectWorkspace(workspace.id)}
              selectedSessionId={selectedSessionId}
              selectSession={selectSession}
              workspace={workspace}
              workspaces={workspaces}
            />
          ) : (
            <CollapsedWorkspace
              isActive={selectedWorkspaceId === workspace.id}
              key={workspace.id}
              onSelect={() => selectWorkspace(workspace.id)}
              workspace={workspace}
            />
          ),
        )}
      </div>

      <div className={cn("border-t border-[#dedede]", isSidebarOpen ? "w-full" : "w-full p-2")}>
        {isSidebarOpen ? (
          <EmdashFooter />
        ) : (
          <Tooltip>
            <TooltipTrigger asChild>
              <button
                aria-label="Search"
                className="grid h-9 w-9 place-items-center rounded-xl text-[#696969] transition-colors hover:bg-[#e8e8e8]"
                type="button"
              >
                <Search className="h-4 w-4" aria-hidden="true" />
              </button>
            </TooltipTrigger>
            <TooltipContent side="right">Search ⌘K</TooltipContent>
          </Tooltip>
        )}
      </div>
    </aside>
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
            className="grid h-7 w-7 place-items-center rounded-lg hover:bg-[#e8e8e8]"
            disabled={isChoosingPath}
            onClick={choosePath}
            type="button"
          >
            <FolderPlus className="h-4 w-4" aria-hidden="true" />
          </button>
        </TooltipTrigger>
        <TooltipContent>{isChoosingPath ? "Opening..." : "New project"}</TooltipContent>
      </Tooltip>
      {error && <span className="sr-only" role="status">{error}</span>}
    </>
  );
}

function CreateTaskComposer({
  initialWorkspace,
  onCreateTask,
  workspaces,
}: {
  initialWorkspace: WorkspaceSummary;
  onCreateTask: SidebarProps["onCreateTask"];
  workspaces: WorkspaceSummary[];
}) {
  const [open, setOpen] = useState(false);
  const [projectId, setProjectId] = useState(initialWorkspace.id);
  const [agent, setAgent] = useState<AgentProvider>("codex");
  const [prompt, setPrompt] = useState("");
  const [branch, setBranch] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [isSubmitting, setIsSubmitting] = useState(false);
  const selectedWorkspace = workspaces.find((workspace) => workspace.id === projectId) ?? initialWorkspace;
  const branchOptions = Array.from(
    new Set(["", "main", ...selectedWorkspace.sessions.map((session) => session.branch).filter(Boolean)]),
  );

  const submit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    setError(null);
    setIsSubmitting(true);
    try {
      await onCreateTask({ projectId, prompt: prompt.trim(), branch: branch.trim(), harness: agent });
      setPrompt("");
      setBranch("");
      setOpen(false);
    } catch (err) {
      setError(err instanceof Error ? err.message : "Could not start task");
    } finally {
      setIsSubmitting(false);
    }
  };

  const openComposer = (event: MouseEvent<HTMLButtonElement>) => {
    event.stopPropagation();
    setProjectId(initialWorkspace.id);
    setOpen(true);
  };

  return (
    <Dialog.Root open={open} onOpenChange={setOpen}>
      <Tooltip>
        <TooltipTrigger asChild>
          <Dialog.Trigger asChild>
            <button
              aria-label={`Start task in ${initialWorkspace.name}`}
              className="grid h-6 w-6 shrink-0 place-items-center rounded-lg text-[#707070] transition-colors hover:bg-[#d8d8d8] hover:text-[#202020]"
              onClick={openComposer}
              title="Start task"
              type="button"
            >
              <Plus className="h-3.5 w-3.5" aria-hidden="true" />
            </button>
          </Dialog.Trigger>
        </TooltipTrigger>
        <TooltipContent>Start task</TooltipContent>
      </Tooltip>
      <Dialog.Portal>
        <Dialog.Overlay className="fixed inset-0 z-40 bg-black/30 backdrop-blur-sm" />
        <Dialog.Content
          className="fixed left-1/2 top-1/2 z-50 w-[min(54rem,calc(100vw-2rem))] -translate-x-1/2 -translate-y-1/2 rounded-2xl border border-border bg-popover p-4 text-popover-foreground shadow-[0_24px_80px_rgb(0_0_0_/_0.35)] focus-visible:outline-none"
          onClick={(event) => event.stopPropagation()}
        >
          <Dialog.Title className="sr-only">Start task</Dialog.Title>
          <Dialog.Description className="sr-only">Create a worker session.</Dialog.Description>
          <form className="grid gap-3" onSubmit={submit}>
            <label className="sr-only" htmlFor="task-prompt">Prompt</label>
            <textarea
              autoFocus
              className="min-h-28 w-full resize-none rounded-xl border border-border bg-background px-4 py-3 text-[15px] leading-6 text-foreground outline-none placeholder:text-muted-foreground focus:border-ring"
              id="task-prompt"
              onChange={(event) => setPrompt(event.target.value)}
              placeholder="What do you want to do?"
              required
              value={prompt}
            />
            <div className="flex min-w-0 flex-wrap items-center gap-2">
              <label className="sr-only" htmlFor="task-agent">Agent</label>
              <select
                className="h-9 min-w-28 rounded-xl border border-border bg-background px-3 text-sm font-medium text-foreground outline-none focus:border-ring"
                id="task-agent"
                onChange={(event) => setAgent(event.target.value as AgentProvider)}
                value={agent}
              >
                {agentOptions.map((option) => (
                  <option key={option.value} value={option.value}>{option.label}</option>
                ))}
              </select>

              <label className="sr-only" htmlFor="task-project">Project</label>
              <select
                className="h-9 min-w-0 flex-1 rounded-xl border border-border bg-background px-3 text-sm text-foreground outline-none focus:border-ring sm:max-w-64"
                id="task-project"
                onChange={(event) => {
                  setProjectId(event.target.value);
                  setBranch("");
                }}
                value={projectId}
              >
                {workspaces.map((workspace) => (
                  <option key={workspace.id} value={workspace.id}>{workspace.name}</option>
                ))}
              </select>

              <label className="sr-only" htmlFor="task-branch">Branch</label>
              <select
                className="h-9 min-w-0 flex-1 rounded-xl border border-border bg-background px-3 font-mono text-sm text-foreground outline-none focus:border-ring sm:max-w-52"
                id="task-branch"
                onChange={(event) => setBranch(event.target.value)}
                value={branch}
              >
                {branchOptions.map((option) => (
                  <option key={option || "default"} value={option}>
                    {option || "Default branch"}
                  </option>
                ))}
              </select>

              <button
                aria-label="Start task"
                className="grid h-10 w-10 shrink-0 place-items-center rounded-full bg-primary text-primary-foreground transition-colors hover:opacity-85 disabled:cursor-not-allowed disabled:opacity-45"
                disabled={isSubmitting || prompt.trim().length === 0}
                type="submit"
              >
                <ArrowUp className="h-5 w-5" aria-hidden="true" />
              </button>
            </div>
            {error && <p className="text-xs text-red-600 dark:text-red-300">{error}</p>}
          </form>
        </Dialog.Content>
      </Dialog.Portal>
    </Dialog.Root>
  );
}

function ExpandedWorkspace({
  workspace,
  workspaces,
  isActive,
  selectedSessionId,
  onCreateTask,
  onSelect,
  selectSession,
}: {
  workspace: WorkspaceSummary;
  workspaces: WorkspaceSummary[];
  isActive: boolean;
  selectedSessionId: string;
  onCreateTask: SidebarProps["onCreateTask"];
  onSelect: () => void;
  selectSession: (sessionId: string) => void;
}) {
  const activeSessions = workspace.sessions.filter((session) => session.status !== "stopped");

  return (
    <section className="mb-4">
      <div
        aria-label={`Select ${workspace.name}`}
        className={cn(
          "group relative flex h-7 w-full items-center gap-2.5 rounded-xl px-4 text-left text-[13px] font-medium text-[#626262] transition-colors hover:bg-[#e8e8e8]",
          isActive && "text-[#4f4f4f]",
        )}
        onClick={onSelect}
        onKeyDown={(event) => {
          if (event.key === "Enter" || event.key === " ") {
            event.preventDefault();
            onSelect();
          }
        }}
        role="button"
        tabIndex={0}
      >
        <WorkspaceIcon />
        <span className="min-w-0 flex-1 truncate">{workspace.name}</span>
        <CreateTaskComposer initialWorkspace={workspace} onCreateTask={onCreateTask} workspaces={workspaces} />
      </div>

      <div className="mt-1.5 space-y-1">
        {activeSessions.map((session) => (
          <div
            className={cn(
              "group/session relative flex h-7 w-full cursor-default items-center gap-2.5 rounded-xl px-4 pl-10 pr-3 text-left text-[12px] transition-colors hover:bg-[#e8e8e8]",
              selectedSessionId === session.id && "bg-[#e4e4e4] text-[#202020]",
            )}
            key={session.id}
            onKeyDown={(event) => {
              if (event.key === "Enter" || event.key === " ") {
                event.preventDefault();
                selectSession(session.id);
              }
            }}
            onClick={() => selectSession(session.id)}
            role="button"
            tabIndex={0}
          >
            <Bot className="h-3.5 w-3.5 shrink-0 text-[#666]" aria-hidden="true" />
            <span className="min-w-0 flex-1 truncate font-medium leading-4">{session.title}</span>
            <span className="absolute right-2 hidden items-center gap-0.5 rounded-lg bg-[#e8e8e8] pl-2 group-hover/session:flex">
              <button
                aria-label="Pin task"
                className="grid h-5 w-5 place-items-center rounded-lg text-[#777] hover:bg-[#d8d8d8] hover:text-[#202020]"
                onClick={(event) => event.stopPropagation()}
                title="Pin task"
                type="button"
              >
                <Pin className="h-3 w-3" aria-hidden="true" />
              </button>
              <button
                aria-label="Archive task"
                className="grid h-5 w-5 place-items-center rounded-lg text-[#777] hover:bg-[#d8d8d8] hover:text-[#202020]"
                onClick={(event) => event.stopPropagation()}
                title="Archive task"
                type="button"
              >
                <Archive className="h-3 w-3" aria-hidden="true" />
              </button>
            </span>
          </div>
        ))}
      </div>
    </section>
  );
}

function CollapsedWorkspace({
  workspace,
  isActive,
  onSelect,
}: {
  workspace: WorkspaceSummary;
  isActive: boolean;
  onSelect: () => void;
}) {
  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <button
          aria-label={workspace.name}
          className={cn(
            "relative mb-2 grid h-8 w-8 place-items-center rounded-md transition-colors hover:bg-muted/50",
            isActive && "bg-muted",
          )}
          onClick={onSelect}
          type="button"
        >
          {isActive && <span className="absolute inset-y-1 -left-2 w-0.5 rounded-r bg-foreground" />}
          <WorkspaceIcon collapsed />
        </button>
      </TooltipTrigger>
      <TooltipContent side="right">
        <div className="grid gap-1">
          <span>{workspace.name}</span>
          <span className="text-[11px] text-muted-foreground">{workspace.path}</span>
        </div>
      </TooltipContent>
    </Tooltip>
  );
}

function WorkspaceIcon({ collapsed = false }: { collapsed?: boolean }) {
  return <FolderGit2 className={cn("shrink-0 text-[#696969]", collapsed ? "h-4 w-4" : "h-4 w-4")} aria-hidden="true" />;
}

function EmdashFooter() {
  return (
    <div className="text-[#666]">
      <div className="space-y-1 px-3 py-4">
        <button className="ao-sidebar-action flex h-9 w-full items-center gap-3 rounded-xl bg-[#e9e9e9] px-4 text-left font-medium text-[#202020]" type="button">
          <Search className="h-4 w-4 shrink-0" aria-hidden="true" />
          <span className="min-w-0 flex-1 truncate">Search...</span>
          <span className="ao-sidebar-shortcut shrink-0 font-semibold text-[#2a2a2a]">⌘K</span>
        </button>
        <button className="ao-sidebar-action flex h-9 w-full items-center gap-3 rounded-xl px-4 text-left font-medium hover:bg-[#e8e8e8]" type="button">
          <Settings className="h-4 w-4 shrink-0" aria-hidden="true" />
          <span className="min-w-0 flex-1 truncate">Settings</span>
          <span className="ao-sidebar-shortcut shrink-0 font-semibold">⌘,</span>
        </button>
      </div>
    </div>
  );
}
