import { ChevronsUpDown, Folder, Plus, Search, Settings, Waypoints } from "lucide-react";
import { useState } from "react";
import { sessionIsActive, sessionNeedsAttention, type WorkspaceSummary } from "../types/workspace";
import { useUiStore } from "../stores/ui-store";
import { aoBridge } from "../lib/bridge";
import { useEventsConnection } from "../hooks/useEventsConnection";
import { Tooltip, TooltipContent, TooltipTrigger } from "./ui/tooltip";
import { cn } from "../lib/utils";

type SidebarProps = {
	daemonStatus: { state: string; message?: string };
	workspaceError?: string;
	workspaces: WorkspaceSummary[];
	onCreateProject: (input: { path: string }) => Promise<void>;
	onNewWorker: (projectId: string) => void;
};

function fleetSummary(workspaces: WorkspaceSummary[]) {
	const sessions = workspaces.flatMap((workspace) => workspace.sessions);
	const agents = sessions.filter(sessionIsActive).length;
	const needYou = sessions.filter(sessionNeedsAttention).length;
	return { agents, needYou };
}

export function Sidebar({ daemonStatus, workspaceError, workspaces, onCreateProject, onNewWorker }: SidebarProps) {
	const {
		isSidebarOpen,
		view,
		selectedSessionId,
		selectedWorkspaceId,
		selectOrchestrator,
		selectSession,
		selectWorkspace,
	} = useUiStore();
	const { agents, needYou } = fleetSummary(workspaces);
	const eventsConnection = useEventsConnection();

	if (!isSidebarOpen) {
		return (
			<CollapsedRail agents={agents} needYou={needYou} onCreateProject={onCreateProject} workspaces={workspaces} />
		);
	}

	return (
		<aside className="flex h-full w-60 shrink-0 flex-col border-r border-border bg-sidebar text-sidebar-foreground">
			<div className="min-h-0 flex-1 overflow-y-auto p-2">
				{/* Orchestrator anchor — ReverbCode's one addition over emdash. */}
				<button
					aria-label="Orchestrator"
					className={cn(
						"group relative mb-2 flex h-[38px] w-full items-center gap-2.5 rounded-lg pl-3 pr-2.5 text-left transition-colors",
						"before:absolute before:left-0 before:top-2 before:bottom-2 before:w-0.5 before:rounded-r before:bg-accent",
						view === "orchestrator" ? "bg-overlay" : "bg-raised hover:bg-overlay",
					)}
					onClick={selectOrchestrator}
					type="button"
				>
					<Waypoints className="h-4 w-4 shrink-0 text-accent" aria-hidden="true" />
					<span className="text-[13.5px] font-semibold text-foreground">Orchestrator</span>
					<span className="ml-auto whitespace-nowrap font-mono text-[10px] text-passive">{agents} agents</span>
				</button>

				<div className="flex h-[34px] items-center gap-1.5 px-2">
					<span className="font-mono text-[11px] uppercase tracking-[0.12em] text-passive">Projects</span>
					<CreateProjectButton onCreateProject={onCreateProject} />
				</div>

				{workspaceError ? (
					<div className="px-3 py-4">
						<p className="text-[12px] text-foreground">Could not load projects.</p>
						<p className="mt-1 text-[11px] text-passive">{workspaceError}</p>
					</div>
				) : workspaces.length === 0 ? (
					<div className="px-3 py-4">
						<p className="text-[12px] text-passive">No projects yet.</p>
						<p className="mt-1 text-[11px] text-passive">
							Click <span className="text-foreground">+</span> above to register a git repo.
						</p>
					</div>
				) : (
					workspaces.map((workspace) => (
						<section key={workspace.id} className="mb-1">
							<div
								className={cn(
									"group flex h-8 w-full items-center rounded-lg pr-1 transition-colors",
									selectedWorkspaceId === workspace.id && view !== "session"
										? "bg-raised text-foreground"
										: "text-muted-foreground hover:bg-surface",
								)}
							>
								<button
									aria-label={`Select ${workspace.name}`}
									className="flex h-full min-w-0 flex-1 items-center gap-2 rounded-lg px-2 text-left"
									onClick={() => selectWorkspace(workspace.id)}
									type="button"
								>
									<Folder className="h-3.5 w-3.5 shrink-0 text-passive" aria-hidden="true" />
									<span className="min-w-0 flex-1 truncate text-[13.5px]">{workspace.name}</span>
								</button>
								<NewWorkerButton
									onClick={() => onNewWorker(workspace.id)}
									projectName={workspace.name}
									visible={selectedWorkspaceId === workspace.id && view !== "session"}
								/>
							</div>

							{workspace.sessions.map((session) => {
								const active = view === "session" && selectedSessionId === session.id;
								return (
									<button
										aria-label={session.title}
										className={cn(
											"relative flex h-8 w-full items-center rounded-lg pl-[30px] pr-2 text-left transition-colors",
											active
												? "bg-raised text-foreground before:absolute before:left-5 before:top-2 before:bottom-2 before:w-0.5 before:rounded before:bg-accent"
												: "text-muted-foreground hover:bg-surface",
										)}
										key={session.id}
										onClick={() => selectSession(session.id, workspace.id)}
										type="button"
									>
										<span className="min-w-0 flex-1 truncate text-[13.5px]">{session.title}</span>
									</button>
								);
							})}
						</section>
					))
				)}
			</div>

			<div className="border-t border-border p-2">
				<FooterRow icon={<Search className="h-[15px] w-[15px]" aria-hidden="true" />} label="Search" shortcut="⌘K" />
				<FooterRow
					icon={<Settings className="h-[15px] w-[15px]" aria-hidden="true" />}
					label="Settings"
					shortcut="⌘,"
				/>
			</div>

			<div className="flex items-center gap-2.5 border-t border-border px-2.5 py-2">
				<span className="grid h-[22px] w-[22px] shrink-0 place-items-center rounded-md bg-accent text-[11px] font-semibold text-accent-foreground">
					R
				</span>
				<div className="min-w-0">
					<div className="truncate text-[12.5px] text-foreground">ReverbCode</div>
					<div className="truncate font-mono text-[10px] text-passive">
						daemon {daemonStatus.state}
						{eventsConnection === "disconnected" && " · events offline"}
					</div>
				</div>
				<ChevronsUpDown className="ml-auto h-3.5 w-3.5 shrink-0 text-passive" aria-hidden="true" />
			</div>
		</aside>
	);
}

