import { act, renderHook } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { ReactNode } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { MuxConnectionState, TerminalMux } from "../lib/terminal-mux";
import type { WorkspaceSession } from "../types/workspace";
import { useTerminalSession, type AttachableTerminal } from "./useTerminalSession";
import { workspaceQueryKey } from "./useWorkspaceQuery";

const session: WorkspaceSession = {
	id: "sess-1",
	terminalHandleId: "handle-1",
	workspaceId: "ws-1",
	workspaceName: "demo",
	title: "fix the tests",
	provider: "claude-code",
	branch: "main",
	status: "working",
	updatedAt: "now",
};

type FakeMux = {
	mux: TerminalMux;
	opens: Array<[string, number, number]>;
	resizes: Array<[string, number, number]>;
	inputs: Array<[string, string]>;
	disposed: boolean;
	emitData(id: string, text: string): void;
	emitOpened(id: string): void;
	emitExit(id: string): void;
	emitError(id: string, message: string): void;
	emitConnection(state: MuxConnectionState): void;
};

function subscribe<T>(map: Map<string, Set<T>>, id: string, listener: T): () => void {
	const set = map.get(id) ?? new Set<T>();
	set.add(listener);
	map.set(id, set);
	return () => set.delete(listener);
}

function createFakeMux(): FakeMux {
	const data = new Map<string, Set<(bytes: Uint8Array) => void>>();
	const exit = new Map<string, Set<() => void>>();
	const opened = new Map<string, Set<() => void>>();
	const error = new Map<string, Set<(message: string) => void>>();
	const connection = new Set<(state: MuxConnectionState) => void>();

	const fake: FakeMux = {
		opens: [],
		resizes: [],
		inputs: [],
		disposed: false,
		mux: {
			open: (id, cols, rows) => fake.opens.push([id, cols, rows]),
			sendInput: (id, input) => fake.inputs.push([id, input]),
			resize: (id, cols, rows) => fake.resizes.push([id, cols, rows]),
			close: () => undefined,
			onData: (id, listener) => subscribe(data, id, listener),
			onExit: (id, listener) => subscribe(exit, id, listener),
			onOpened: (id, listener) => subscribe(opened, id, listener),
			onError: (id, listener) => subscribe(error, id, listener),
			onConnectionChange: (listener) => {
				connection.add(listener);
				return () => connection.delete(listener);
			},
			dispose: () => {
				fake.disposed = true;
			},
		},
		emitData: (id, text) => data.get(id)?.forEach((listener) => listener(new TextEncoder().encode(text))),
		emitOpened: (id) => opened.get(id)?.forEach((listener) => listener()),
		emitExit: (id) => exit.get(id)?.forEach((listener) => listener()),
		emitError: (id, message) => error.get(id)?.forEach((listener) => listener(message)),
		emitConnection: (state) => connection.forEach((listener) => listener(state)),
	};
	return fake;
}

type FakeTerminal = AttachableTerminal & {
	lines: string[];
	clears: number;
	typeKeys(data: string): void;
	emitResize(cols: number, rows: number): void;
};

function createFakeTerminal(): FakeTerminal {
	const dataListeners = new Set<(data: string) => void>();
	const resizeListeners = new Set<(size: { cols: number; rows: number }) => void>();
	const terminal: FakeTerminal = {
		cols: 80,
		rows: 24,
		lines: [],
		clears: 0,
		write: (bytes) => terminal.lines.push(new TextDecoder().decode(bytes)),
		writeln: (line) => terminal.lines.push(line),
		clear: () => {
			terminal.clears += 1;
		},
		onData: (listener) => {
			dataListeners.add(listener);
			return { dispose: () => dataListeners.delete(listener) };
		},
		onResize: (listener) => {
			resizeListeners.add(listener);
			return { dispose: () => resizeListeners.delete(listener) };
		},
		typeKeys: (data) => dataListeners.forEach((listener) => listener(data)),
		emitResize: (cols, rows) => resizeListeners.forEach((listener) => listener({ cols, rows })),
	};
	return terminal;
}

function setup({ daemonReady = true, attachedSession = session as WorkspaceSession | undefined } = {}) {
	const muxes: FakeMux[] = [];
	const createMux = () => {
		const fake = createFakeMux();
		muxes.push(fake);
		return fake.mux;
	};
	const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
	const invalidateSpy = vi.spyOn(queryClient, "invalidateQueries");
	const wrapper = ({ children }: { children: ReactNode }) => (
		<QueryClientProvider client={queryClient}>{children}</QueryClientProvider>
	);
	const view = renderHook(
		({ daemonReady: ready }) => useTerminalSession(attachedSession, { daemonReady: ready, createMux }),
		{ initialProps: { daemonReady }, wrapper },
	);
	const terminal = createFakeTerminal();
	let detach: () => void = () => undefined;
	act(() => {
		detach = view.result.current.attach(terminal);
	});
	return { view, terminal, muxes, invalidateSpy, detach: () => detach() };
}

beforeEach(() => {
	vi.useFakeTimers();
});

afterEach(() => {
	vi.useRealTimers();
	vi.restoreAllMocks();
});

