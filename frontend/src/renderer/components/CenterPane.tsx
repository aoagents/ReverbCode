import type { Theme } from "../stores/ui-store";
import type { WorkspaceSession } from "../types/workspace";
import { TerminalPane } from "./TerminalPane";

type CenterPaneProps = {
	session?: WorkspaceSession;
	theme: Theme;
	daemonReady: boolean;
};

export function CenterPane({ session, theme, daemonReady }: CenterPaneProps) {
	return (
		<div className="flex h-full min-h-0 min-w-0 flex-col bg-background">
			<div className="min-h-0 flex-1">
				<TerminalPane session={session} theme={theme} daemonReady={daemonReady} />
			</div>
		</div>
	);
}
