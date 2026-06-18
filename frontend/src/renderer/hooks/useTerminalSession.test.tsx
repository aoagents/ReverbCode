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
	closes: string[];
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
	let connectionState: MuxConnectionState = "open";

	const fake: FakeMux = {
		opens: [],
		resizes: [],
		inputs: [],
		closes: [],
		mux: {
			open: (id, cols, rows) => {
				if (connectionState === "open") fake.opens.push([id, cols, rows]);
			},
			sendInput: (id, input) => {
				if (connectionState === "open") fake.inputs.push([id, input]);
			},
			resize: (id, cols, rows) => {
				if (connectionState === "open") fake.resizes.push([id, cols, rows]);
			},
			close: (id) => {
				if (connectionState === "open") fake.closes.push(id);
			},
			onData: (id, listener) => subscribe(data, id, listener),
			onExit: (id, listener) => subscribe(exit, id, listener),
			onOpened: (id, listener) => subscribe(opened, id, listener),
			onError: (id, listener) => subscribe(error, id, listener),
			onConnectionChange: (listener) => {
				connection.add(listener);
				return () => connection.delete(listener);
			},
			dispose: () => undefined,
		},
		emitData: (id, text) => data.get(id)?.forEach((listener) => listener(new TextEncoder().encode(text))),
		emitOpened: (id) => opened.get(id)?.forEach((listener) => listener()),
		emitExit: (id) => exit.get(id)?.forEach((listener) => listener()),
		emitError: (id, message) => error.get(id)?.forEach((listener) => listener(message)),
		emitConnection: (state) => {
			connectionState = state;
			connection.forEach((listener) => listener(state));
		},
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

function setup({ attachedSession = session as WorkspaceSession | undefined, mux = createFakeMux() } = {}) {
	const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
	const invalidateSpy = vi.spyOn(queryClient, "invalidateQueries");
	const wrapper = ({ children }: { children: ReactNode }) => (
		<QueryClientProvider client={queryClient}>{children}</QueryClientProvider>
	);
	const view = renderHook(
		({ currentSession }) => useTerminalSession(currentSession, { mux: mux.mux }),
		{ initialProps: { currentSession: attachedSession }, wrapper },
	);
	const terminal = createFakeTerminal();
	let detach: () => void = () => undefined;
	act(() => {
		detach = view.result.current.attach(terminal);
	});
	return { view, terminal, mux, invalidateSpy, detach: () => detach() };
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
		const { view, mux } = setup();
		expect(view.result.current.state).toBe("connecting");
		expect(mux.opens).toEqual([["handle-1", 80, 24]]);
		act(() => mux.emitOpened("handle-1"));
		expect(view.result.current.state).toBe("attached");
	});

	it("stays idle when the session has no terminal handle", () => {
		const { view, mux } = setup({ attachedSession: { ...session, terminalHandleId: undefined } });
		expect(view.result.current.state).toBe("idle");
		expect(mux.opens).toHaveLength(0);
	});

	it("forwards PTY output, keystrokes, and resizes across the attachment", () => {
		const { terminal, mux } = setup();
		act(() => mux.emitData("handle-1", "hello"));
		expect(terminal.lines).toContain("hello");
		terminal.typeKeys("ls\r");
		expect(mux.inputs).toEqual([["handle-1", "ls\r"]]);
		terminal.emitResize(120, 40);
		act(() => void vi.advanceTimersByTime(100));
		expect(mux.resizes).toContainEqual(["handle-1", 120, 40]);
	});

	it("collapses a drag's burst of grid changes into one trailing PTY resize, then re-asserts it", () => {
		const { terminal, mux } = setup();
		const initialResizes = mux.resizes.length; // attach sends the opening size
		terminal.emitResize(100, 30);
		terminal.emitResize(110, 34);
		terminal.emitResize(120, 40);
		act(() => void vi.advanceTimersByTime(100));
		expect(mux.resizes.slice(initialResizes)).toEqual([["handle-1", 120, 40]]);
		// The settled grid goes out once more: paired with the backend's explicit
		// SIGWINCH (pty_unix.go) it re-syncs a zellij client that lost the
		// original update, which otherwise kept the session laid out for the old
		// size until the next real grid change.
		act(() => void vi.advanceTimersByTime(250));
		expect(mux.resizes.slice(initialResizes)).toEqual([
			["handle-1", 120, 40],
			["handle-1", 120, 40],
		]);
	});

	it("a new resize burst supersedes a pending re-assert", () => {
		const { terminal, mux } = setup();
		const initialResizes = mux.resizes.length;
		terminal.emitResize(100, 30);
		act(() => void vi.advanceTimersByTime(100)); // settles -> sent, re-assert pending
		terminal.emitResize(120, 40); // user keeps dragging before the re-assert fires
		act(() => void vi.advanceTimersByTime(100 + 250));
		expect(mux.resizes.slice(initialResizes)).toEqual([
			["handle-1", 100, 30],
			["handle-1", 120, 40],
			["handle-1", 120, 40],
		]);
	});

	it("marks exit in the terminal and refetches workspace state instead of writing status", () => {
		const { view, terminal, mux, invalidateSpy } = setup();
		act(() => mux.emitExit("handle-1"));
		expect(view.result.current.state).toBe("exited");
		expect(terminal.lines.some((line) => line.includes("[process exited]"))).toBe(true);
		expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: workspaceQueryKey });
	});

	it("surfaces pane errors and refetches, with no automatic retry", () => {
		const { view, mux, invalidateSpy } = setup();
		act(() => mux.emitError("handle-1", "no such pane"));
		expect(view.result.current.state).toBe("error");
		expect(view.result.current.error).toBe("no such pane");
		expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: workspaceQueryKey });
		act(() => mux.emitConnection("closed"));
		act(() => void vi.advanceTimersByTime(60_000));
		expect(mux.opens).toHaveLength(1);
	});

	it("reattaches on the same mux after a socket drop, clearing the stale screen", () => {
		const { view, terminal, mux } = setup();
		act(() => mux.emitOpened("handle-1"));
		act(() => mux.emitConnection("closed"));
		expect(view.result.current.state).toBe("reattaching");
		act(() => mux.emitConnection("open"));
		expect(terminal.clears).toBe(1); // the fresh zellij attach repaints over a blank grid
		expect(mux.opens).toEqual([
			["handle-1", 80, 24],
			["handle-1", 80, 24],
		]);
		act(() => mux.emitOpened("handle-1"));
		expect(view.result.current.state).toBe("attached");
	});

	it("does not replay user input typed while the mux is disconnected", () => {
		const { terminal, mux } = setup();
		act(() => mux.emitConnection("closed"));
		terminal.typeKeys("hidden\r");
		expect(mux.inputs).toEqual([]);
		act(() => mux.emitConnection("open"));
		expect(mux.inputs).toEqual([]);
	});

	it("detach closes the visible handle, stops reattach, and returns to idle", () => {
		const { view, mux, detach } = setup();
		act(() => detach());
		expect(view.result.current.state).toBe("idle");
		expect(mux.closes).toEqual(["handle-1"]);
		act(() => mux.emitConnection("closed"));
		act(() => void vi.advanceTimersByTime(60_000));
		expect(mux.opens).toHaveLength(1);
	});

	it("closes the old handle and opens the new handle on session switch", () => {
		const nextSession = { ...session, id: "sess-2", terminalHandleId: "handle-2" };
		const { view, terminal, mux, detach } = setup();
		act(() => mux.emitData("handle-1", "one"));
		expect(terminal.lines).toContain("one");

		view.rerender({ currentSession: nextSession });
		act(() => detach());
		act(() => {
			view.result.current.attach(terminal);
		});
		act(() => mux.emitData("handle-1", "late"));
		act(() => mux.emitData("handle-2", "two"));

		expect(mux.closes).toEqual(["handle-1"]);
		expect(mux.opens).toEqual([
			["handle-1", 80, 24],
			["handle-2", 80, 24],
		]);
		expect(terminal.lines).not.toContain("late");
		expect(terminal.lines).toContain("two");
	});
});
