import { createFileRoute, Outlet, useNavigate } from "@tanstack/react-router";
import { useQueryClient } from "@tanstack/react-query";
import { useCallback, useEffect, useState } from "react";
import { Sidebar } from "../components/Sidebar";
import { SidebarProvider } from "../components/ui/sidebar";
import { SpawnWorkerModal } from "../components/SpawnWorkerModal";
import { TitlebarNav } from "../components/TitlebarNav";
import { useDaemonStatus } from "../hooks/useDaemonStatus";
import { useWorkspaceQuery, workspaceQueryKey, workspaceQueryOptions } from "../hooks/useWorkspaceQuery";
import { apiClient, apiErrorMessage } from "../lib/api-client";
import { ShellProvider } from "../lib/shell-context";
import { readStoredTheme, type Theme, useUiStore } from "../stores/ui-store";
import { toAgentProvider, toSessionStatus, type AgentProvider, type WorkspaceSummary } from "../types/workspace";

export const Route = createFileRoute("/_shell")({
	// Prefetch the workspace list for the whole shell (parent loaders run before
	// children); pairs with the router's defaultPreload: "intent" so a hovered
	// nav target is warm before the click.
	loader: ({ context }) => context.queryClient.ensureQueryData(workspaceQueryOptions),
	component: ShellLayout,
});

function systemTheme(): Theme {
	return window.matchMedia("(prefers-color-scheme: light)").matches ? "light" : "dark";
}

function errorMessage(error: unknown) {
	return error instanceof Error ? error.message : "Could not load projects";
}

