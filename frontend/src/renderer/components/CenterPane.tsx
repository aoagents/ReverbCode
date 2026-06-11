import { Columns2, Waypoints } from "lucide-react";
import { useState } from "react";
import type { Theme, WorkbenchView } from "../stores/ui-store";
import type { WorkspaceSession, WorkspaceSummary } from "../types/workspace";
import { Button } from "./ui/button";
import { TerminalPane } from "./TerminalPane";

type CenterPaneProps = {
	view: WorkbenchView;
	session?: WorkspaceSession;
	theme: Theme;
	daemonReady: boolean;
	/** Project whose orchestrator the orchestrator view fronts. */
	workspace?: WorkspaceSummary | null;
	onStartOrchestrator?: () => Promise<void>;
};

export function CenterPane({ view, session, theme, daemonReady, workspace, onStartOrchestrator }: CenterPaneProps) {
	const isOrchestrator = view === "orchestrator";
	const agentLabel = session?.provider ?? "claude-code";
	const live = Boolean(session?.terminalHandleId);

	return (
		<div className="flex min-w-0 flex-1 flex-col bg-background">
			<div className="flex h-[38px] shrink-0 items-center border-b border-border px-2.5">
				<div className="-mb-px flex h-[38px] items-center gap-2 border-b-2 border-accent px-3 text-[13px] text-foreground">
					<span
						className={
							live
								? "h-[7px] w-[7px] rounded-full bg-success shadow-[0_0_0_3px_rgb(108_177_108_/_0.24)]"
								: "h-[7px] w-[7px] rounded-full bg-passive"
						}
					/>
					{isOrchestrator ? (
						<>
							orchestrator{" "}
							<span className="font-mono text-[11px] text-passive">{session ? agentLabel : "not running"}</span>
						</>
					) : (
						<>
							{agentLabel} <span className="font-mono text-[11px] text-passive">(1)</span>
						</>
					)}
				</div>
				{!isOrchestrator && (
					<button
						aria-label="Split terminal"
						className="ml-auto grid h-7 w-7 place-items-center rounded-md text-passive transition-colors hover:bg-raised hover:text-muted-foreground"
						type="button"
					>
						<Columns2 className="h-[15px] w-[15px]" aria-hidden="true" />
					</button>
				)}
			</div>

			<div className="min-h-0 flex-1">
				{isOrchestrator && !session ? (
					<StartOrchestratorPane workspace={workspace} onStart={onStartOrchestrator} />
				) : (
					<TerminalPane session={session} theme={theme} daemonReady={daemonReady} />
				)}
			</div>
		</div>
	);
}

/**
 * Empty state for a project with no live orchestrator: explains the model and
 * offers the one action that makes the view real (spawn via POST /orchestrators).
 */
function StartOrchestratorPane({
	workspace,
	onStart,
}: {
	workspace?: WorkspaceSummary | null;
	onStart?: () => Promise<void>;
}) {
	const [isStarting, setIsStarting] = useState(false);
	const [error, setError] = useState<string | null>(null);

	const start = async () => {
		if (!onStart) return;
		setError(null);
		setIsStarting(true);
		try {
			await onStart();
		} catch (err) {
			setError(err instanceof Error ? err.message : "Could not start orchestrator");
		} finally {
			setIsStarting(false);
		}
	};

	// This pane sits on the terminal surface, which keeps the dark palette in
	// both themes (DESIGN.md → Color) — so it uses --term-* tokens, not theme ones.
	return (
		<div className="grid h-full place-items-center bg-terminal p-6">
			<div className="flex max-w-sm flex-col items-center text-center">
				<span className="grid h-10 w-10 place-items-center rounded-xl bg-white/[0.06]">
					<Waypoints className="h-5 w-5 text-[var(--term-blue)]" aria-hidden="true" />
				</span>
				{workspace ? (
					<>
						<p className="mt-4 text-[13.5px] font-medium text-[var(--term-fg)]">No orchestrator running</p>
						<p className="mt-1 text-[12.5px] leading-relaxed text-[var(--term-dim)]">
							The orchestrator coordinates <span className="text-[var(--term-fg)]">{workspace.name}</span> — talk to it
							and it spawns and manages workers for you.
						</p>
						<Button className="mt-4" disabled={isStarting} onClick={() => void start()} variant="primary">
							{isStarting ? "Starting…" : "Start orchestrator"}
						</Button>
						{error && (
							<p className="mt-3 text-[12px] text-error" role="alert">
								{error}
							</p>
						)}
					</>
				) : (
					<>
						<p className="mt-4 text-[13.5px] font-medium text-[var(--term-fg)]">No project yet</p>
						<p className="mt-1 text-[12.5px] leading-relaxed text-[var(--term-dim)]">
							Register a git repository from the sidebar to start its orchestrator.
						</p>
					</>
				)}
			</div>
		</div>
	);
}
