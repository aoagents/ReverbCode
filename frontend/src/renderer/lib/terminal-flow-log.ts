export type TerminalFlowFields = Record<string, string | number | boolean | null | undefined>;

export function logTerminalFlow(event: string, fields: TerminalFlowFields = {}): void {
	if (typeof window === "undefined") return;
	try {
		window.ao?.diagnostics.logTerminal(event, {
			...fields,
			pathname: window.location.pathname,
		});
	} catch {
		// Diagnostics must not affect the terminal path being diagnosed.
	}
}
