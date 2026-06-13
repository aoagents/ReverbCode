import { useNavigate, useParams, useRouterState } from "@tanstack/react-router";
import {
	ChevronRight,
	GitPullRequest,
	Moon,
	MoreHorizontal,
	Plus,
	Search,
	Settings,
	Sun,
	Trash2,
	Waypoints,
} from "lucide-react";
import { useState } from "react";
import {
	attentionZone,
	sessionIsActive,
	type WorkspaceSession,
	type WorkspaceSummary,
	workerSessions,
} from "../types/workspace";
import { aoBridge } from "../lib/bridge";
import { useEventsConnection } from "../hooks/useEventsConnection";
import { useResizable } from "../hooks/useResizable";
import {
	DropdownMenu,
	DropdownMenuContent,
	DropdownMenuItem,
	DropdownMenuSeparator,
	DropdownMenuShortcut,
	DropdownMenuTrigger,
} from "./ui/dropdown-menu";
import {
	Sidebar as SidebarRoot,
	SidebarContent,
	SidebarFooter,
	SidebarGroup,
	SidebarGroupContent,
	SidebarGroupLabel,
	SidebarHeader,
	SidebarMenu,
	SidebarMenuAction,
	SidebarMenuButton,
	SidebarMenuItem,
	SidebarMenuSub,
	SidebarMenuSubButton,
	SidebarMenuSubItem,
	SidebarTrigger,
	useSidebar,
} from "./ui/sidebar";
import { Tooltip, TooltipContent, TooltipTrigger } from "./ui/tooltip";
import { cn } from "../lib/utils";
import { useUiStore } from "../stores/ui-store";

// The macOS hiddenInset traffic lights and the fixed TitlebarNav overlay live
// in the full-width topbar's left inset (_shell renders the bar above the
// sidebar row); the sidebar itself starts below the 56px header, so its border
// never crosses the titlebar strip.
const isMac = typeof navigator !== "undefined" && /Mac|iPod|iPhone|iPad/.test(navigator.userAgent);
const noDragStyle = isMac ? ({ WebkitAppRegion: "no-drag" } as React.CSSProperties) : undefined;

type SidebarProps = {
	daemonStatus: { state: string; message?: string };
	workspaceError?: string;
	workspaces: WorkspaceSummary[];
	onCreateProject: (input: { path: string }) => Promise<void>;
	onRemoveProject: (projectId: string) => Promise<void>;
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
		goSettings: (projectId: string) => void navigate({ to: "/projects/$projectId/settings", params: { projectId } }),
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
			className={cn(
				"mt-px h-1.5 w-1.5 shrink-0 rounded-full",
				working ? "animate-status-pulse bg-working" : "bg-passive",
			)}
		/>
	);
}

