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
import { buildTerminalThemes } from "../lib/terminal-themes";
import type { Theme } from "../stores/ui-store";

export type XtermTerminalProps = {
	ariaLabel?: string;
	className?: string;
	theme: Theme;
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

// xterm palette tracks the app theme (see lib/terminal-themes.ts + --term-* in
// styles.css). The PTY content is still the agent's own ANSI output.
const terminalThemes = buildTerminalThemes();

// Erase scrollback (3J) + display (2J) and home the cursor — yyork's
// terminalResetSequence. Deliberately NOT term.reset(): every pane PTY is a
// fresh per-client `zellij attach` whose handshake re-asserts the DEC private
// modes (SGR mouse tracking, alt screen) anyway, but a full RIS would drop
// them for the window until that handshake arrives — a flash where wheel
// events stop reaching zellij. The clear only wipes pixels; modes stay up.
const CLEAR_SEQUENCE = "\x1b[3J\x1b[2J\x1b[H";

export function XtermTerminal(props: XtermTerminalProps) {
	const hostRef = useRef<HTMLDivElement | null>(null);
	const termRef = useRef<Terminal | null>(null);
	// Latest callbacks in a ref so the mount effect stays dependency-free — we
	// never tear down and recreate the terminal because a handler identity
	// changed between renders.
	const callbacksRef = useRef(props);

	useEffect(() => {
		callbacksRef.current = props;
	});

	useEffect(() => {
		const term = termRef.current;
		if (!term) return;
		term.options.theme = props.theme === "dark" ? terminalThemes.dark : terminalThemes.light;
	}, [props.theme]);

	useEffect(() => {
		const host = hostRef.current;
		if (!host) return undefined;

		let term: Terminal;
		try {
			term = new Terminal({
				// Required for the Unicode 11 width addon below.
				allowProposedApi: true,
				cursorBlink: true,
				// Resolve the Nerd Font stack from --font-mono (styles.css) at
				// construction so terminal glyphs follow the app's font tokens. The
				// box-drawing grid is rasterized by the WebGL/canvas renderer itself,
				// but powerline separators and file-type icons are real PUA codepoints
				// that must come from a system-installed Nerd Font.
				fontFamily:
					getComputedStyle(host).getPropertyValue("--font-mono").trim() ||
					'ui-monospace, Menlo, Monaco, "Courier New", monospace',
				fontSize: 13,
				lineHeight: 1.35,
				// Agent TUIs leave SGR bold active while using ANSI black for
				// separators; keep bold weight-only so black stays black.
				drawBoldTextInBrightColors: false,
				// Auto-adjust glyph colors that don't clear WCAG AA against their cell
				// background, the way VS Code's terminal does; without it dim colors
				// render washed out.
				minimumContrastRatio: 4.5,
				// The mux PTY runs `zellij attach` (backend AttachCommand), a
				// full-screen alt-buffer app that owns scrollback itself — same as
				// yyork. xterm's own buffer never accumulates history (the alt screen
				// doesn't feed scrollback), and wheel events reach zellij as mouse
				// reports instead of scrolling locally. 0 also stops FitAddon
				// reserving ~14px on the right for a scrollbar that can never appear.
				scrollback: 0,
				theme: props.theme === "dark" ? terminalThemes.dark : terminalThemes.light,
			});
		} catch (error) {
			callbacksRef.current.onError?.(error);
			return undefined;
		}

		termRef.current = term;

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
			clear: () => term.write(CLEAR_SEQUENCE),
			onData: (listener) => term.onData(listener),
			onResize: (listener) => term.onResize(listener),
		};
		callbacksRef.current.onReady?.(handle);

		return () => {
			termRef.current = null;
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
