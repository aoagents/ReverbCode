import { useEffect, useRef } from "react";
import { Terminal } from "@xterm/xterm";
import { CanvasAddon } from "@xterm/addon-canvas";
import { WebglAddon } from "@xterm/addon-webgl";
import { FitAddon } from "@xterm/addon-fit";
import { SearchAddon } from "@xterm/addon-search";
import { WebLinksAddon } from "@xterm/addon-web-links";
import type { WorkspaceSession } from "../types/workspace";
import type { Theme } from "../stores/ui-store";
import { apiBaseUrl } from "../lib/api-client";
import { createTerminalMux, muxUrlFromApiBase } from "../lib/terminal-mux";

type TerminalPaneProps = {
  session?: WorkspaceSession;
  theme: Theme;
};

export function TerminalPane({ session, theme }: TerminalPaneProps) {
  if (!window.ao) {
    return (
      <pre className="h-full overflow-auto bg-terminal p-4 font-mono text-sm leading-6 text-foreground">
        Agent Orchestrator terminal scaffold{"\n\n"}
        session: {session?.id ?? "none"}{"\n"}
        provider: {session?.provider ?? "unassigned"}{"\n\n"}
        Browser preview uses a static terminal surface. Electron loads @xterm/xterm.
      </pre>
    );
  }

  return <XtermTerminal session={session} theme={theme} />;
}

// Load the GPU-accelerated WebGL renderer, falling back to the 2D canvas
// renderer when WebGL is unavailable (older GPUs, software rendering, context
// loss). Renderer addons must be loaded after terminal.open().
function attachRenderer(terminal: Terminal): void {
  try {
    const webgl = new WebglAddon();
    webgl.onContextLoss(() => {
      webgl.dispose();
      terminal.loadAddon(new CanvasAddon());
    });
    terminal.loadAddon(webgl);
  } catch {
    terminal.loadAddon(new CanvasAddon());
  }
}

function XtermTerminal({ session, theme }: TerminalPaneProps) {
  const containerRef = useRef<HTMLDivElement | null>(null);

  useEffect(() => {
    if (!containerRef.current) return;

    const terminal = new Terminal({
      allowProposedApi: false,
      cursorBlink: true,
      fontFamily: '"SF Mono", Menlo, Monaco, Consolas, monospace',
      fontSize: 13,
      lineHeight: 1.35,
      theme: terminalTheme(theme),
    });
    const fitAddon = new FitAddon();

    terminal.loadAddon(fitAddon);
    terminal.loadAddon(new WebLinksAddon());
    terminal.loadAddon(new SearchAddon());
    terminal.open(containerRef.current);
    attachRenderer(terminal);

    const sessionId = session?.id;
    // Connect this pane to the daemon's terminal multiplexer for the selected
    // session. The mux speaks PTY bytes over a loopback WebSocket; without a
    // selected session there is nothing to attach to.
    const mux = sessionId ? createTerminalMux(muxUrlFromApiBase(apiBaseUrl)) : null;
    const disposers: Array<() => void> = [];
    let rafId: number | undefined;

    const fitTerminal = () => {
      if (!containerRef.current?.clientWidth || !containerRef.current.clientHeight) return;
      try {
        fitAddon.fit();
        if (mux && sessionId) mux.resize(sessionId, terminal.cols, terminal.rows);
      } catch {
        // Electron can report zero-sized panels during startup; the next resize will retry.
      }
    };

    if (mux && sessionId) {
      const onData = mux.onData(sessionId, (bytes) => terminal.write(bytes));
      const onExit = mux.onExit(sessionId, () => terminal.writeln("\r\n\x1b[2m[process exited]\x1b[0m"));
      const input = terminal.onData((data) => mux.sendInput(sessionId, data));
      disposers.push(onData, onExit, () => input.dispose());
      rafId = requestAnimationFrame(() => {
        // Open BEFORE resizing: the backend ignores the open frame's size and only
        // honours a resize once the pane is registered, so a resize sent first is
        // silently dropped (backend manager.go). Fit to compute cols/rows, open, then
        // send the matching resize.
        try {
          fitAddon.fit();
        } catch {
          // panel may be zero-sized at startup; the ResizeObserver retries the fit.
        }
        mux.open(sessionId, terminal.cols, terminal.rows);
        mux.resize(sessionId, terminal.cols, terminal.rows);
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
      terminal.dispose();
    };
  }, [session?.id, session?.provider, theme]);

  return <div ref={containerRef} className="h-full min-h-0 bg-terminal p-3" />;
}

function terminalTheme(theme: Theme) {
  if (theme === "light") {
    return {
      background: "#fbfcfd",
      foreground: "#1f2328",
      cursor: "#1f2328",
      selectionBackground: "#bfdbfe",
    };
  }

  return {
    background: "#0f1014",
    foreground: "#f4f4f5",
    cursor: "#f4f4f5",
    selectionBackground: "#334155",
  };
}
