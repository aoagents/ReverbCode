import { describe, expect, it } from "vitest";
import {
  base64ToBytes,
  bytesToBase64,
  closeFrame,
  createTerminalMux,
  dataFrame,
  muxUrlFromApiBase,
  openFrame,
  resizeFrame,
} from "./terminal-mux";

describe("terminal-mux framing", () => {
  it("round-trips arbitrary (non-UTF8) bytes through base64", () => {
    const bytes = new Uint8Array([0x00, 0x1b, 0x5b, 0xff, 0xfe, 0x7f, 0x41]);
    expect(base64ToBytes(bytesToBase64(bytes))).toEqual(bytes);
  });

  it("encodes an open frame the backend expects", () => {
    expect(JSON.parse(openFrame("sess-1", 80, 24))).toEqual({
      ch: "terminal",
      type: "open",
      id: "sess-1",
      cols: 80,
      rows: 24,
    });
  });

  it("base64-encodes input bytes in a data frame", () => {
    const frame = JSON.parse(dataFrame("sess-1", new TextEncoder().encode("ls\n")));
    expect(frame).toMatchObject({ ch: "terminal", type: "data", id: "sess-1" });
    expect(new TextDecoder().decode(base64ToBytes(frame.data))).toBe("ls\n");
  });

  it("carries cols/rows on resize and id on close", () => {
    expect(JSON.parse(resizeFrame("s", 120, 40))).toMatchObject({ type: "resize", cols: 120, rows: 40 });
    expect(JSON.parse(closeFrame("s"))).toEqual({ ch: "terminal", type: "close", id: "s" });
  });

  it("derives the ws mux url from the http api base (root path, not /api/v1)", () => {
    expect(muxUrlFromApiBase("http://127.0.0.1:4317")).toBe("ws://127.0.0.1:4317/mux");
    expect(muxUrlFromApiBase("https://host:8443/")).toBe("wss://host:8443/mux");
  });
});

// Minimal fake socket so we can assert client behaviour without a live daemon.
class FakeSocket {
  static OPEN = 1;
  static instances: FakeSocket[] = [];
  readyState = 0;
  sent: string[] = [];
  closed = false;
  private listeners: Record<string, ((ev: unknown) => void)[]> = {};
  constructor(public url: string) {
    FakeSocket.instances.push(this);
  }
  addEventListener(type: string, cb: (ev: unknown) => void) {
    (this.listeners[type] ??= []).push(cb);
  }
  send(frame: string) {
    this.sent.push(frame);
  }
  close() {
    this.closed = true;
  }
  emitOpen() {
    this.readyState = FakeSocket.OPEN;
    this.listeners.open?.forEach((cb) => cb({}));
  }
  emitMessage(data: string) {
    this.listeners.message?.forEach((cb) => cb({ data }));
  }
}

describe("createTerminalMux client", () => {
  it("queues frames until open, then flushes them in order", () => {
    const mux = createTerminalMux("ws://x/mux", FakeSocket as unknown as typeof WebSocket);
    const socket = FakeSocket.instances.at(-1)!;
    mux.open("s", 80, 24);
    expect(socket.sent).toHaveLength(0); // not open yet → queued
    socket.emitOpen();
    expect(JSON.parse(socket.sent[0])).toMatchObject({ type: "open", id: "s" });
  });

  it("routes server data frames to the matching id listener as bytes", () => {
    const mux = createTerminalMux("ws://x/mux", FakeSocket as unknown as typeof WebSocket);
    const socket = FakeSocket.instances.at(-1)!;
    socket.emitOpen();
    const chunks: string[] = [];
    mux.onData("s", (bytes) => chunks.push(new TextDecoder().decode(bytes)));
    socket.emitMessage(JSON.stringify({ ch: "terminal", id: "s", type: "data", data: bytesToBase64(new TextEncoder().encode("hi")) }));
    socket.emitMessage(JSON.stringify({ ch: "terminal", id: "other", type: "data", data: bytesToBase64(new TextEncoder().encode("nope")) }));
    expect(chunks).toEqual(["hi"]);
  });

  it("fires the exit listener on an exited frame", () => {
    const mux = createTerminalMux("ws://x/mux", FakeSocket as unknown as typeof WebSocket);
    const socket = FakeSocket.instances.at(-1)!;
    socket.emitOpen();
    let exited = false;
    mux.onExit("s", () => {
      exited = true;
    });
    socket.emitMessage(JSON.stringify({ ch: "terminal", id: "s", type: "exited" }));
    expect(exited).toBe(true);
  });
});