describe("useTerminalSession", () => {
	it("opens the pane at the terminal's size and reaches attached on the server ack", () => {
		const { view, muxes } = setup();
		expect(view.result.current.state).toBe("connecting");
		expect(muxes).toHaveLength(1);
		expect(muxes[0].opens).toEqual([["handle-1", 80, 24]]);
		act(() => muxes[0].emitOpened("handle-1"));
		expect(view.result.current.state).toBe("attached");
	});

	it("stays idle when the session has no terminal handle", () => {
		const { view, muxes } = setup({ attachedSession: { ...session, terminalHandleId: undefined } });
		expect(view.result.current.state).toBe("idle");
		expect(muxes).toHaveLength(0);
	});

	it("forwards PTY output, keystrokes, and resizes across the attachment", () => {
		const { terminal, muxes } = setup();
		act(() => muxes[0].emitData("handle-1", "hello"));
		expect(terminal.lines).toContain("hello");
		terminal.typeKeys("ls\r");
		expect(muxes[0].inputs).toEqual([["handle-1", "ls\r"]]);
		terminal.emitResize(120, 40);
		act(() => void vi.advanceTimersByTime(100));
		expect(muxes[0].resizes).toContainEqual(["handle-1", 120, 40]);
	});

	it("collapses a drag's burst of grid changes into one trailing PTY resize, then re-asserts it", () => {
		const { terminal, muxes } = setup();
		const initialResizes = muxes[0].resizes.length; // connect() sends the opening size
		terminal.emitResize(100, 30);
		terminal.emitResize(110, 34);
		terminal.emitResize(120, 40);
		act(() => void vi.advanceTimersByTime(100));
		expect(muxes[0].resizes.slice(initialResizes)).toEqual([["handle-1", 120, 40]]);
		// The settled grid goes out once more: paired with the backend's explicit
		// SIGWINCH (pty_unix.go) it re-syncs a zellij client that lost the
		// original update, which otherwise kept the session laid out for the old
		// size until the next real grid change.
		act(() => void vi.advanceTimersByTime(250));
		expect(muxes[0].resizes.slice(initialResizes)).toEqual([
			["handle-1", 120, 40],
			["handle-1", 120, 40],
		]);
	});

	it("a new resize burst supersedes a pending re-assert", () => {
		const { terminal, muxes } = setup();
		const initialResizes = muxes[0].resizes.length;
		terminal.emitResize(100, 30);
		act(() => void vi.advanceTimersByTime(100)); // settles -> sent, re-assert pending
		terminal.emitResize(120, 40); // user keeps dragging before the re-assert fires
		act(() => void vi.advanceTimersByTime(100 + 250));
		expect(muxes[0].resizes.slice(initialResizes)).toEqual([
			["handle-1", 100, 30],
			["handle-1", 120, 40],
			["handle-1", 120, 40],
		]);
	});

	it("marks exit in the terminal and refetches workspace state instead of writing status", () => {
		const { view, terminal, muxes, invalidateSpy } = setup();
		act(() => muxes[0].emitExit("handle-1"));
		expect(view.result.current.state).toBe("exited");
		expect(terminal.lines.some((line) => line.includes("[process exited]"))).toBe(true);
		expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: workspaceQueryKey });
	});

	it("surfaces pane errors and refetches, with no automatic retry", () => {
		const { view, muxes, invalidateSpy } = setup();
		act(() => muxes[0].emitError("handle-1", "no such pane"));
		expect(view.result.current.state).toBe("error");
		expect(view.result.current.error).toBe("no such pane");
		expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: workspaceQueryKey });
		act(() => muxes[0].emitConnection("closed"));
		act(() => void vi.advanceTimersByTime(60_000));
		expect(muxes).toHaveLength(1);
	});

	it("reattaches with a fresh mux after a socket drop, clearing the stale screen", () => {
		const { view, terminal, muxes } = setup();
		act(() => muxes[0].emitOpened("handle-1"));
		act(() => muxes[0].emitConnection("closed"));
		expect(view.result.current.state).toBe("reattaching");
		act(() => void vi.advanceTimersByTime(500));
		expect(muxes).toHaveLength(2);
		expect(muxes[0].disposed).toBe(true);
		expect(terminal.clears).toBe(1); // the fresh zellij attach repaints over a blank grid
		expect(muxes[1].opens).toEqual([["handle-1", 80, 24]]);
		act(() => muxes[1].emitOpened("handle-1"));
		expect(view.result.current.state).toBe("attached");
	});

	it("backs off between failed reconnect attempts", () => {
		const { muxes } = setup();
		act(() => muxes[0].emitConnection("closed"));
		act(() => void vi.advanceTimersByTime(500)); // attempt 1 after 500ms
		expect(muxes).toHaveLength(2);
		act(() => muxes[1].emitConnection("closed"));
		act(() => void vi.advanceTimersByTime(500)); // attempt 2 needs 1000ms
		expect(muxes).toHaveLength(2);
		act(() => void vi.advanceTimersByTime(500));
		expect(muxes).toHaveLength(3);
	});

	it("waits for daemon readiness instead of retrying, then reconnects when it flips", () => {
		const { view, muxes } = setup({ daemonReady: false });
		act(() => muxes[0].emitConnection("closed"));
		expect(view.result.current.state).toBe("reattaching");
		act(() => void vi.advanceTimersByTime(60_000));
		expect(muxes).toHaveLength(1); // no retries against a dead daemon
		view.rerender({ daemonReady: true });
		expect(muxes).toHaveLength(2); // reconnects immediately, without backoff debt
		act(() => muxes[1].emitOpened("handle-1"));
		expect(view.result.current.state).toBe("attached");
	});

	it("detach disposes the mux, stops reattach, and returns to idle", () => {
		const { view, muxes, detach } = setup();
		act(() => detach());
		expect(view.result.current.state).toBe("idle");
		expect(muxes[0].disposed).toBe(true);
		act(() => muxes[0].emitConnection("closed"));
		act(() => void vi.advanceTimersByTime(60_000));
		expect(muxes).toHaveLength(1);
	});
});
