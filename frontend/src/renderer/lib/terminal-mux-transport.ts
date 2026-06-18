import { getApiBaseUrl, subscribeApiBaseUrl } from "./api-client";
import { createTerminalMux, muxUrlFromApiBase, type MuxConnectionState, type TerminalMux } from "./terminal-mux";

type DataListener = (bytes: Uint8Array) => void;
type ExitListener = () => void;
type OpenedListener = () => void;
type ErrorListener = (message: string) => void;
type ConnectionListener = (state: MuxConnectionState) => void;

export type TerminalMuxTransport = TerminalMux & {
	setDaemonReady: (ready: boolean) => void;
};

export type TerminalMuxTransportOptions = {
	daemonReady?: boolean;
	createMux?: (url: string) => TerminalMux;
	getApiBaseUrl?: () => string;
	subscribeApiBaseUrl?: (listener: () => void) => () => void;
	retryBaseMs?: number;
	retryMaxMs?: number;
};

const RETRY_BASE_MS = 500;
const RETRY_MAX_MS = 8_000;

function subscribeById<T>(map: Map<string, Set<T>>, id: string, listener: T): () => void {
	const set = map.get(id) ?? new Set<T>();
	set.add(listener);
	map.set(id, set);
	return () => {
		set.delete(listener);
		if (set.size === 0) map.delete(id);
	};
}

/**
 * Shell-lifetime wrapper around the single-socket mux client. It owns socket
 * replacement and keeps pane listeners registered across reconnects; visible
 * terminal attachments still decide which handle is opened on each live socket.
 */
