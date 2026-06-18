// Terminal Attachment (see CONTEXT.md): the live binding between a terminal
// pane and a session's PTY over the shell-owned mux. The hook owns the visible
// attachment lifecycle — open/close ordering, xterm event listeners, error
// surfacing, and exit handling — so the pane component only renders.
//
// Status rule: the frontend never writes a session's display status. On mux
// `exited`/`error` it invalidates the workspaces query and lets the daemon's
// derived status flow back (docs/architecture.md).

import { useQueryClient } from "@tanstack/react-query";
import { useCallback, useEffect, useRef, useState } from "react";
import type { TerminalMux } from "../lib/terminal-mux";
import type { WorkspaceSession } from "../types/workspace";
import { workspaceQueryKey } from "./useWorkspaceQuery";

/**
 * The slice of xterm's Terminal the attachment needs. Structural, so tests can
 * drive the hook with a tiny fake instead of a real xterm + DOM.
 */
export type AttachableTerminal = {
	cols: number;
	rows: number;
	write: (data: Uint8Array) => void;
	writeln: (line: string) => void;
	/**
	 * Erase screen + scrollback and home the cursor, preserving terminal modes.
	 * Never a full reset (RIS): that would drop zellij's mouse-tracking mode
	 * for the gap until the fresh attach's handshake re-asserts it — a window
	 * with wheel scroll dead (see XtermTerminal's CLEAR_SEQUENCE).
	 */
	clear: () => void;
	onData: (listener: (data: string) => void) => { dispose: () => void };
	onResize: (listener: (size: { cols: number; rows: number }) => void) => { dispose: () => void };
};

export type TerminalSessionState =
	| "idle" // nothing attached (no session, or detached)
	| "connecting" // first attach in flight
	| "attached" // server acked the open
	| "reattaching" // socket dropped; waiting on backoff or daemon readiness
	| "exited" // PTY process ended; terminal kept for scrollback
	| "error"; // server reported a pane error; no automatic retry

export type UseTerminalSessionOptions = {
	/** Shell-lifetime mux transport. Browser preview passes null and renders a static pane. */
	mux: TerminalMux | null;
};

// Trailing debounce on grid changes: a pane drag emits a burst of intermediate
// sizes; the attached program should get one SIGWINCH when the drag settles,
// not dozens (yyork's terminal-panel does the same at its socket layer).
const RESIZE_DEBOUNCE_MS = 100;
// One follow-up frame with the same grid after each settled resize. xterm only
// fires onResize on actual grid changes and the kernel only raises SIGWINCH on
// actual size changes, so a resize update the zellij client loses (raced
// mid-attach, coalesced during a drag) would otherwise desync the session's
// layout from the pane until the NEXT real change — the terminal keeps
// painting at the old size. The backend answers every resize frame with an
// explicit SIGWINCH (pty_unix.go), so this re-assert makes the client re-read
// and re-report its grid; when everything is already in sync it's a no-op.
const RESIZE_REASSERT_MS = 250;

