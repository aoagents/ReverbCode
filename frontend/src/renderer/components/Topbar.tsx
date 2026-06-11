import { GitBranch, LayoutGrid, PanelRightClose, PanelRightOpen, Waypoints } from "lucide-react";
import { type WorkbenchView, useUiStore } from "../stores/ui-store";
import type { WorkerDisplayStatus, WorkspaceSession } from "../types/workspace";
import { workerDisplayStatus } from "../types/workspace";
import { cn } from "../lib/utils";

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

const isMac = typeof navigator !== "undefined" && /Mac|iPod|iPhone|iPad/.test(navigator.userAgent);
const dragStyle = isMac ? ({ WebkitAppRegion: "drag" } as React.CSSProperties) : undefined;
const noDragStyle = isMac ? ({ WebkitAppRegion: "no-drag" } as React.CSSProperties) : undefined;

type TopbarProps = {
	view: WorkbenchView;
	session?: WorkspaceSession;
	/** Project crumb for orchestrator sessions (matches AO topbar-project-line). */
	projectLabel?: string;
	/** Back-to-board navigation for the Kanban / Open Kanban button. */
	onOpenBoard?: () => void;
};

export function Topbar({ view, session, projectLabel, onOpenBoard }: TopbarProps) {
	const isSidebarOpen = useUiStore((state) => state.isSidebarOpen);
	const isInspectorOpen = useUiStore((state) => state.isInspectorOpen);
	const toggleInspector = useUiStore((state) => state.toggleInspector);

	return (
		<header
			className={cn("dashboard-app-header session-topbar", isMac && !isSidebarOpen && "is-under-titlebar-nav")}
			style={dragStyle}
		>
			<div className="session-topbar__lead">
				{view === "orchestrator" ? (
					<div className="topbar-project-pills-group">
						<div className="topbar-project-line">
							<span className="dashboard-app-header__project">
								{projectLabel ?? session?.workspaceName ?? "Project"}
							</span>
							<span aria-hidden="true" className="topbar-identity-sep">
								·
							</span>
							<span className="session-detail-mode-badge session-detail-mode-badge--neutral">
								<Waypoints className="size-3 shrink-0" aria-hidden="true" />
								Orchestrator
							</span>
						</div>
					</div>
				) : (
					<div className="session-topbar__identity">
						<div className="session-topbar__branch">
							<GitBranch className="h-3 w-3 shrink-0" aria-hidden="true" />
							<span className="truncate">{session?.branch || `session/${session?.id ?? ""}`}</span>
						</div>
						{session ? <SessionStatusPill session={session} /> : null}
					</div>
				)}
			</div>

			<div className="dashboard-app-header__spacer" />

			<div className="dashboard-app-header__actions">
				<button
					aria-label={view === "orchestrator" ? "Open Kanban" : "Back to board"}
					className="dashboard-app-header__primary-btn"
					onClick={onOpenBoard}
					style={noDragStyle}
					type="button"
				>
					<LayoutGrid className="h-3.5 w-3.5" aria-hidden="true" />
					{view === "orchestrator" ? "Open Kanban" : "Kanban"}
				</button>
				{/* Inspector collapse (worker sessions only — orchestrators have no rail). */}
				{view === "session" && (
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