export function createTerminalMuxTransport(options: TerminalMuxTransportOptions = {}): TerminalMuxTransport {
	const buildMux = options.createMux ?? ((url: string) => createTerminalMux(url));
	const readApiBaseUrl = options.getApiBaseUrl ?? getApiBaseUrl;
	const subscribeBaseUrl = options.subscribeApiBaseUrl ?? subscribeApiBaseUrl;
	const retryBaseMs = options.retryBaseMs ?? RETRY_BASE_MS;
	const retryMaxMs = options.retryMaxMs ?? RETRY_MAX_MS;

	const dataListeners = new Map<string, Set<DataListener>>();
	const exitListeners = new Map<string, Set<ExitListener>>();
	const openedListeners = new Map<string, Set<OpenedListener>>();
	const errorListeners = new Map<string, Set<ErrorListener>>();
	const connectionListeners = new Set<ConnectionListener>();

	let mux: TerminalMux | null = null;
	let muxDisposers: Array<() => void> = [];
	let socketBindings = new Set<string>();
	let state: MuxConnectionState = "closed";
	let daemonReady = options.daemonReady ?? false;
	let disposed = false;
	let retryTimer: ReturnType<typeof setTimeout> | null = null;
	let attempts = 0;
	let currentBaseUrl: string | null = null;

	const setState = (next: MuxConnectionState, options: { force?: boolean } = {}) => {
		if (disposed || (!options.force && state === next)) return;
		state = next;
		connectionListeners.forEach((listener) => listener(next));
	};

	const clearRetry = () => {
		if (retryTimer) {
			clearTimeout(retryTimer);
			retryTimer = null;
		}
	};

	const disposeMux = () => {
		muxDisposers.forEach((dispose) => dispose());
		muxDisposers = [];
		socketBindings = new Set<string>();
		mux?.dispose();
		mux = null;
	};

	const ensureDataBinding = (id: string) => {
		if (!mux) return;
		const key = `data:${id}`;
		if (socketBindings.has(key)) return;
		socketBindings.add(key);
		muxDisposers.push(mux.onData(id, (bytes) => dataListeners.get(id)?.forEach((listener) => listener(bytes))));
	};

	const ensureExitBinding = (id: string) => {
		if (!mux) return;
		const key = `exit:${id}`;
		if (socketBindings.has(key)) return;
		socketBindings.add(key);
		muxDisposers.push(mux.onExit(id, () => exitListeners.get(id)?.forEach((listener) => listener())));
	};

	const ensureOpenedBinding = (id: string) => {
		if (!mux) return;
		const key = `opened:${id}`;
		if (socketBindings.has(key)) return;
		socketBindings.add(key);
		muxDisposers.push(mux.onOpened(id, () => openedListeners.get(id)?.forEach((listener) => listener())));
	};

	const ensureErrorBinding = (id: string) => {
		if (!mux) return;
		const key = `error:${id}`;
		if (socketBindings.has(key)) return;
		socketBindings.add(key);
		muxDisposers.push(mux.onError(id, (message) => errorListeners.get(id)?.forEach((listener) => listener(message))));
	};

	const bindSocketListeners = (nextMux: TerminalMux) => {
		mux = nextMux;
		for (const id of dataListeners.keys()) {
			ensureDataBinding(id);
		}
		for (const id of exitListeners.keys()) {
			ensureExitBinding(id);
		}
		for (const id of openedListeners.keys()) {
			ensureOpenedBinding(id);
		}
		for (const id of errorListeners.keys()) {
			ensureErrorBinding(id);
		}
		muxDisposers.push(
			nextMux.onConnectionChange((nextState) => {
				if (nextState === "open") {
					attempts = 0;
					clearRetry();
					setState("open");
				} else {
					disposeMux();
					setState("closed", { force: true });
					scheduleReconnect();
				}
			}),
		);
	};

	const connect = () => {
		if (disposed || !daemonReady || mux) return;
		currentBaseUrl = readApiBaseUrl();
		const nextMux = buildMux(muxUrlFromApiBase(currentBaseUrl));
		bindSocketListeners(nextMux);
	};

	const scheduleReconnect = () => {
		if (disposed || !daemonReady || retryTimer) return;
		const delay = Math.min(retryBaseMs * 2 ** attempts, retryMaxMs);
		attempts += 1;
		retryTimer = setTimeout(() => {
			retryTimer = null;
			connect();
		}, delay);
	};

	const rebindBaseUrl = () => {
		if (disposed) return;
		const nextBaseUrl = readApiBaseUrl();
		if (nextBaseUrl === currentBaseUrl && mux) return;
		clearRetry();
		disposeMux();
		setState("closed");
		currentBaseUrl = nextBaseUrl;
		attempts = 0;
		connect();
	};

	const removeBaseUrlListener = subscribeBaseUrl(rebindBaseUrl);
	if (daemonReady) connect();

	const sendWhenOpen = (send: (activeMux: TerminalMux) => void) => {
		if (state !== "open" || !mux) return;
		send(mux);
	};

	return {
		open: (id, cols, rows) => sendWhenOpen((activeMux) => activeMux.open(id, cols, rows)),
		sendInput: (id, input) => sendWhenOpen((activeMux) => activeMux.sendInput(id, input)),
		resize: (id, cols, rows) => sendWhenOpen((activeMux) => activeMux.resize(id, cols, rows)),
		close: (id) => sendWhenOpen((activeMux) => activeMux.close(id)),
		onData: (id, listener) => {
			const unsubscribe = subscribeById(dataListeners, id, listener);
			ensureDataBinding(id);
			return unsubscribe;
		},
		onExit: (id, listener) => {
			const unsubscribe = subscribeById(exitListeners, id, listener);
			ensureExitBinding(id);
			return unsubscribe;
		},
		onOpened: (id, listener) => {
			const unsubscribe = subscribeById(openedListeners, id, listener);
			ensureOpenedBinding(id);
			return unsubscribe;
		},
		onError: (id, listener) => {
			const unsubscribe = subscribeById(errorListeners, id, listener);
			ensureErrorBinding(id);
			return unsubscribe;
		},
		onConnectionChange: (listener) => {
			connectionListeners.add(listener);
			return () => connectionListeners.delete(listener);
		},
		setDaemonReady: (ready) => {
			if (daemonReady === ready) return;
			daemonReady = ready;
			clearRetry();
			if (!daemonReady) {
				disposeMux();
				setState("closed");
				return;
			}
			attempts = 0;
			connect();
		},
		dispose: () => {
			if (disposed) return;
			disposed = true;
			clearRetry();
			removeBaseUrlListener();
			disposeMux();
			connectionListeners.clear();
			dataListeners.clear();
			exitListeners.clear();
			openedListeners.clear();
			errorListeners.clear();
		},
	};
}