// Persistent app shell: the Sidebar + shared state survive route changes; only
// the <Outlet> content (board / session / settings / …) swaps. Lifted out of
// the old single <App>, with selection now owned by the router (route params)
// instead of Zustand. The daemon-status effect runs here exactly once.
function ShellLayout() {
	const navigate = useNavigate();
	const queryClient = useQueryClient();
	const workspaceQuery = useWorkspaceQuery();
	const workspaces = workspaceQuery.data ?? [];
	const daemonStatus = useDaemonStatus(queryClient);
	const { theme, setTheme, isSidebarOpen, toggleSidebar } = useUiStore();
	const [spawnOpen, setSpawnOpen] = useState(false);
	const [spawnProjectId, setSpawnProjectId] = useState<string | undefined>(undefined);

	const openSpawn = useCallback((projectId?: string) => {
		setSpawnProjectId(projectId);
		setSpawnOpen(true);
	}, []);

	const updateWorkspaces = useCallback(
		(updater: (workspaces: WorkspaceSummary[]) => WorkspaceSummary[]) => {
			queryClient.setQueryData<WorkspaceSummary[]>(workspaceQueryKey, (current = []) => updater(current));
		},
		[queryClient],
	);

	const createProject = useCallback(
		async (input: { path: string }) => {
			const { data, error } = await apiClient.POST("/api/v1/projects", { body: { path: input.path } });
			if (error) throw new Error(apiErrorMessage(error));
			if (!data?.project) throw new Error("Project creation returned no project");

			const workspace: WorkspaceSummary = {
				id: data.project.id,
				name: data.project.name,
				path: data.project.path,
				type: "main",
				sessions: [],
			};
			updateWorkspaces((current) => [workspace, ...current.filter((item) => item.id !== workspace.id)]);
			void navigate({ to: "/projects/$projectId", params: { projectId: workspace.id } });
		},
		[navigate, updateWorkspaces],
	);

	const createTask = useCallback(
		async (input: { projectId: string; prompt: string; branch?: string; harness?: AgentProvider }) => {
			const { data, error } = await apiClient.POST("/api/v1/sessions", {
				body: {
					projectId: input.projectId,
					kind: "worker",
					harness: input.harness,
					prompt: input.prompt,
					branch: input.branch || undefined,
				},
			});
			if (error || !data?.session) throw new Error(error ? apiErrorMessage(error) : "No session returned");

			const session = data.session;
			updateWorkspaces((current) =>
				current.map((item) =>
					item.id === input.projectId
						? {
								...item,
								sessions: [
									{
										id: session.id,
										terminalHandleId: session.terminalHandleId,
										workspaceId: item.id,
										workspaceName: item.name,
										title: input.prompt,
										provider: toAgentProvider(session.harness),
										branch: input.branch ?? "",
										status: toSessionStatus(session.status, session.isTerminated),
										updatedAt: "now",
									},
									...item.sessions.filter((existing) => existing.id !== session.id),
								],
							}
						: item,
				),
			);
			void navigate({
				to: "/projects/$projectId/sessions/$sessionId",
				params: { projectId: input.projectId, sessionId: session.id },
			});
		},
		[navigate, updateWorkspaces],
	);

	useEffect(() => {
		document.documentElement.dataset.theme = theme;
		document.documentElement.style.colorScheme = theme;
	}, [theme]);

	// Follow OS appearance only until the user picks a theme explicitly.
	useEffect(() => {
		if (readStoredTheme()) return;

		const mediaQuery = window.matchMedia("(prefers-color-scheme: light)");
		const handleChange = () => setTheme(systemTheme());
		mediaQuery.addEventListener("change", handleChange);
		return () => mediaQuery.removeEventListener("change", handleChange);
	}, [setTheme]);

	// ⌘B lives in SidebarProvider (shadcn's built-in shortcut), which routes
	// through onOpenChange back into the ui-store.
	useEffect(() => {
		const handleKeyDown = (event: KeyboardEvent) => {
			if ((event.metaKey || event.ctrlKey) && /^[1-9]$/.test(event.key)) {
				const workspace = workspaces[Number(event.key) - 1];
				if (workspace) {
					event.preventDefault();
					void navigate({ to: "/projects/$projectId", params: { projectId: workspace.id } });
				}
			}
		};
		window.addEventListener("keydown", handleKeyDown);
		return () => window.removeEventListener("keydown", handleKeyDown);
	}, [navigate, workspaces]);

	return (
		<ShellProvider value={{ daemonStatus, openSpawn, createProject, createTask }}>
			{/* Controlled by the ui-store so TitlebarNav / Topbar toggles (which call
          the store directly) stay in sync. --sidebar-width chains to the
          drag-resizable --ao-sidebar-w set on :root by useResizable. */}
			<SidebarProvider
				className="h-screen min-h-0 bg-background text-foreground"
				onOpenChange={(open) => open !== isSidebarOpen && toggleSidebar()}
				open={isSidebarOpen}
				style={
					{ "--sidebar-width": "var(--ao-sidebar-w, 240px)", "--sidebar-width-icon": "48px" } as React.CSSProperties
				}
			>
				<Sidebar
					daemonStatus={daemonStatus}
					onCreateProject={createProject}
					onNewWorker={openSpawn}
					workspaceError={workspaceQuery.isError ? errorMessage(workspaceQuery.error) : undefined}
					workspaces={workspaces}
				/>
				<main className="flex min-w-0 flex-1 flex-col">
					<Outlet />
				</main>
				{/* Fixed macOS titlebar cluster beside the traffic lights — rendered
            once here so the toggle/history buttons never move when the
            sidebar collapses or expands. MUST come after the sidebar/topbars
            in the DOM: Electron builds the window-drag region in document
            order (drag rects add, no-drag rects subtract), so the cluster's
            no-drag holes only survive if they're processed after the drag
            strips they overlap. Rendered first, real clicks get swallowed by
            window-drag even though DOM hit-testing looks correct. */}
				<TitlebarNav />
			</SidebarProvider>
			<SpawnWorkerModal
				defaultProjectId={spawnProjectId}
				onCreateTask={createTask}
				onOpenChange={setSpawnOpen}
				open={spawnOpen}
				workspaces={workspaces}
			/>
		</ShellProvider>
	);
}