function FooterRow({ icon, label, shortcut }: { icon: React.ReactNode; label: string; shortcut: string }) {
	return (
		<button
			className="flex h-7 w-full items-center gap-2.5 rounded-lg px-2 text-left text-[13px] text-muted-foreground transition-colors hover:bg-surface [&_svg]:text-passive"
			type="button"
		>
			{icon}
			<span className="min-w-0 flex-1 truncate">{label}</span>
			<span className="font-mono text-[10px] text-passive">{shortcut}</span>
		</button>
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
						className="ml-auto grid h-6 w-6 place-items-center rounded-md text-passive transition-colors hover:bg-surface hover:text-foreground"
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

function NewWorkerButton({
	onClick,
	projectName,
	visible,
}: {
	onClick: () => void;
	projectName: string;
	visible: boolean;
}) {
	return (
		<Tooltip>
			<TooltipTrigger asChild>
				<button
					aria-label={`New worker in ${projectName}`}
					className={cn(
						"grid h-6 w-6 shrink-0 place-items-center rounded-md text-passive transition-all hover:bg-overlay hover:text-foreground group-hover:opacity-100",
						visible ? "opacity-100" : "opacity-0",
					)}
					onClick={onClick}
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
	agents,
	needYou,
	workspaces,
	onCreateProject,
}: {
	agents: number;
	needYou: number;
	workspaces: WorkspaceSummary[];
	onCreateProject: SidebarProps["onCreateProject"];
}) {
	const { view, selectedWorkspaceId, selectOrchestrator, selectWorkspace } = useUiStore();
	return (
		<aside className="flex h-full w-12 shrink-0 flex-col items-center border-r border-border bg-sidebar py-2">
			<Tooltip>
				<TooltipTrigger asChild>
					<button
						aria-label="Orchestrator"
						className={cn(
							"relative grid h-9 w-9 place-items-center rounded-lg transition-colors",
							"before:absolute before:left-0 before:top-2 before:bottom-2 before:w-0.5 before:rounded-r before:bg-accent",
							view === "orchestrator" ? "bg-overlay" : "bg-raised hover:bg-overlay",
						)}
						onClick={selectOrchestrator}
						type="button"
					>
						<Waypoints className="h-4 w-4 text-accent" aria-hidden="true" />
					</button>
				</TooltipTrigger>
				<TooltipContent side="right">
					Orchestrator · {agents} agents · {needYou} need you
				</TooltipContent>
			</Tooltip>

			<div className="mt-2 flex min-h-0 flex-1 flex-col items-center gap-1 overflow-y-auto">
				{workspaces.map((workspace) => (
					<Tooltip key={workspace.id}>
						<TooltipTrigger asChild>
							<button
								aria-label={workspace.name}
								className={cn(
									"grid h-9 w-9 place-items-center rounded-lg transition-colors",
									selectedWorkspaceId === workspace.id && view !== "session"
										? "bg-raised text-foreground"
										: "text-muted-foreground hover:bg-surface",
								)}
								onClick={() => selectWorkspace(workspace.id)}
								type="button"
							>
								<Folder className="h-4 w-4" aria-hidden="true" />
							</button>
						</TooltipTrigger>
						<TooltipContent side="right">{workspace.name}</TooltipContent>
					</Tooltip>
				))}
			</div>

			<div className="flex flex-col items-center gap-1 border-t border-border pt-2">
				<CreateProjectButton onCreateProject={onCreateProject} />
				<button
					aria-label="Search"
					className="grid h-9 w-9 place-items-center rounded-lg text-passive transition-colors hover:bg-surface hover:text-foreground"
					type="button"
				>
					<Search className="h-4 w-4" aria-hidden="true" />
				</button>
			</div>
		</aside>
	);
}
