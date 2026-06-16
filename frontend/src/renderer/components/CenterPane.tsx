import { ChevronLeft, Shield } from "lucide-react";
import type { Theme } from "../stores/ui-store";
import type { TerminalTarget } from "../types/terminal";
import type { WorkspaceSession } from "../types/workspace";
import { TerminalPane } from "./TerminalPane";

type CenterPaneProps = {
	session?: WorkspaceSession;
	theme: Theme;
	daemonReady: boolean;
	terminalTarget?: TerminalTarget;
	onSelectWorkerTerminal?: () => void;
};

export function CenterPane({ session, theme, daemonReady, terminalTarget, onSelectWorkerTerminal }: CenterPaneProps) {
	const target = terminalTarget ?? { kind: "worker" };

	return (
		<div className="flex h-full min-h-0 min-w-0 flex-col bg-background">
			{target.kind === "reviewer" ? (
				<div className="reviewer-terminal-header">
					<button
						aria-label="Back to agent terminal"
						className="reviewer-terminal-header__back"
						onClick={onSelectWorkerTerminal}
						type="button"
					>
						<ChevronLeft aria-hidden="true" />
						<span>agent</span>
					</button>
					<span className="reviewer-terminal-header__role">
						<Shield aria-hidden="true" />
						Reviewer
					</span>
					<span className="reviewer-terminal-header__harness">{target.harness}</span>
				</div>
			) : null}
			<div className="min-h-0 flex-1">
				<TerminalPane daemonReady={daemonReady} session={session} terminalTarget={target} theme={theme} />
			</div>
		</div>
	);
}
