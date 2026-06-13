import { useQueryClient } from "@tanstack/react-query";
import { useNavigate, useParams } from "@tanstack/react-router";
import { GitBranch, LayoutGrid, PanelRightClose, PanelRightOpen, Waypoints } from "lucide-react";
import { useState } from "react";
import {
	findProjectOrchestrator,
	isOrchestratorSession,
	workerDisplayStatus,
	type WorkerDisplayStatus,
	type WorkspaceSession,
} from "../types/workspace";
import { useWorkspaceQuery, workspaceQueryKey } from "../hooks/useWorkspaceQuery";
import { spawnOrchestrator } from "../lib/spawn-orchestrator";
import { useUiStore } from "../stores/ui-store";
import { cn } from "../lib/utils";

const isMac = typeof navigator !== "undefined" && /Mac|iPod|iPhone|iPad/.test(navigator.userAgent);
const dragStyle = isMac ? ({ WebkitAppRegion: "drag" } as React.CSSProperties) : undefined;
const noDragStyle = isMac ? ({ WebkitAppRegion: "no-drag" } as React.CSSProperties) : undefined;

// Session status → pill tone, mirroring agent-orchestrator's StatusBadge
// (working=orange & breathing, input=amber, fail=red, ready=green, done=neutral).
// Tones are theme vars so the pill tracks the light/dark status palettes.
const STATUS_PILL: Record<WorkerDisplayStatus, { label: string; tone: string; breathe: boolean }> = {
	working: { label: "Working", tone: "var(--orange)", breathe: true },
	needs_you: { label: "Needs input", tone: "var(--amber)", breathe: false },
	ci_failed: { label: "CI failed", tone: "var(--red)", breathe: false },
	mergeable: { label: "Ready", tone: "var(--green)", breathe: false },
	done: { label: "Done", tone: "var(--fg-muted)", breathe: false },
};

// The one app topbar (.dashboard-app-header), rendered by the shell layout
// across the full window width — above both the sidebar and the route outlet —
// so the crumb and actions sit at identical offsets on every screen and the
// macOS traffic lights + TitlebarNav cluster live in its left inset
// (.is-under-titlebar-nav pads past them). The
// variant is derived from the route, not props: a sessionId in the URL swaps
// the lead to the session identity (orchestrator crumb + mode badge, or worker
// branch + status pill) and the actions to Kanban/inspector controls;
// otherwise it's the dashboard crumb plus the Orchestrator launcher when a
// project is in scope. Merges the old DashboardTopbar/Topbar pair —
// agent-orchestrator keeps those as two components aligned only by CSS.
export function ShellTopbar() {
	const navigate = useNavigate();
	const queryClient = useQueryClient();
	const params = useParams({ strict: false }) as { projectId?: string; sessionId?: string };
	const isInspectorOpen = useUiStore((state) => state.isInspectorOpen);
	const toggleInspector = useUiStore((state) => state.toggleInspector);
	const [isSpawning, setIsSpawning] = useState(false);
	const all = useWorkspaceQuery().data ?? [];

	const session = params.sessionId
		? all.flatMap((workspace) => workspace.sessions).find((s) => s.id === params.sessionId)
		: undefined;
	const isSessionRoute = Boolean(params.sessionId);
	const isOrchestrator = session ? isOrchestratorSession(session) : false;
	// Project in scope: the session's workspace wins over the route param so the
	// cross-project /sessions/$sessionId route still resolves a crumb. A
	// projectId that no longer resolves (stale route after the project was
	// removed, or data still loading) shows an empty crumb — never the raw
	// route slug. "agent-orchestrator" is the root-board crumb only.
	const projectId = session?.workspaceId ?? params.projectId;
	const project = projectId ? all.find((workspace) => workspace.id === projectId) : undefined;
	const projectLabel = project?.name ?? session?.workspaceName ?? (projectId ? "" : "agent-orchestrator");
	const orchestrator = projectId ? findProjectOrchestrator(all, projectId) : undefined;

	const openBoard = () =>
		projectId ? void navigate({ to: "/projects/$projectId", params: { projectId } }) : void navigate({ to: "/" });

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
		<header className={cn("dashboard-app-header", isMac && "is-under-titlebar-nav")} style={dragStyle}>
			<div className="session-topbar__lead">
				{isSessionRoute && isOrchestrator ? (
					<div className="topbar-project-pills-group">
						<div className="topbar-project-line">
							<span className="dashboard-app-header__project">{projectLabel}</span>
							<span aria-hidden="true" className="topbar-identity-sep">
								·
							</span>
							<span className="session-detail-mode-badge session-detail-mode-badge--neutral">
								<Waypoints className="size-3 shrink-0" aria-hidden="true" />
								Orchestrator
							</span>
						</div>
					</div>
				) : isSessionRoute ? (
					<div className="session-topbar__identity">
						<div className="session-topbar__branch">
							<GitBranch className="h-3 w-3 shrink-0" aria-hidden="true" />
							<span className="truncate">{session?.branch || `session/${session?.id ?? ""}`}</span>
						</div>
						{session ? <SessionStatusPill session={session} /> : null}
					</div>
				) : (
					<div className="topbar-project-line">
						<span className="dashboard-app-header__project">{projectLabel}</span>
					</div>
				)}
			</div>

			<div className="dashboard-app-header__spacer" />

			<div className="dashboard-app-header__actions">
				{isSessionRoute ? (
					<>
						<button
							aria-label={isOrchestrator ? "Open Kanban" : "Back to board"}
							className="dashboard-app-header__primary-btn"
							onClick={openBoard}
							style={noDragStyle}
							type="button"
						>
							<LayoutGrid className="h-3.5 w-3.5" aria-hidden="true" />
							{isOrchestrator ? "Open Kanban" : "Kanban"}
						</button>
						{/* Inspector collapse (worker sessions only — orchestrators have no rail). */}
						{!isOrchestrator && (
							<button
								aria-label={isInspectorOpen ? "Close inspector panel" : "Open inspector panel"}
								aria-pressed={isInspectorOpen}
								className="dashboard-app-header__icon-btn"
								onClick={toggleInspector}
								style={noDragStyle}
								title={`${isInspectorOpen ? "Close" : "Open"} inspector · ⌘⇧B`}
								type="button"
							>
								{isInspectorOpen ? (
									<PanelRightClose className="h-[15px] w-[15px]" aria-hidden="true" />
								) : (
									<PanelRightOpen className="h-[15px] w-[15px]" aria-hidden="true" />
								)}
							</button>
						)}
					</>
				) : projectId ? (
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

// StatusBadge --pill: tinted bordered pill (inset 25%-tone hairline + 7%-tone
// fill) with a 6px dot that breathes while the agent is working.
function SessionStatusPill({ session }: { session: WorkspaceSession }) {
	const { label, tone, breathe } = STATUS_PILL[workerDisplayStatus(session)];
	return (
		<span
			className="inline-flex shrink-0 items-center gap-[7px] whitespace-nowrap rounded-[7px] px-[11px] py-[5px] text-[11.5px] font-semibold leading-none"
			style={{
				color: tone,
				background: `color-mix(in srgb, ${tone} 7%, transparent)`,
				boxShadow: `inset 0 0 0 1px color-mix(in srgb, ${tone} 25%, transparent)`,
			}}
		>
			<span
				className={cn("h-1.5 w-1.5 rounded-full", breathe && "animate-status-pulse")}
				style={{ background: tone }}
			/>
			{label}
		</span>
	);
}
