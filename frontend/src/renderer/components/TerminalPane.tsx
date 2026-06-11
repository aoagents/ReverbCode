import { useEffect, useRef } from "react";
import { Terminal } from "@xterm/xterm";
import { CanvasAddon } from "@xterm/addon-canvas";
import { WebglAddon } from "@xterm/addon-webgl";
import { FitAddon } from "@xterm/addon-fit";
import { SearchAddon } from "@xterm/addon-search";
import { WebLinksAddon } from "@xterm/addon-web-links";
import type { WorkspaceSession } from "../types/workspace";
import type { Theme } from "../stores/ui-store";
import { useTerminalSession, type TerminalSessionState } from "../hooks/useTerminalSession";

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

	return <XtermTerminal session={session} theme={theme} daemonReady={daemonReady} />;
}

function webgl2Available(): boolean {
	try {
		return Boolean(document.createElement("canvas").getContext("webgl2"));
	} catch {
		return false;
	}
}

// Load the GPU-accelerated WebGL renderer when a real WebGL2 context is
// available, falling back to the 2D canvas renderer otherwise (software
// rendering, older GPUs). Probing first avoids loading a half-initialised
// WebglAddon that then throws on dispose. Renderer addons load after open().
function attachRenderer(terminal: Terminal): void {
	if (webgl2Available()) {
		try {
			const webgl = new WebglAddon();
			webgl.onContextLoss(() => webgl.dispose());
			terminal.loadAddon(webgl);
			return;
		} catch {
			// WebGL init failed despite the probe; fall through to canvas.
		}
	}
	try {
		terminal.loadAddon(new CanvasAddon());
	} catch {
		// The renderer addon is an optimisation; the DOM renderer still works.
	}
}

function bannerText(state: TerminalSessionState, error?: string): string | undefined {
	if (state === "reattaching") return "Terminal disconnected — reattaching…";
	if (state === "error") return `Terminal error: ${error ?? "connection failed"}`;
	return undefined;
}

function XtermTerminal({ session, theme, daemonReady }: TerminalPaneProps) {
	const containerRef = useRef<HTMLDivElement | null>(null);
	const terminalRef = useRef<Terminal | null>(null);
	const { attach, state, error } = useTerminalSession(session, { daemonReady });

	useEffect(() => {
		if (!containerRef.current) return;

		const terminal = new Terminal({
			allowProposedApi: false,
			cursorBlink: true,
			fontFamily: 'Menlo, Monaco, Consolas, "Liberation Mono", "Courier New", monospace',
			fontSize: 13,
			lineHeight: 1.35,
			theme: terminalTheme(theme),
		});
		terminalRef.current = terminal;
		const fitAddon = new FitAddon();

		terminal.loadAddon(fitAddon);
		terminal.loadAddon(new WebLinksAddon());
		terminal.loadAddon(new SearchAddon());
		terminal.open(containerRef.current);
		attachRenderer(terminal);

		let detach: (() => void) | undefined;
		let rafId: number | undefined;

		// The attachment forwards size changes itself (terminal.onResize → mux);
		// the component only owns fitting the terminal to its container.
		const fitTerminal = () => {
			if (!containerRef.current?.clientWidth || !containerRef.current.clientHeight) return;
			try {
				fitAddon.fit();
			} catch {
				// Electron can report zero-sized panels during startup; the next resize will retry.
			}
		};

		if (session?.terminalHandleId) {
			rafId = requestAnimationFrame(() => {
				fitTerminal();
				detach = attach(terminal);
			});
		} else {
			rafId = requestAnimationFrame(fitTerminal);
			terminal.writeln("Agent Orchestrator");
			terminal.writeln("");
			terminal.writeln("\x1b[2mNo session selected. Pick a worker to attach its terminal.\x1b[0m");
		}

		const resizeObserver = new ResizeObserver(fitTerminal);
		resizeObserver.observe(containerRef.current);

		return () => {
			if (rafId !== undefined) cancelAnimationFrame(rafId);
			resizeObserver.disconnect();
			detach?.();
			terminalRef.current = null;
			try {
				terminal.dispose();
			} catch {
				// Some xterm renderer addons can throw during dispose in certain GPU
				// environments; the terminal is being torn down regardless.
			}
		};
	}, [session?.id, session?.terminalHandleId, attach]);

	useEffect(() => {
		if (terminalRef.current) {
			terminalRef.current.options.theme = terminalTheme(theme);
		}
	}, [theme]);

	const banner = bannerText(state, error);

	return (
		<div className="relative h-full min-h-0">
			<div ref={containerRef} className="h-full min-h-0 bg-terminal p-3" />
			{banner && (
				<div className="absolute inset-x-3 top-2 rounded-md border border-border bg-surface/95 px-3 py-1.5 font-mono text-[11px] text-muted-foreground">
					{banner}
				</div>
			)}
		</div>
	);
}

// The terminal is the agent CLI; it keeps the emdash dark palette (green cursor) in
// both themes — see DESIGN.md → Color. The `theme` arg is kept for the signature the
// caller uses on theme change.
function terminalTheme(_theme: Theme) {
	return {
		background: "#161616",
		foreground: "#d7d7d2",
		cursor: "#7bd88f",
		cursorAccent: "#161616",
		selectionBackground: "rgba(63, 142, 247, 0.35)",
	};
}