// Built on shadcn's sidebar primitives (components/ui/sidebar): the provider in
// _shell owns open state (synced to the ui-store) and `collapsible="icon"`
// replaces the old hand-rolled CollapsedRail — the same tree restyles itself
// via group-data-[collapsible=icon] into the 48px letter rail.
export function Sidebar({ daemonStatus, workspaceError, workspaces, onCreateProject, onRemoveProject }: SidebarProps) {
	const selection = useSelection();
	const eventsConnection = useEventsConnection();
	const { state } = useSidebar();
	const isCollapsed = state === "collapsed";
	const theme = useUiStore((s) => s.theme);
	const toggleTheme = useUiStore((s) => s.toggleTheme);
	// Disclosure state: projects are expanded by default; a project id present in
	// this set is collapsed (sessions hidden).
	const [collapsedIds, setCollapsedIds] = useState<ReadonlySet<string>>(() => new Set());
	const toggleCollapsed = (id: string) =>
		setCollapsedIds((prev) => {
			const next = new Set(prev);
			next.has(id) ? next.delete(id) : next.add(id);
			return next;
		});
	// agent-orchestrator's sidebar resize: drag the right edge (200–420px,
	// persisted), double-click to reset to 240px. Drives --ao-sidebar-w on :root,
	// which the provider forwards into shadcn's --sidebar-width.
	const { onPointerDown: onResizePointerDown, onDoubleClick: onResizeDoubleClick } = useResizable({
		cssVar: "--ao-sidebar-w",
		storageKey: "ao-sidebar-w",
		defaultWidth: 240,
		min: 200,
		max: 420,
		edge: "right",
	});

	return (
		// The container is fixed-positioned by the shadcn primitive; offset it
		// below the 56px shell topbar so the bar runs edge-to-edge above it
		// (same override as shadcn's header-above-sidebar block).
		<SidebarRoot collapsible="icon" className="border-border top-14 h-[calc(100svh-3.5rem)]!">
			<SidebarHeader className="gap-0 p-0 px-[7px] pt-3.5 group-data-[collapsible=icon]:px-1.5">
				{/* Brand (project-sidebar__brand); in the icon rail it becomes the old
            36px board button wrapping the 22px accent mark. */}
				<div className="flex shrink-0 items-center gap-2.5 px-2 pb-[18px] group-data-[collapsible=icon]:justify-center group-data-[collapsible=icon]:px-0 group-data-[collapsible=icon]:pb-2">
					<Tooltip>
						<TooltipTrigger asChild>
							<button
								aria-label="Orchestrator board"
								className={cn(
									"grid h-[22px] w-[22px] shrink-0 place-items-center rounded-[6px] bg-accent text-accent-foreground",
									"group-data-[collapsible=icon]:size-9 group-data-[collapsible=icon]:rounded-lg group-data-[collapsible=icon]:bg-transparent group-data-[collapsible=icon]:text-current",
									selection.isHome
										? "group-data-[collapsible=icon]:bg-interactive-active"
										: "group-data-[collapsible=icon]:hover:bg-interactive-hover",
								)}
								onClick={selection.goHome}
								type="button"
							>
								<span className="contents group-data-[collapsible=icon]:grid group-data-[collapsible=icon]:h-[22px] group-data-[collapsible=icon]:w-[22px] group-data-[collapsible=icon]:place-items-center group-data-[collapsible=icon]:rounded-[6px] group-data-[collapsible=icon]:bg-accent group-data-[collapsible=icon]:text-accent-foreground">
									<Waypoints className="h-3.5 w-3.5" aria-hidden="true" />
								</span>
							</button>
						</TooltipTrigger>
						<TooltipContent side="right" hidden={state !== "collapsed"}>
							Orchestrator board
						</TooltipContent>
					</Tooltip>
					<span className="min-w-0 flex-1 truncate text-[14px] font-bold tracking-[-0.015em] text-foreground group-data-[collapsible=icon]:hidden">
						Agent Orchestrator
					</span>
					{/* On macOS the toggle lives in the titlebar cluster instead. */}
					{!isMac && (
						<Tooltip>
							<TooltipTrigger asChild>
								<SidebarTrigger className="shrink-0 rounded-md text-passive hover:bg-interactive-hover hover:text-foreground group-data-[collapsible=icon]:hidden [&_svg]:size-[15px]" />
							</TooltipTrigger>
							<TooltipContent>Collapse sidebar · ⌘B</TooltipContent>
						</Tooltip>
					)}
				</div>
			</SidebarHeader>

			<SidebarContent className="gap-0 px-[7px] group-data-[collapsible=icon]:items-center group-data-[collapsible=icon]:px-1.5">
				<SidebarGroup className="p-0">
					{/* Section label (project-sidebar__nav-label) */}
					<div className="flex shrink-0 items-center justify-between px-2 pb-2 group-data-[collapsible=icon]:hidden">
						<SidebarGroupLabel className="h-auto rounded-none p-0 text-[10.5px] font-semibold uppercase tracking-[0.09em] text-passive">
							Projects
						</SidebarGroupLabel>
						<CreateProjectButton onCreateProject={onCreateProject} />
					</div>

					{/* Tree (project-sidebar__tree) */}
					<SidebarGroupContent>
						{workspaceError ? (
							<div className="px-2 py-3 group-data-[collapsible=icon]:hidden">
								<p className="text-[12px] text-foreground">Could not load projects.</p>
								<p className="mt-1 text-[11px] text-passive">{workspaceError}</p>
							</div>
						) : workspaces.length === 0 ? (
							<div className="px-2 py-3 group-data-[collapsible=icon]:hidden">
								<p className="text-[12px] text-passive">No projects yet.</p>
								<p className="mt-1 text-[11px] text-passive">
									Click <span className="text-foreground">+</span> above to register a git repo.
								</p>
							</div>
						) : (
							<SidebarMenu className="gap-0 group-data-[collapsible=icon]:gap-1">
								{workspaces.map((workspace) => (
									<ProjectItem
										key={workspace.id}
										workspace={workspace}
										expanded={!collapsedIds.has(workspace.id)}
										selection={selection}
										onToggle={() => toggleCollapsed(workspace.id)}
										onRemoveProject={onRemoveProject}
									/>
								))}
								{isCollapsed && <CreateProjectListItem onCreateProject={onCreateProject} />}
							</SidebarMenu>
						)}
					</SidebarGroupContent>
				</SidebarGroup>
			</SidebarContent>

			{/* Footer (project-sidebar__footer) — single Settings menu. Divergence
          (user-requested 2026-06-10): the trigger stretches the full row width
          (flex-1) with a uniform 7px footer inset on all sides (reference uses
          12px top, 0 bottom, content-hugging button). The icon rail keeps the
          icon-only settings action plus expand toggle (off macOS). */}
			<SidebarFooter className="mt-auto gap-0 border-t border-border p-[7px] group-data-[collapsible=icon]:items-center group-data-[collapsible=icon]:px-1.5 group-data-[collapsible=icon]:pb-0 group-data-[collapsible=icon]:pt-2">
				<div className="relative flex w-full items-center group-data-[collapsible=icon]:hidden">
					<DropdownMenu>
						<DropdownMenuTrigger asChild>
							<button
								aria-label="Settings"
								className="flex flex-1 items-center justify-start gap-2.5 rounded-md p-2 text-[13px] font-medium text-passive transition-colors hover:bg-interactive-hover hover:text-foreground data-[state=open]:bg-interactive-hover data-[state=open]:text-foreground [&_svg]:size-[15px] [&_svg]:text-passive"
								type="button"
							>
								<Settings aria-hidden="true" />
								<span className="tracking-[-0.01em]">Settings</span>
							</button>
						</DropdownMenuTrigger>
						<DropdownMenuContent
							align="start"
							className="w-[var(--radix-dropdown-menu-trigger-width)] min-w-0"
							side="top"
						>
							<DropdownMenuItem onSelect={toggleTheme}>
								{theme === "dark" ? <Sun aria-hidden="true" /> : <Moon aria-hidden="true" />}
								{theme === "dark" ? "Light mode" : "Dark mode"}
							</DropdownMenuItem>
							<DropdownMenuSeparator />
							<DropdownMenuItem onSelect={selection.goPrs}>
								<GitPullRequest aria-hidden="true" />
								Pull requests
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
									"absolute right-1.5 top-1/2 h-1.5 w-1.5 -translate-y-1/2 rounded-full",
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
				<div className="hidden flex-col items-center gap-1 pb-3.5 group-data-[collapsible=icon]:flex">
					<DropdownMenu>
						<Tooltip>
							<TooltipTrigger asChild>
								<DropdownMenuTrigger asChild>
									<button
										aria-label="Settings"
										className="grid size-9 place-items-center rounded-lg text-passive transition-colors hover:bg-interactive-hover hover:text-foreground [&_svg]:size-4"
										type="button"
									>
										<Settings aria-hidden="true" />
									</button>
								</DropdownMenuTrigger>
							</TooltipTrigger>
							<TooltipContent side="right">Settings</TooltipContent>
						</Tooltip>
						<DropdownMenuContent align="start" className="min-w-0" side="top">
							<DropdownMenuItem onSelect={toggleTheme}>
								{theme === "dark" ? <Sun aria-hidden="true" /> : <Moon aria-hidden="true" />}
								{theme === "dark" ? "Light mode" : "Dark mode"}
							</DropdownMenuItem>
							<DropdownMenuSeparator />
							<DropdownMenuItem onSelect={selection.goPrs}>
								<GitPullRequest aria-hidden="true" />
								Pull requests
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
					{!isMac && (
						<Tooltip>
							<TooltipTrigger asChild>
								<SidebarTrigger className="size-9 rounded-lg text-passive hover:bg-interactive-hover hover:text-foreground [&_svg]:size-4" />
							</TooltipTrigger>
							<TooltipContent side="right">Expand sidebar · ⌘B</TooltipContent>
						</Tooltip>
					)}
				</div>
			</SidebarFooter>

			<div
				className="resize-handle resize-handle--right group-data-[collapsible=icon]:hidden"
				onPointerDown={onResizePointerDown}
				onDoubleClick={onResizeDoubleClick}
				style={noDragStyle}
			/>
		</SidebarRoot>
	);
}

