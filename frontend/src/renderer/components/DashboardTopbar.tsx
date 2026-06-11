import { useQueryClient } from "@tanstack/react-query";
import { useNavigate } from "@tanstack/react-router";
import { Waypoints } from "lucide-react";
import { useState } from "react";
import { findProjectOrchestrator } from "../types/workspace";
import { useWorkspaceQuery, workspaceQueryKey } from "../hooks/useWorkspaceQuery";
import { spawnOrchestrator } from "../lib/spawn-orchestrator";
import { useUiStore } from "../stores/ui-store";
import { cn } from "../lib/utils";

const isMac = typeof navigator !== "undefined" && /Mac|iPod|iPhone|iPad/.test(navigator.userAgent);
const dragStyle = isMac ? ({ WebkitAppRegion: "drag" } as React.CSSProperties) : undefined;
const noDragStyle = isMac ? ({ WebkitAppRegion: "no-drag" } as React.CSSProperties) : undefined;

type DashboardTab = "coding" | "reviews";

type DashboardTopbarProps = {
	/** Which top-nav tab reads as active (omit on the PR board, which is neither). */
	activeTab?: DashboardTab;
	/** When set, the project crumb scopes to one project. */
	projectId?: string;
	projectLabel?: string;
};

// The dashboard header (mc-board .dashboard-app-header): project crumb · Coding/
// Reviews tabs | bell · Orchestrator (board ↔ terminal).
// Shared verbatim across the board, review, and PR screens so navigating between
// them keeps one stable top strip (agent-orchestrator surfaces them as tabs).
export function DashboardTopbar({ activeTab, projectId, projectLabel = "agent-orchestrator" }: DashboardTopbarProps) {
	const navigate = useNavigate();
	const queryClient = useQueryClient();
	const [isSpawning, setIsSpawning] = useState(false);
	const isSidebarOpen = useUiStore((state) => state.isSidebarOpen);
	const all = useWorkspaceQuery().data ?? [];
	const orchestrator = projectId ? findProjectOrchestrator(all, projectId) : undefined;

	const openOrchestrator = async () => {
		if (!projectId) return;
		if (orchestrator) {
			void navigate({
				to: "/projects/$projectId/sessions/$sessionId",
				params: { projectId, sessionId: orchestrator.id },
			});
			return;
		}
		setIsSpawning(true);
		try {
			const sessionId = await spawnOrchestrator(projectId);
			await queryClient.invalidateQueries({ queryKey: workspaceQueryKey });
			void navigate({
				to: "/projects/$projectId/sessions/$sessionId",
				params: { projectId, sessionId },
			});
		} catch (error) {
			console.error("Failed to spawn orchestrator:", error);
		} finally {
			setIsSpawning(false);
		}
	};

	return (
		<header
			className={cn("dashboard-app-header", isMac && !isSidebarOpen && "is-under-titlebar-nav")}
			style={dragStyle}
		>
			<div className="session-topbar__lead">
				<div className="topbar-project-line">
					<span className="dashboard-app-header__project">{projectLabel}</span>
					<nav aria-label="Workspace mode" className="dashboard-app-header__tabs">
						<button
							className={cn("dashboard-app-header__tab", activeTab === "coding" && "is-active")}
							onClick={() =>
								void navigate(projectId ? { to: "/projects/$projectId", params: { projectId } } : { to: "/" })
							}
							style={noDragStyle}
							type="button"
						>
							Coding
						</button>
						<button
							className={cn("dashboard-app-header__tab", activeTab === "reviews" && "is-active")}
							onClick={() => void navigate({ to: "/review" })}
							style={noDragStyle}
							type="button"
						>
							Reviews
						</button>
					</nav>
				</div>
			</div>
			<div className="dashboard-app-header__spacer" />
			<div className="dashboard-app-header__actions">
				{projectId ? (
					orchestrator ? (
						<button
							aria-label="Orchestrator"
							className="dashboard-app-header__primary-btn"
							onClick={() =>
								void navigate({
									to: "/projects/$projectId/sessions/$sessionId",
									params: { projectId, sessionId: orchestrator.id },
								})
							}
							style={noDragStyle}
							type="button"
						>
							<Waypoints className="h-3.5 w-3.5" aria-hidden="true" />
							Orchestrator
						</button>
					) : (
						<button
							aria-label="Spawn Orchestrator"
							className="dashboard-app-header__primary-btn"
							disabled={isSpawning}
							onClick={() => void openOrchestrator()}
							style={noDragStyle}
							type="button"
						>
							<Waypoints className="h-3.5 w-3.5" aria-hidden="true" />
							{isSpawning ? "Spawning…" : "Spawn Orchestrator"}
						</button>
					)
				) : null}
			</div>
		</header>
	);
}

// The board subhead (mc-board .dashboard-main__subhead): a 21px bold title with
// a muted one-line subtitle, optionally a trailing count.
export function DashboardSubhead({ title, subtitle, count }: { title: string; subtitle: string; count?: number }) {
	return (
		<div className="flex items-baseline gap-3 px-[18px] pt-[22px]">
			<h1 className="text-[21px] font-bold tracking-[-0.025em] text-foreground">{title}</h1>
			{typeof count === "number" && <span className="font-mono text-[13px] text-passive">{count}</span>}
			<span className="text-[12.5px] text-passive">{subtitle}</span>
		</div>
	);
}
