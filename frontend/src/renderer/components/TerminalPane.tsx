import { useEffect, useRef } from "react";
import { Terminal } from "@xterm/xterm";
import { CanvasAddon } from "@xterm/addon-canvas";
import { FitAddon } from "@xterm/addon-fit";
import { SearchAddon } from "@xterm/addon-search";
import { WebLinksAddon } from "@xterm/addon-web-links";
import type { WorkspaceSession } from "../types/workspace";
import type { Theme } from "../stores/ui-store";

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
    if (window.ao) {
      terminal.loadAddon(new CanvasAddon());
    }
    terminal.open(containerRef.current);
    const fitTerminal = () => {
      if (!containerRef.current?.clientWidth || !containerRef.current.clientHeight) return;
      try {
        fitAddon.fit();
      } catch {
        // Electron can report zero-sized panels during startup; the next resize will retry.
      }
    };
    requestAnimationFrame(fitTerminal);
    terminal.writeln("Agent Orchestrator terminal scaffold");
    terminal.writeln("");
    terminal.writeln(`session: ${session?.id ?? "none"}`);
    terminal.writeln(`provider: ${session?.provider ?? "unassigned"}`);
    terminal.writeln("");
    terminal.writeln("Terminal surface scaffold; daemon streaming is intentionally not wired yet.");

    const resizeObserver = new ResizeObserver(fitTerminal);
    resizeObserver.observe(containerRef.current);

    return () => {
      resizeObserver.disconnect();
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