type Selection = ReturnType<typeof useSelection>;

function ProjectItem({
	workspace,
	expanded,
	selection,
	onToggle,
	onRemoveProject,
}: {
	workspace: WorkspaceSummary;
	expanded: boolean;
	selection: Selection;
	onToggle: () => void;
	onRemoveProject: (projectId: string) => Promise<void>;
}) {
	const projectActive = selection.activeProjectId === workspace.id && !selection.activeSessionId;
	const [removeError, setRemoveError] = useState<string | null>(null);
	const [isRemoving, setIsRemoving] = useState(false);
	// Live workers only: merged/terminated sessions leave the sidebar and stay
	// reachable through the board's Done / Terminated bar (SessionsBoard).
	const sessions = workerSessions(workspace.sessions).filter(sessionIsActive);

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

	const removeProject = async () => {
		setRemoveError(null);
		const confirmed = window.confirm(
			`Remove project ${workspace.name}? This stops its live sessions and removes it from the sidebar, but keeps the repository folder and stored history on disk.`,
		);
		if (!confirmed) return;

		setIsRemoving(true);
		try {
			await onRemoveProject(workspace.id);
			// The route for a removed project no longer resolves; fall back home.
			if (selection.activeProjectId === workspace.id) selection.goHome();
		} catch (err) {
			const message = err instanceof Error ? err.message : "Could not remove project";
			setRemoveError(message);
			window.alert(message);
		} finally {
			setIsRemoving(false);
		}
	};

	return (
		<SidebarMenuItem className="mb-px group-data-[collapsible=icon]:mb-0">
			{/* project-sidebar__proj-row */}
			<SidebarMenuButton
				aria-current={projectActive ? "page" : undefined}
				aria-expanded={expanded}
				isActive={projectActive}
				onClick={onProjectClick}
				tooltip={workspace.name}
				className={cn(
					"h-auto gap-[9px] rounded-[5px] px-1.5 py-[7px] text-[13px] font-medium text-muted-foreground transition-[padding]",
					"hover:bg-interactive-hover hover:text-muted-foreground active:bg-interactive-hover active:text-muted-foreground",
					"data-[active=true]:bg-interactive-active data-[active=true]:font-semibold data-[active=true]:text-foreground",
					// Make room for the kebab action when the row is hovered, focused, or
					// its menu is open (the absolutely-positioned action replaces the count).
					"group-hover/menu-item:pr-[34px] group-focus-within/menu-item:pr-[34px] group-has-data-[state=open]/menu-item:pr-[34px]",
					// Icon rail: the old 36px letter tile.
					"group-data-[collapsible=icon]:size-9! group-data-[collapsible=icon]:justify-center group-data-[collapsible=icon]:rounded-lg group-data-[collapsible=icon]:p-0! group-data-[collapsible=icon]:font-semibold",
				)}
			>
				<ChevronRight
					className={cn(
						"h-[9px]! w-[9px]! shrink-0 text-passive transition-transform group-data-[collapsible=icon]:hidden",
						expanded && "rotate-90",
					)}
					strokeWidth={2.5}
					aria-hidden="true"
				/>
				<span className="hidden group-data-[collapsible=icon]:block">{workspace.name.charAt(0).toUpperCase()}</span>
				<span className="min-w-0 flex-1 truncate group-data-[collapsible=icon]:hidden">{workspace.name}</span>
				<span className="shrink-0 font-mono text-[11px] text-passive group-hover/menu-item:opacity-0 group-focus-within/menu-item:opacity-0 group-has-data-[state=open]/menu-item:opacity-0 group-data-[collapsible=icon]:hidden">
					{sessions.length}
				</span>
			</SidebarMenuButton>
			{/* Per-project actions: a kebab that reveals on row hover (replacing the
          session count) — surfaces the daemon/CLI removal capability in the UI. */}
			<DropdownMenu>
				<DropdownMenuTrigger asChild>
					<SidebarMenuAction showOnHover aria-label={`Project actions for ${workspace.name}`}>
						<MoreHorizontal aria-hidden="true" />
					</SidebarMenuAction>
				</DropdownMenuTrigger>
				<DropdownMenuContent side="right" align="start" className="min-w-44">
					<DropdownMenuItem onSelect={() => selection.goSettings(workspace.id)}>
						<Settings aria-hidden="true" />
						Project settings
					</DropdownMenuItem>
					<DropdownMenuSeparator />
					<DropdownMenuItem
						className="text-destructive focus:text-destructive [&_svg]:text-destructive"
						disabled={isRemoving}
						onSelect={() => void removeProject()}
					>
						<Trash2 aria-hidden="true" />
						Remove project
					</DropdownMenuItem>
				</DropdownMenuContent>
			</DropdownMenu>
			{removeError && (
				<span className="sr-only" role="status">
					{removeError}
				</span>
			)}
			{/* project-sidebar__sessions. Divergence from AO (user decision
          2026-06-12): no left indent or tree guide line — every sidebar row
          (project or worker) spans the same full width. */}
			{expanded && sessions.length > 0 && (
				<SidebarMenuSub className="mx-0 translate-x-0 gap-0 border-0 px-0 pb-2 pt-0.5">
					{sessions.map((session) => {
						const active = selection.activeSessionId === session.id;
						return (
							<SidebarMenuSubItem key={session.id}>
								<SidebarMenuSubButton asChild isActive={active}>
									<button
										aria-current={active ? "page" : undefined}
										aria-label={`Open ${session.title}`}
										className={cn(
											"h-auto w-full translate-x-0 gap-[9px] rounded-[5px] py-[5px] pl-2 pr-1.5 text-left transition-colors",
											"hover:bg-interactive-hover data-[active=true]:bg-interactive-active",
										)}
										onClick={() => selection.goSession(workspace.id, session.id)}
										type="button"
									>
										<SessionDot session={session} />
										<span className="min-w-0 flex-1">
											<span
												className={cn(
													"block truncate text-[12px]",
													active ? "text-foreground" : "text-muted-foreground",
												)}
											>
												{session.title}
											</span>
											<span className="block truncate font-mono text-[10px] text-passive">{session.id}</span>
										</span>
									</button>
								</SidebarMenuSubButton>
							</SidebarMenuSubItem>
						);
					})}
				</SidebarMenuSub>
			)}
		</SidebarMenuItem>
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
						className="grid h-[18px] w-[18px] place-items-center rounded-[4px] text-passive transition-colors hover:bg-interactive-hover hover:text-muted-foreground"
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

function CreateProjectListItem({ onCreateProject }: Pick<SidebarProps, "onCreateProject">) {
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
		<SidebarMenuItem className="mb-px group-data-[collapsible=icon]:mb-0">
			<Tooltip>
				<TooltipTrigger asChild>
					<button
						aria-label="New project"
						className="grid h-9 w-full place-items-center rounded-[5px] text-passive transition-colors hover:bg-interactive-hover hover:text-muted-foreground"
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
		</SidebarMenuItem>
	);
}
