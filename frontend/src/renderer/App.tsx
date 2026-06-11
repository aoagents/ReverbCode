import { useQueryClient } from "@tanstack/react-query";
import { useEffect, useState } from "react";
import { CenterPane } from "./components/CenterPane";
import { SideRail } from "./components/SideRail";
import { Sidebar } from "./components/Sidebar";
import { SpawnWorkerModal } from "./components/SpawnWorkerModal";
import { Topbar } from "./components/Topbar";
import { useDaemonStatus } from "./hooks/useDaemonStatus";
import { useWorkspaceQuery, workspaceQueryKey } from "./hooks/useWorkspaceQuery";
import { apiClient } from "./lib/api-client";
import { Theme, useUiStore } from "./stores/ui-store";
import { toAgentProvider, toSessionStatus, type AgentProvider, type WorkspaceSummary } from "./types/workspace";

type AppProps = {
	routeSessionId?: string;
	routeWorkspaceId?: string;
};

function systemTheme(): Theme {
	return window.matchMedia("(prefers-color-scheme: light)").matches ? "light" : "dark";
}

function errorMessage(error: unknown) {
	return error instanceof Error ? error.message : "Could not load projects";
}

export function App({ routeSessionId, routeWorkspaceId }: AppProps) {
	const queryClient = useQueryClient();
	const {
		view,
		workbenchTab,
		selectedSessionId,
		selectedWorkspaceId,
		theme,
		setSystemTheme,
		setWorkbenchTab,
		toggleSidebar,
		selectWorkspace,
		selectSession,
	} = useUiStore();
	const workspaceQuery = useWorkspaceQuery();
	const workspaces = workspaceQuery.data ?? [];
	const daemonStatus = useDaemonStatus(queryClient);
	const [spawnOpen, setSpawnOpen] = useState(false);
	const [spawnProjectId, setSpawnProjectId] = useState<string | undefined>(undefined);

	const openSpawn = (projectId?: string) => {
		setSpawnProjectId(projectId);
		setSpawnOpen(true);
	};

	const selectedWorkspace =
		workspaces.find((workspace) => workspace.id === selectedWorkspaceId) ?? workspaces[0] ?? null;
	const selectedSession =
		view === "session"
			? workspaces.flatMap((workspace) => workspace.sessions).find((session) => session.id === selectedSessionId)
			: undefined;
	const sessionWorkspace = selectedSession
		? (workspaces.find((workspace) => workspace.id === selectedSession.workspaceId) ?? selectedWorkspace)
		: selectedWorkspace;

	useEffect(() => {
		if (routeWorkspaceId) selectWorkspace(routeWorkspaceId);
		if (routeSessionId) selectSession(routeSessionId, routeWorkspaceId);
	}, [routeSessionId, routeWorkspaceId, selectWorkspace, selectSession]);

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
			}
		};

		window.addEventListener("keydown", handleKeyDown);
		return () => window.removeEventListener("keydown", handleKeyDown);
	}, [selectWorkspace, toggleSidebar, workspaces]);

	const updateWorkspaces = (updater: (workspaces: WorkspaceSummary[]) => WorkspaceSummary[]) => {
		queryClient.setQueryData<WorkspaceSummary[]>(workspaceQueryKey, (current = workspaces) => updater(current));
	};

	const createProject = async (input: { path: string }) => {
		const { data, error } = await apiClient.POST("/api/v1/projects", { body: { path: input.path } });

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
			throw new Error(error instanceof Error ? error.message : error ? String(error) : "No session returned");
		}

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
		selectSession(session.id, input.projectId);
	};

	const showSideRail = !(view === "session" && workbenchTab === "terminal");

	return (
		<>
			<div className="flex h-screen flex-col bg-background text-foreground">
				<Topbar
					onNewWorker={() => openSpawn()}
					onSetWorkbenchTab={setWorkbenchTab}
					onToggleSidebar={toggleSidebar}
					session={selectedSession}
					view={view}
					workbenchTab={workbenchTab}
					workspace={sessionWorkspace}
				/>
				<div className="flex min-h-0 flex-1">
					<Sidebar
						daemonStatus={daemonStatus}
						onCreateProject={createProject}
						onNewWorker={openSpawn}
						workspaceError={workspaceQuery.isError ? errorMessage(workspaceQuery.error) : undefined}
						workspaces={workspaces}
					/>
					<CenterPane
						daemonReady={daemonStatus.state === "ready"}
						session={selectedSession}
						theme={theme}
						view={view}
					/>
					{showSideRail && (
						<SideRail onSelectSession={selectSession} session={selectedSession} view={view} workspaces={workspaces} />
					)}
				</div>
			</div>
			<SpawnWorkerModal
				defaultProjectId={spawnProjectId ?? selectedWorkspace?.id}
				onCreateTask={createTask}
				onOpenChange={setSpawnOpen}
				open={spawnOpen}
				workspaces={workspaces}
			/>
		</>
	);
}
