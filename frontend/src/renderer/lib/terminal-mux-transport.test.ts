import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { MuxConnectionState, TerminalMux } from "./terminal-mux";
import { createTerminalMuxTransport } from "./terminal-mux-transport";

type FakeMux = TerminalMux & {
	url: string;
	opens: Array<[string, number, number]>;
	inputs: Array<[string, string]>;
	disposed: boolean;
	emitConnection(state: MuxConnectionState): void;
	emitData(id: string, bytes: Uint8Array): void;
};

function subscribe<T>(map: Map<string, Set<T>>, id: string, listener: T): () => void {
	const set = map.get(id) ?? new Set<T>();
	set.add(listener);
	map.set(id, set);
	return () => set.delete(listener);
}

function createFakeMux(url: string): FakeMux {
	const data = new Map<string, Set<(bytes: Uint8Array) => void>>();
	const connection = new Set<(state: MuxConnectionState) => void>();
	const fake: FakeMux = {
		url,
		opens: [],
		inputs: [],
		disposed: false,
		open: (id, cols, rows) => fake.opens.push([id, cols, rows]),
		sendInput: (id, input) => fake.inputs.push([id, input]),
		resize: () => undefined,
		close: () => undefined,
		onData: (id, listener) => subscribe(data, id, listener),
		onExit: () => () => undefined,
		onOpened: () => () => undefined,
		onError: () => () => undefined,
		onConnectionChange: (listener) => {
			connection.add(listener);
			return () => connection.delete(listener);
		},
		dispose: () => {
			fake.disposed = true;
		},
		emitConnection: (state) => connection.forEach((listener) => listener(state)),
		emitData: (id, bytes) => data.get(id)?.forEach((listener) => listener(bytes)),
	};
	return fake;
}

function setup({ daemonReady = true } = {}) {
	let baseUrl = "http://127.0.0.1:3001";
	let onBaseUrlChange: (() => void) | undefined;
	const muxes: FakeMux[] = [];
	const transport = createTerminalMuxTransport({
		daemonReady,
		retryBaseMs: 500,
		retryMaxMs: 8_000,
		getApiBaseUrl: () => baseUrl,
		subscribeApiBaseUrl: (listener) => {
			onBaseUrlChange = listener;
			return () => {
				onBaseUrlChange = undefined;
			};
		},
		createMux: (url) => {
			const mux = createFakeMux(url);
			muxes.push(mux);
			return mux;
		},
	});
	return {
		transport,
		muxes,
		setBaseUrl(next: string) {
			baseUrl = next;
			onBaseUrlChange?.();
		},
	};
}

beforeEach(() => {
	vi.useFakeTimers();
});

afterEach(() => {
	vi.useRealTimers();
});

describe("createTerminalMuxTransport", () => {
	it("waits for daemon readiness before opening /mux", () => {
		const { transport, muxes } = setup({ daemonReady: false });

		expect(muxes).toHaveLength(0);
		transport.setDaemonReady(true);

		expect(muxes).toHaveLength(1);
		expect(muxes[0].url).toBe("ws://127.0.0.1:3001/mux");
	});

	it("rebinds to the latest API base URL", () => {
		const { setBaseUrl, muxes } = setup();
		const first = muxes[0];

		setBaseUrl("http://127.0.0.1:4555");

		expect(first.disposed).toBe(true);
		expect(muxes).toHaveLength(2);
		expect(muxes[1].url).toBe("ws://127.0.0.1:4555/mux");
	});

	it("keeps listeners across socket replacement", () => {
		const { transport, muxes } = setup();
		const chunks: string[] = [];
		transport.onData("t1", (bytes) => chunks.push(new TextDecoder().decode(bytes)));

		muxes[0].emitConnection("open");
		muxes[0].emitData("t1", new TextEncoder().encode("one"));
		muxes[0].emitConnection("closed");
		vi.advanceTimersByTime(500);
		muxes[1].emitConnection("open");
		muxes[1].emitData("t1", new TextEncoder().encode("two"));

		expect(chunks).toEqual(["one", "two"]);
	});

	it("backs off between reconnect attempts while the daemon is ready", () => {
		const { muxes } = setup();

		muxes[0].emitConnection("closed");
		vi.advanceTimersByTime(499);
		expect(muxes).toHaveLength(1);
		vi.advanceTimersByTime(1);
		expect(muxes).toHaveLength(2);

		muxes[1].emitConnection("closed");
		vi.advanceTimersByTime(999);
		expect(muxes).toHaveLength(2);
		vi.advanceTimersByTime(1);
		expect(muxes).toHaveLength(3);
	});

	it("drops user input while disconnected instead of replaying it on reconnect", () => {
		const { transport, muxes } = setup();

		muxes[0].emitConnection("open");
		transport.sendInput("t1", "before");
		muxes[0].emitConnection("closed");
		transport.sendInput("t1", "during");
		vi.advanceTimersByTime(500);
		muxes[1].emitConnection("open");
		transport.sendInput("t1", "after");

		expect(muxes[0].inputs).toEqual([["t1", "before"]]);
		expect(muxes[1].inputs).toEqual([["t1", "after"]]);
	});
});
