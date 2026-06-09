import { useEffect, useRef } from "react";
import { Terminal } from "@xterm/xterm";
import { CanvasAddon } from "@xterm/addon-canvas";
import { WebglAddon } from "@xterm/addon-webgl";
import { FitAddon } from "@xterm/addon-fit";
import { SearchAddon } from "@xterm/addon-search";
import { WebLinksAddon } from "@xterm/addon-web-links";
import type { WorkspaceSession } from "../types/workspace";
import type { Theme } from "../stores/ui-store";
import { getApiBaseUrl } from "../lib/api-client";
import { createTerminalMux, muxUrlFromApiBase } from "../lib/terminal-mux";

type TerminalPaneProps = {
  session?: WorkspaceSession;
  theme: Theme;
};

export function TerminalPane({ session, theme }: TerminalPaneProps) {
  if (!window.ao) {
    return (
      <pre className="h-full overflow-auto bg-terminal p-4 font-mono text-[13px] leading-relaxed text-[var(--term-fg)]">
        <span className="text-[var(--term-dim)]">~/{session?.workspaceName ?? "reverbcode"}</span>{" "}
        <span className="text-[var(--term-blue)]">{session?.branch || "main"}</span> $ {session?.provider ?? "claude"}
        {"\n"}
        <span className="text-[var(--term-green)]">✻ Welcome to the agent CLI</span>
        {"\n\n"}
        <span className="text-[var(--term-dim)]">Browser preview renders a static terminal surface. Electron attaches the live PTY.</span>
      </pre>
    );
  }

  return <XtermTerminal session={session} theme={theme} />;
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

function XtermTerminal({ session, theme }: TerminalPaneProps) {
  const containerRef = useRef<HTMLDivElement | null>(null);
  const terminalRef = useRef<Terminal | null>(null);

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

    const sessionId = session?.id;
    const terminalHandleId = session?.terminalHandleId;
    const mux = terminalHandleId ? createTerminalMux(muxUrlFromApiBase(getApiBaseUrl())) : null;
    const disposers: Array<() => void> = [];
    let rafId: number | undefined;

    const fitTerminal = () => {
      if (!containerRef.current?.clientWidth || !containerRef.current.clientHeight) return;
      try {
        fitAddon.fit();
        if (mux && terminalHandleId) mux.resize(terminalHandleId, terminal.cols, terminal.rows);
      } catch {
        // Electron can report zero-sized panels during startup; the next resize will retry.
      }
    };

    if (mux && terminalHandleId) {
      const onData = mux.onData(terminalHandleId, (bytes) => terminal.write(bytes));
      const onExit = mux.onExit(terminalHandleId, () => terminal.writeln("\r\n\x1b[2m[process exited]\x1b[0m"));
      const input = terminal.onData((data) => mux.sendInput(terminalHandleId, data));
      disposers.push(onData, onExit, () => input.dispose());
      terminal.writeln(`\x1b[2mAttaching to ${session?.title ?? sessionId}…\x1b[0m`);
      rafId = requestAnimationFrame(() => {
        try {
          fitAddon.fit();
        } catch {
          // panel may be zero-sized at startup; the ResizeObserver retries the fit.
        }
        mux.open(terminalHandleId, terminal.cols, terminal.rows);
        mux.resize(terminalHandleId, terminal.cols, terminal.rows);
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
      disposers.forEach((dispose) => dispose());
      mux?.dispose();
      terminalRef.current = null;
      try {
        terminal.dispose();
      } catch {
        // Some xterm renderer addons can throw during dispose in certain GPU
        // environments; the terminal is being torn down regardless.
      }
    };
  }, [session?.id, session?.terminalHandleId]);

  useEffect(() => {
    if (terminalRef.current) {
      terminalRef.current.options.theme = terminalTheme(theme);
    }
  }, [theme]);

  return <div ref={containerRef} className="h-full min-h-0 bg-terminal p-3" />;
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