export function useTerminalSession(session: WorkspaceSession | undefined, options: UseTerminalSessionOptions) {
	const queryClient = useQueryClient();
	const [state, setState] = useState<TerminalSessionState>("idle");
	const [error, setError] = useState<string | undefined>(undefined);

	const sessionRef = useRef(session);
	sessionRef.current = session;
	const optionsRef = useRef(options);
	optionsRef.current = options;
	const stateRef = useRef<TerminalSessionState>(state);
	const openVisibleRef = useRef<(clearBeforeOpen?: boolean) => void>(() => undefined);

	const runtime = useRef({
		terminal: null as AttachableTerminal | null,
		mux: null as TerminalMux | null,
		handle: null as string | null,
		disposers: [] as Array<() => void>,
		resizeTimer: null as ReturnType<typeof setTimeout> | null,
		firstAttach: true,
		detached: true,
	});

	const transition = useCallback((next: TerminalSessionState) => {
		stateRef.current = next;
		setState(next);
	}, []);

	const invalidateWorkspaces = useCallback(() => {
		void queryClient.invalidateQueries({ queryKey: workspaceQueryKey });
	}, [queryClient]);

	const detachVisible = useCallback(() => {
		const r = runtime.current;
		if (r.resizeTimer) {
			clearTimeout(r.resizeTimer);
			r.resizeTimer = null;
		}
		const mux = r.mux;
		const handle = r.handle;
		r.disposers.forEach((dispose) => dispose());
		r.disposers = [];
		if (mux && handle) {
			mux.close(handle);
		}
		r.mux = null;
	}, []);

	const openVisible = useCallback((clearBeforeOpen = false) => {
		const r = runtime.current;
		const { terminal, handle, mux } = r;
		if (!terminal || !handle || !mux || r.detached) return;
		if (clearBeforeOpen || !r.firstAttach) {
			terminal.clear();
		}
		r.firstAttach = false;
		mux.open(handle, terminal.cols, terminal.rows);
		mux.resize(handle, terminal.cols, terminal.rows);
	}, []);
	openVisibleRef.current = openVisible;

	const bindVisible = useCallback(() => {
		const r = runtime.current;
		const { terminal, handle } = r;
		const mux = optionsRef.current.mux;
		if (!terminal || !handle || !mux || r.detached) return;
		r.mux = mux;

		r.disposers.push(
			mux.onData(handle, (bytes) => terminal.write(bytes)),
			mux.onOpened(handle, () => {
				setError(undefined);
				transition("attached");
			}),
			mux.onExit(handle, () => {
				terminal.writeln("\r\n\x1b[2m[process exited]\x1b[0m");
				transition("exited");
				invalidateWorkspaces();
			}),
			mux.onError(handle, (message) => {
				terminal.writeln(`\r\n\x1b[2m[terminal error] ${message}\x1b[0m`);
				setError(message);
				transition("error");
				invalidateWorkspaces();
			}),
			mux.onConnectionChange((connectionState) => {
				if (connectionState === "closed") {
					if (r.detached || !r.terminal || !r.handle) return;
					if (stateRef.current === "exited" || stateRef.current === "error") return;
					transition("reattaching");
				} else {
					openVisibleRef.current(true);
				}
			}),
		);
		const input = terminal.onData((data) => mux.sendInput(handle, data));
		// xterm only fires onResize when the grid actually changed; the debounce
		// additionally collapses a drag's burst of changes into one PTY resize.
		// Each settled resize is re-asserted once (see RESIZE_REASSERT_MS); both
		// stages share resizeTimer so a new burst or teardown cancels either.
		const resize = terminal.onResize(({ cols, rows }) => {
			if (r.resizeTimer) clearTimeout(r.resizeTimer);
			r.resizeTimer = setTimeout(() => {
				mux.resize(handle, cols, rows);
				r.resizeTimer = setTimeout(() => {
					r.resizeTimer = null;
					mux.resize(handle, cols, rows);
				}, RESIZE_REASSERT_MS);
			}, RESIZE_DEBOUNCE_MS);
		});
		r.disposers.push(
			() => input.dispose(),
			() => resize.dispose(),
		);

		// Connection status is chrome (the pane's banner), never buffer content —
		// the PTY owns the buffer. Each open spawns a fresh server-side `zellij
		// attach` (backend internal/terminal/attachment.go) that answers with its
		// init handshake + a full repaint; clear the stale screen so the repaint
		// lands on a blank grid. Screen-clear only, never reset(): RIS would drop
		// zellij's mouse-tracking mode until the handshake lands.
		openVisible(false);
	}, [invalidateWorkspaces, openVisible, transition]);

	/**
	 * Bind a terminal to the current session's PTY. Call once the terminal is
	 * opened (and fitted); returns the detach function for effect cleanup.
	 */
	const attach = useCallback(
		(terminal: AttachableTerminal) => {
			const r = runtime.current;
			const handle = sessionRef.current?.terminalHandleId ?? null;
			r.terminal = terminal;
			r.handle = handle;
			r.mux = null;
			r.detached = false;
			r.firstAttach = true;
			setError(undefined);
			if (handle && optionsRef.current.mux) {
				transition("connecting");
				bindVisible();
			} else if (handle) {
				transition("reattaching");
			} else {
				transition("idle");
			}
			return () => {
				r.detached = true;
				detachVisible();
				r.terminal = null;
				r.handle = null;
				setError(undefined);
				transition("idle");
			};
		},
		[bindVisible, detachVisible, transition],
	);

	const mux = options.mux;
	useEffect(() => {
		const r = runtime.current;
		if (r.detached || r.mux === mux) return;
		detachVisible();
		r.mux = null;
		if (!mux) {
			if (r.handle) transition("reattaching");
			return;
		}
		if (r.handle) {
			transition("connecting");
			bindVisible();
		}
	}, [bindVisible, detachVisible, mux, transition]);

	// Belt-and-braces: never leak a socket past unmount, even if the owner
	// forgot to call detach.
	useEffect(
		() => () => {
			runtime.current.detached = true;
			detachVisible();
		},
		[detachVisible],
	);

	return { attach, state, error };
}
