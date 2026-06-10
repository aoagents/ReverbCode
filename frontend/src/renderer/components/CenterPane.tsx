import { Columns2 } from "lucide-react";
import type { Theme, WorkbenchView } from "../stores/ui-store";
import type { WorkspaceSession } from "../types/workspace";
import { TerminalPane } from "./TerminalPane";

type CenterPaneProps = {
	view: WorkbenchView;
	session?: WorkspaceSession;
	theme: Theme;
	daemonReady: boolean;
};

export function CenterPane({ view, session, theme, daemonReady }: CenterPaneProps) {
	const isOrchestrator = view === "orchestrator";
	const agentLabel = session?.provider ?? "claude-code";

	return (
		<div className="flex min-w-0 flex-1 flex-col bg-background">
			<div className="flex h-[38px] shrink-0 items-center border-b border-border px-2.5">
				<div className="-mb-px flex h-[38px] items-center gap-2 border-b-2 border-accent px-3 text-[13px] text-foreground">
					<span className="h-[7px] w-[7px] rounded-full bg-success shadow-[0_0_0_3px_rgb(108_177_108_/_0.24)]" />
					{isOrchestrator ? (
						<>
							orchestrator <span className="font-mono text-[11px] text-passive">{agentLabel}</span>
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
				<TerminalPane session={session} theme={theme} daemonReady={daemonReady} />
			</div>
		</div>
	);
}
