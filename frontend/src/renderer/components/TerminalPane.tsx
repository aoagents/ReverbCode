import { useCallback, useEffect, useRef, useState } from "react";
import type { WorkspaceSession } from "../types/workspace";
import type { Theme } from "../stores/ui-store";
import { useTerminalSession, type AttachableTerminal, type TerminalSessionState } from "../hooks/useTerminalSession";
import { XtermTerminal } from "./XtermTerminal";

type TerminalPaneProps = {
	session?: WorkspaceSession;
	theme: Theme;
	daemonReady: boolean;
};

export function TerminalPane({ session, theme, daemonReady }: TerminalPaneProps) {
	if (!window.ao) {
		return (
			<pre className="h-full overflow-auto bg-terminal p-4 font-mono text-[13px] leading-relaxed text-[var(--term-fg)]">
				<span className="text-[var(--term-dim)]">~/{session?.workspaceName ?? "reverbcode"}</span>{" "}
				<span className="text-[var(--term-blue)]">{session?.branch || "main"}</span> $ {session?.provider ?? "claude"}
				{"\n"}
				<span className="text-[var(--term-green)]">✻ Welcome to the agent CLI</span>
				{"\n\n"}
				<span className="text-[var(--term-dim)]">
					Browser preview renders a static terminal surface. Electron attaches the live PTY.
				</span>
			</pre>
		);
	}

	return <AttachedTerminal session={session} theme={theme} daemonReady={daemonReady} />;
}

function bannerText(state: TerminalSessionState, error?: string): string | undefined {
	if (state === "reattaching") return "Terminal disconnected — reattaching…";
	if (state === "error") return `Terminal error: ${error ?? "connection failed"}`;
	return undefined;
}

function AttachedTerminal({ session, theme, daemonReady }: TerminalPaneProps) {
	// One terminal instance per pane lifetime (yyork's core rule): switching
	// sessions never remounts XtermTerminal — the attachment effect re-points
	// the mux and clears the screen instead. A keyed remount would tear down the
	// renderer mid-switch and lose the warm GPU surface.
	const [terminal, setTerminal] = useState<AttachableTerminal | null>(null);
	const [initFailed, setInitFailed] = useState(false);
	const { attach, state, error } = useTerminalSession(session, { daemonReady });
	const handleId = session?.terminalHandleId;
	const hadAttachmentRef = useRef(false);

	const handleReady = useCallback((handle: AttachableTerminal) => setTerminal(handle), []);
	const handleInitError = useCallback((err: unknown) => {
		console.error("xterm failed to initialize", err);
		setInitFailed(true);
	}, []);

	useEffect(() => {
		if (!terminal) return;
		// Reuse means the previous session's screen would linger; clear before
		// re-pointing. Screen-clear only, never reset(): every pane PTY is
		// `zellij attach` with identical modes, so the previous session's mouse
		// tracking stays valid while the new attach's handshake + repaint stream
		// in — a full RIS would leave wheel scroll dead for that window (yyork's
		// frozen-scroll regression, solved there the same way). Skipped on the
		// very first attachment: the buffer is empty and the first fit may not
		// have run yet.
		if (hadAttachmentRef.current) {
			terminal.clear();
		}
		hadAttachmentRef.current = true;
		return attach(terminal);
	}, [terminal, handleId, attach]);

	if (initFailed) {
		return (
			<div className="grid h-full place-items-center bg-terminal p-4 font-mono text-[12px] text-muted-foreground">
				Terminal failed to initialize on this GPU/driver. Restart the app to retry.
			</div>
		);
	}

	const banner = bannerText(state, error);
	const showEmptyState = !handleId;

	return (
		<div className="relative h-full min-h-0 bg-terminal">
			<XtermTerminal ariaLabel="Session terminal" onError={handleInitError} onReady={handleReady} theme={theme} />
			{showEmptyState && (
				<div className="absolute inset-0 grid place-items-center bg-terminal font-mono text-[13px]">
					<div className="text-center">
						<div className="text-[var(--term-fg)]">Agent Orchestrator</div>
						<div className="mt-2 text-[var(--term-dim)]">
							No session selected. Pick a worker to attach its terminal.
						</div>
					</div>
				</div>
			)}
			{banner && (
				<div className="absolute inset-x-3 top-2 rounded-md border border-border bg-surface/95 px-3 py-1.5 font-mono text-[11px] text-muted-foreground">
					{banner}
				</div>
			)}
		</div>
	);
}
