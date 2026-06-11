import { useEffect, useRef, useState } from "react";
import { useNavigate } from "@tanstack/react-router";
import type { PanelImperativeHandle, PanelSize } from "react-resizable-panels";
import { CenterPane } from "./CenterPane";
import { SessionInspector } from "./SessionInspector";
import { Topbar } from "./Topbar";
import { ResizableHandle, ResizablePanel, ResizablePanelGroup } from "./ui/resizable";
import { useUiStore } from "../stores/ui-store";
import { useShell } from "../lib/shell-context";
import { useWorkspaceQuery } from "../hooks/useWorkspaceQuery";
import { isOrchestratorSession } from "../types/workspace";

const INSPECTOR_MIN_PERCENT = 22;
const INSPECTOR_MAX_PERCENT = 45;
const inspectorSplitStorageKey = "ao.inspector.split";

function initialSplitPercent(): number {
	const raw = typeof window === "undefined" ? null : window.localStorage?.getItem(inspectorSplitStorageKey);
	const parsed = raw === null ? Number.NaN : Number(raw);
	if (!Number.isFinite(parsed)) return 28;
	return Math.min(INSPECTOR_MAX_PERCENT, Math.max(INSPECTOR_MIN_PERCENT, parsed));
}

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
//
// The split is shadcn's resizable (react-resizable-panels v4) with a fully
// collapsible inspector: the panel is `collapsible` and driven to 0% via the
// imperative API from the ui-store (Topbar button / ⌘⇧B), animated by the
// flex-grow transition in styles.css. Content keeps a stable min-width inside
// the clipped panel so nothing reflows mid-animation; split width persists.
export function SessionView({ sessionId, projectId }: SessionViewProps) {
	const navigate = useNavigate();
	const workspaceQuery = useWorkspaceQuery();
	const workspaces = workspaceQuery.data ?? [];
	const { theme } = useUiStore();
	const isInspectorOpen = useUiStore((state) => state.isInspectorOpen);
	const toggleInspector = useUiStore((state) => state.toggleInspector);
	const { daemonStatus } = useShell();
	const inspectorRef = useRef<PanelImperativeHandle | null>(null);

	const session = workspaces.flatMap((workspace) => workspace.sessions).find((s) => s.id === sessionId);
	const isOrchestrator = session ? isOrchestratorSession(session) : false;
	const workspace =
		(session && workspaces.find((w) => w.id === session.workspaceId)) ??
		(projectId ? workspaces.find((w) => w.id === projectId) : undefined);

	// Orchestrator sessions are terminal-only; only worker sessions have the rail.
	const hasInspector = !isOrchestrator;
	// Frozen at mount: rrp re-registers the panel (a layout effect keyed on
	// defaultSize, among others) whenever this prop's identity changes, and the
	// imperative collapse()/expand() below can race that re-registration within
	// the same commit — rrp then throws "Panel constraints not found for Panel
	// inspector", which unwinds the whole route to the router's CatchBoundary
	// (the toggle button looks dead and the session view is torn down).
	// defaultSize only matters at first mount; afterwards the imperative API
	// owns the size, so it must never track live open/closed state.
	const [inspectorDefaultSize] = useState(() => (isInspectorOpen ? `${initialSplitPercent()}%` : "0%"));

	useEffect(() => {
		if (!hasInspector) return;
		const handleKeyDown = (event: KeyboardEvent) => {
			if (event.key.toLowerCase() !== "b" || !event.shiftKey) return;
			if (!event.metaKey && !event.ctrlKey) return;
			event.preventDefault();
			toggleInspector();
		};
		window.addEventListener("keydown", handleKeyDown);
		return () => window.removeEventListener("keydown", handleKeyDown);
	}, [hasInspector, toggleInspector]);

	// Drive the collapsible panel from the store so the Topbar button, ⌘⇧B, and
	// drag-to-collapse all stay in sync.
	useEffect(() => {
		const panel = inspectorRef.current;
		if (!panel) return;
		if (isInspectorOpen) {
			panel.expand();
			// expand() restores the "most recent" size, which is 0 when the panel
			// mounted collapsed — fall back to the persisted split.
			if (panel.getSize().asPercentage === 0) panel.resize(`${initialSplitPercent()}%`);
		} else {
			panel.collapse();
		}
	}, [hasInspector, isInspectorOpen]);

	// Persist drags and mirror collapse state (dragging past minSize collapses)
	// back into the store. Read the store imperatively to avoid a stale closure.
	const handleInspectorResize = (size: PanelSize) => {
		const open = useUiStore.getState().isInspectorOpen;
		if (size.asPercentage > 0) {
			window.localStorage?.setItem(inspectorSplitStorageKey, String(size.asPercentage));
			if (!open) toggleInspector();
		} else if (open) {
			toggleInspector();
		}
	};

	if (!session && !workspaceQuery.isLoading) {
		return (
			<div className="grid h-full place-items-center bg-background p-6 text-center font-mono text-[12px] text-passive">
				Session not found. It may have been cleaned up — pick another from the sidebar.
			</div>
		);
	}

	return (
		<div className="flex h-full min-h-0 flex-col bg-background text-foreground">
			<Topbar
				onOpenBoard={() =>
					workspace
						? void navigate({ to: "/projects/$projectId", params: { projectId: workspace.id } })
						: void navigate({ to: "/" })
				}
				projectLabel={workspace?.name}
				session={session}
				view={isOrchestrator ? "orchestrator" : "session"}
			/>
			<ResizablePanelGroup className="session-split min-h-0 flex-1" id="session-workspace" orientation="horizontal">
				{/* react-resizable-panels v4: bare numbers are PIXELS; percentages must
            be strings. Numeric sizes here once clamped the inspector to 45px. */}
				<ResizablePanel defaultSize="72%" id="terminal" minSize="45%">
					<CenterPane daemonReady={daemonStatus.state === "ready"} session={session} theme={theme} />
				</ResizablePanel>
				{hasInspector ? (
					<>
						<ResizableHandle className="session-inspector__resize-handle focus-visible:ring-0 focus-visible:ring-offset-0" />
						<ResizablePanel
							aria-hidden={!isInspectorOpen}
							collapsible
							defaultSize={inspectorDefaultSize}
							id="inspector"
							inert={!isInspectorOpen}
							maxSize={`${INSPECTOR_MAX_PERCENT}%`}
							minSize={`${INSPECTOR_MIN_PERCENT}%`}
							onResize={handleInspectorResize}
							panelRef={inspectorRef}
							style={{ overflow: "hidden" }}
						>
							{/* Stable content width while the panel animates (yyork pattern):
                  the pane clips instead of reflowing the inspector mid-collapse. */}
							<div className="h-full min-w-[280px]">
								<SessionInspector session={session} />
							</div>
						</ResizablePanel>
					</>
				) : null}
			</ResizablePanelGroup>
		</div>
	);
}
