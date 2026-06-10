// Self-contained xterm.js surface, ported from yyork's terminal architecture.
//
// Design rules (the reason this component exists):
//  - The mount effect is dependency-free: the terminal instance is created once
//    per mount and NEVER torn down because a callback identity or session
//    changed. Session switching is the owner's job (re-point the mux, clear the
//    screen) — see TerminalPane.
//  - Nothing writes into the buffer at mount. Status/empty-state belongs to DOM
//    chrome around the terminal, not inside it. Writing before layout settles
//    is what crashed xterm's Viewport (`dimensions` of a zero-sized renderer).
//  - Fitting runs on several triggers, not one: FitAddon derives the column
//    count from measured cell width, and if it measures before the monospace
//    font's real metrics are resolved it over-counts columns and the grid
//    overflows the panel. So: next frame, two settle timeouts, fonts.ready,
//    and a ResizeObserver. xterm itself only fires onResize when the grid
//    actually changed, so repeated fits don't spam the PTY.

import { useEffect, useRef } from "react";
import { Terminal } from "@xterm/xterm";
import { CanvasAddon } from "@xterm/addon-canvas";
import { FitAddon } from "@xterm/addon-fit";
import { SearchAddon } from "@xterm/addon-search";
import { Unicode11Addon } from "@xterm/addon-unicode11";
import { WebLinksAddon } from "@xterm/addon-web-links";
import { WebglAddon } from "@xterm/addon-webgl";
import type { AttachableTerminal } from "../hooks/useTerminalSession";

export type XtermTerminalProps = {
  ariaLabel?: string;
  className?: string;
  /** Terminal construction failed; the owner decides how to surface it. */
  onError?: (error: unknown) => void;
  /**
   * The terminal is open in the DOM and ready to be attached to a PTY. The
   * handle stays valid until unmount; cols/rows are live getters.
   */
  onReady?: (terminal: AttachableTerminal) => void;
};

// Prefer the WebGL renderer, fall back to 2D canvas. Both rasterize box-drawing
// glyphs themselves onto a fixed cell grid; the DOM renderer does not, so TUI
// borders would drift. Loaded after open().
function loadRenderer(term: Terminal): void {
  try {
    const webgl = new WebglAddon();
    webgl.onContextLoss(() => webgl.dispose());
    term.loadAddon(webgl);
    return;
  } catch {
    // WebGL context unavailable — fall through to the canvas renderer.
  }
  try {
    term.loadAddon(new CanvasAddon());
  } catch (error) {
    console.warn("xterm: WebGL and canvas renderers unavailable; box-drawing may drift", error);
  }
}

// The terminal is the agent CLI; it keeps the emdash dark palette in both app
// themes — see DESIGN.md → Color. ANSI 0-7 normal / 8-15 bright follow the
// VS Code dark palette except where an emdash token exists (green, blue,
// yellow, brightBlack).
const TERMINAL_THEME = {
  background: "#161616",
  foreground: "#d7d7d2",
  cursor: "#7bd88f",
  cursorAccent: "#161616",
  selectionBackground: "rgba(63, 142, 247, 0.35)",
  black: "#000000",
  red: "#cd3131",
  green: "#7bd88f",
  yellow: "#ffcc4a",
  blue: "#5b9dff",
  magenta: "#bc3fbc",
  cyan: "#11a8cd",
  white: "#e5e5e5",
  brightBlack: "#7c7c7c",
  brightRed: "#f14c4c",
  brightGreen: "#23d18b",
  brightYellow: "#f5f543",
  brightBlue: "#3b8eea",
  brightMagenta: "#d670d6",
  brightCyan: "#29b8db",
  brightWhite: "#ffffff",
};

export function XtermTerminal(props: XtermTerminalProps) {
  const hostRef = useRef<HTMLDivElement | null>(null);
  // Latest callbacks in a ref so the mount effect stays dependency-free — we
  // never tear down and recreate the terminal because a handler identity
  // changed between renders.
  const callbacksRef = useRef(props);

  useEffect(() => {
    callbacksRef.current = props;
  });

  useEffect(() => {
    const host = hostRef.current;
    if (!host) return undefined;

    let term: Terminal;
    try {
      term = new Terminal({
        // Required for the Unicode 11 width addon below.
        allowProposedApi: true,
        cursorBlink: true,
        fontFamily: 'Menlo, Monaco, Consolas, "Liberation Mono", "Courier New", monospace',
        fontSize: 13,
        lineHeight: 1.35,
        // Agent TUIs leave SGR bold active while using ANSI black for
        // separators; keep bold weight-only so black stays black.
        drawBoldTextInBrightColors: false,
        // Auto-adjust glyph colors that don't clear WCAG AA against their cell
        // background, the way VS Code's terminal does; without it dim colors
        // render washed out.
        minimumContrastRatio: 4.5,
        // Unlike yyork (zellij owns scrollback there, so it uses 0), the PTY
        // here runs the agent CLI directly — xterm's buffer IS the scrollback.
        scrollback: 4000,
        theme: TERMINAL_THEME,
      });
    } catch (error) {
      callbacksRef.current.onError?.(error);
      return undefined;
    }

    const fit = new FitAddon();
    term.loadAddon(fit);
    const unicode = new Unicode11Addon();
    term.loadAddon(unicode);
    term.unicode.activeVersion = "11";
    term.loadAddon(new WebLinksAddon());
    term.loadAddon(new SearchAddon());

    term.open(host);
    loadRenderer(term);

    const fitTerminal = () => {
      try {
        fit.fit();
      } catch {
        // Container momentarily has no size (hidden/unmounting) — a later
        // trigger retries.
      }
    };

    const raf = requestAnimationFrame(fitTerminal);
    const settleTimers = [window.setTimeout(fitTerminal, 50), window.setTimeout(fitTerminal, 250)];
    if (document.fonts?.ready) {
      void document.fonts.ready.then(fitTerminal);
    }
    const observer = new ResizeObserver(fitTerminal);
    observer.observe(host);

    // Live cols/rows getters: the owner reads the current grid at attach time,
    // not a snapshot taken at ready time (the first fit may not have run yet).
    const handle: AttachableTerminal = {
      get cols() {
        return term.cols;
      },
      get rows() {
        return term.rows;
      },
      write: (data) => term.write(data),
      writeln: (line) => term.writeln(line),
      reset: () => term.reset(),
      onData: (listener) => term.onData(listener),
      onResize: (listener) => term.onResize(listener),
    };
    callbacksRef.current.onReady?.(handle);

    return () => {
      cancelAnimationFrame(raf);
      for (const timer of settleTimers) window.clearTimeout(timer);
      observer.disconnect();
      try {
        term.dispose();
      } catch {
        // Some renderer addons can throw during dispose in certain GPU
        // environments; the terminal is being torn down regardless.
      }
    };
  }, []);

  return (
    <div
      ref={hostRef}
      aria-label={props.ariaLabel}
      className={props.className}
      style={{ height: "100%", overflow: "hidden", width: "100%" }}
    />
  );
}
