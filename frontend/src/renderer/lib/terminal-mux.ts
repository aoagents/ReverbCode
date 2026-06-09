// WebSocket client for the daemon's terminal multiplexer (`/mux`).
//
// The wire protocol mirrors backend/internal/terminal/protocol.go: a single
// JSON-framed socket tagged by channel ("ch"). Terminal payloads carry the PTY
// bytes base64-encoded in `data` because PTY output is arbitrary bytes that a
// raw JSON string cannot represent.
//
//   ch "terminal" — per-pane byte stream keyed by an opaque runtime handle id
//     client → open{id,cols,rows} | data{id,data} | resize{id,cols,rows} | close{id}
//     server → opened{id} | data{id,data} | exited{id} | error{id?,error}
//   ch "system"   — ping/pong liveness
//
// The renderer connects directly to the loopback daemon (same host/port as the
// REST API, path `/mux`); it is not proxied through the Electron main process.

type ServerFrame = {
  ch: string;
  id?: string;
  type: string;
  data?: string;
  error?: string;
  session?: { seq: number; projectId: string; sessionId?: string; eventType: string };
};

// ---- pure framing helpers (unit-tested in terminal-mux.test.ts) ----

export function bytesToBase64(bytes: Uint8Array): string {
  let binary = "";
  const chunk = 0x8000;
  for (let i = 0; i < bytes.length; i += chunk) {
    binary += String.fromCharCode(...bytes.subarray(i, i + chunk));
  }
  return btoa(binary);
}

export function base64ToBytes(b64: string): Uint8Array {
  const binary = atob(b64);
  const bytes = new Uint8Array(binary.length);
  for (let i = 0; i < binary.length; i += 1) {
    bytes[i] = binary.charCodeAt(i);
  }
  return bytes;
}

export function openFrame(id: string, cols: number, rows: number): string {
  return JSON.stringify({ ch: "terminal", type: "open", id, cols, rows });
}

export function dataFrame(id: string, bytes: Uint8Array): string {
  return JSON.stringify({ ch: "terminal", type: "data", id, data: bytesToBase64(bytes) });
}

export function resizeFrame(id: string, cols: number, rows: number): string {
  return JSON.stringify({ ch: "terminal", type: "resize", id, cols, rows });
}

export function closeFrame(id: string): string {
  return JSON.stringify({ ch: "terminal", type: "close", id });
}

function pingFrame(): string {
  return JSON.stringify({ ch: "system", type: "ping" });
}

// Derive the ws(s)://.../mux URL from the REST API base. The mux is mounted at
// the router root (backend router.go), not under /api/v1.
export function muxUrlFromApiBase(apiBaseUrl: string): string {
  // http://host → ws://host and https://host → wss://host (the trailing "s" is left
  // in place by the anchored replace). apiBaseUrl is the host root (e.g.
  // http://127.0.0.1:4317); strip any trailing slash before appending /mux.
  const ws = apiBaseUrl.replace(/^http/i, "ws");
  return `${ws.replace(/\/+$/, "")}/mux`;
}

type DataListener = (bytes: Uint8Array) => void;
type ExitListener = () => void;

export type TerminalMux = {
  /** Open a PTY pane for the given runtime/session id at an initial size. */
  open: (id: string, cols: number, rows: number) => void;
  /** Forward a keystroke string (xterm `onData`) to the pane. */
  sendInput: (id: string, input: string) => void;
  resize: (id: string, cols: number, rows: number) => void;
  close: (id: string) => void;
  onData: (id: string, listener: DataListener) => () => void;
  onExit: (id: string, listener: ExitListener) => () => void;
  /** Close the socket and drop all listeners. */
  dispose: () => void;
};

const PING_INTERVAL_MS = 20_000;

/**
 * Create a mux client over a single WebSocket. Frames sent before the socket is
 * OPEN are queued and flushed on connect. There is no auto-reconnect: the
 * TerminalPane creates one client per mounted session and disposes it on
 * unmount, so a dropped socket surfaces as a closed pane and the next mount
 * reconnects.
 */
export function createTerminalMux(url: string, WebSocketImpl: typeof WebSocket = WebSocket): TerminalMux {
  const socket = new WebSocketImpl(url);
  const encoder = new TextEncoder();
  const queue: string[] = [];
  const dataListeners = new Map<string, Set<DataListener>>();
  const exitListeners = new Map<string, Set<ExitListener>>();
  let pingTimer: ReturnType<typeof setInterval> | undefined;
  let disposed = false;

  const flush = () => {
    while (queue.length > 0) {
      const frame = queue.shift();
      if (frame !== undefined) socket.send(frame);
    }
  };

  const send = (frame: string) => {
    if (disposed) return;
    if (socket.readyState === WebSocketImpl.OPEN) {
      socket.send(frame);
    } else {
      queue.push(frame);
    }
  };

  socket.addEventListener("open", () => {
    if (disposed) return;
    flush();
    pingTimer = setInterval(() => send(pingFrame()), PING_INTERVAL_MS);
  });

  socket.addEventListener("message", (event: MessageEvent) => {
    if (typeof event.data !== "string") return;
    let frame: ServerFrame;
    try {
      frame = JSON.parse(event.data) as ServerFrame;
    } catch {
      return;
    }
    if (frame.ch !== "terminal" || frame.id === undefined) return;
    if (frame.type === "data" && frame.data) {
      dataListeners.get(frame.id)?.forEach((listener) => listener(base64ToBytes(frame.data as string)));
    } else if (frame.type === "exited") {
      exitListeners.get(frame.id)?.forEach((listener) => listener());
    }
  });

  const dispose = () => {
    if (disposed) return;
    disposed = true;
    if (pingTimer) clearInterval(pingTimer);
    dataListeners.clear();
    exitListeners.clear();
    try {
      socket.close();
    } catch {
      // socket may already be closing; ignore.
    }
  };

  return {
    open: (id, cols, rows) => send(openFrame(id, cols, rows)),
    sendInput: (id, input) => send(dataFrame(id, encoder.encode(input))),
    resize: (id, cols, rows) => send(resizeFrame(id, cols, rows)),
    close: (id) => send(closeFrame(id)),
    onData: (id, listener) => {
      const set = dataListeners.get(id) ?? new Set();
      set.add(listener);
      dataListeners.set(id, set);
      return () => set.delete(listener);
    },
    onExit: (id, listener) => {
      const set = exitListeners.get(id) ?? new Set();
      set.add(listener);
      exitListeners.set(id, set);
      return () => set.delete(listener);
    },
    dispose,
  };
}
