// Terminal Attachment (see CONTEXT.md): the live binding between a terminal
// pane and a session's PTY over the mux. The hook owns the whole attachment
// lifecycle — open ordering, auto-reattach with backoff, error surfacing, and
// exit handling — so the pane component only renders.
//
// Status rule: the frontend never writes a session's display status. On mux
// `exited`/`error` it invalidates the workspaces query and lets the daemon's
// derived status flow back (docs/architecture.md).

import { useQueryClient } from "@tanstack/react-query";
import { useCallback, useEffect, useRef, useState } from "react";
import { getApiBaseUrl } from "../lib/api-client";
import { createTerminalMux, muxUrlFromApiBase, type TerminalMux } from "../lib/terminal-mux";
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
	reset: () => void;
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
	/** Gates auto-reattach: when false, a dropped socket waits instead of retrying. */
	daemonReady: boolean;
	/** Test seam: build the mux client. Defaults to a fresh socket against the current API base. */
	createMux?: () => TerminalMux;
};

const RETRY_BASE_MS = 500;
const RETRY_MAX_MS = 8_000;

function defaultCreateMux(): TerminalMux {
	// Resolved per connect, not per hook: a daemon restart can change the port.
	return createTerminalMux(muxUrlFromApiBase(getApiBaseUrl()));
}

export function useTerminalSession(session: WorkspaceSession | undefined, options: UseTerminalSessionOptions) {
	const queryClient = useQueryClient();
	const [state, setState] = useState<TerminalSessionState>("idle");
	const [error, setError] = useState<string | undefined>(undefined);

	const sessionRef = useRef(session);
	sessionRef.current = session;
	const optionsRef = useRef(options);
	optionsRef.current = options;
	const stateRef = useRef<TerminalSessionState>(state);
	const connectRef = useRef<() => void>(() => undefined);

	const runtime = useRef({
		terminal: null as AttachableTerminal | null,
		mux: null as TerminalMux | null,
		handle: null as string | null,
		disposers: [] as Array<() => void>,
		retryTimer: null as ReturnType<typeof setTimeout> | null,
		attempts: 0,
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

	const teardownMux = useCallback(() => {
		const r = runtime.current;
		if (r.retryTimer) {
			clearTimeout(r.retryTimer);
			r.retryTimer = null;
		}
		r.disposers.forEach((dispose) => dispose());
		r.disposers = [];
		r.mux?.dispose();
		r.mux = null;
	}, []);

	const scheduleReattach = useCallback(() => {
		const r = runtime.current;
		if (r.detached || !r.terminal || !r.handle) return;
		// A socket dropping after the PTY ended (or errored) changes nothing.
		if (stateRef.current === "exited" || stateRef.current === "error") return;
		transition("reattaching");
		// Not ready → no timer; the daemonReady effect reconnects when it flips.
		if (!optionsRef.current.daemonReady) return;
		if (r.retryTimer) return;
		const delay = Math.min(RETRY_BASE_MS * 2 ** r.attempts, RETRY_MAX_MS);
		r.attempts += 1;
		r.retryTimer = setTimeout(() => {
			r.retryTimer = null;
			connectRef.current();
		}, delay);
	}, [transition]);

	const connect = useCallback(() => {
		const r = runtime.current;
		const { terminal, handle } = r;
		if (!terminal || !handle || r.detached) return;
		teardownMux();

		const mux = (optionsRef.current.createMux ?? defaultCreateMux)();
		r.mux = mux;

		r.disposers.push(
			mux.onData(handle, (bytes) => terminal.write(bytes)),
			mux.onOpened(handle, () => {
				r.attempts = 0;
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
				if (connectionState === "closed") scheduleReattach();
			}),
		);
		const input = terminal.onData((data) => mux.sendInput(handle, data));
		const resize = terminal.onResize(({ cols, rows }) => mux.resize(handle, cols, rows));
		r.disposers.push(
			() => input.dispose(),
			() => resize.dispose(),
		);

		if (r.firstAttach) {
			terminal.writeln(`\x1b[2mAttaching to ${sessionRef.current?.title ?? handle}…\x1b[0m`);
		} else {
			// The server replays the recent-output ring on open (backend
			// internal/terminal/ring.go); drop the stale screen so it isn't doubled.
			terminal.reset();
		}
		r.firstAttach = false;

		mux.open(handle, terminal.cols, terminal.rows);
		mux.resize(handle, terminal.cols, terminal.rows);
	}, [invalidateWorkspaces, scheduleReattach, teardownMux, transition]);
	connectRef.current = connect;

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
			r.detached = false;
			r.attempts = 0;
			r.firstAttach = true;
			setError(undefined);
			if (handle) {
				transition("connecting");
				connect();
			} else {
				transition("idle");
			}
			return () => {
				r.detached = true;
				teardownMux();
				r.terminal = null;
				r.handle = null;
				setError(undefined);
				transition("idle");
			};
		},
		[connect, teardownMux, transition],
	);

	// Daemon came back while we were waiting: reconnect immediately, without
	// backoff debt from attempts made against the dead daemon.
	const daemonReady = options.daemonReady;
	useEffect(() => {
		const r = runtime.current;
		if (!daemonReady || r.detached) return;
		if (stateRef.current !== "reattaching" || r.retryTimer) return;
		r.attempts = 0;
		connect();
	}, [daemonReady, connect]);

	// Belt-and-braces: never leak a socket past unmount, even if the owner
	// forgot to call detach.
	useEffect(
		() => () => {
			runtime.current.detached = true;
			teardownMux();
		},
		[teardownMux],
	);

	return { attach, state, error };
}
