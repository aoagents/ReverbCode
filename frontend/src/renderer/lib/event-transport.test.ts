import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const { onStatusMock, removeStatusMock, getApiBaseUrlMock } = vi.hoisted(() => ({
  onStatusMock: vi.fn(),
  removeStatusMock: vi.fn(),
  getApiBaseUrlMock: vi.fn(() => "http://127.0.0.1:3001"),
}));

vi.mock("./bridge", () => ({
  aoBridge: {
    daemon: { onStatus: onStatusMock },
  },
}));

vi.mock("./api-client", () => ({
  getApiBaseUrl: getApiBaseUrlMock,
}));

import { createEventTransport } from "./event-transport";

class EventSourceStub {
  static instances: EventSourceStub[] = [];
  url: string;
  closed = false;
  onmessage: (() => void) | null = null;
  listeners: string[] = [];
  constructor(url: string) {
    this.url = url;
    EventSourceStub.instances.push(this);
  }
  addEventListener(type: string) {
    this.listeners.push(type);
  }
  close() {
    this.closed = true;
  }
}

function fakeQueryClient() {
  return { invalidateQueries: vi.fn() } as unknown as Parameters<typeof createEventTransport>[0];
}

beforeEach(() => {
  EventSourceStub.instances = [];
  onStatusMock.mockReset().mockReturnValue(removeStatusMock);
  removeStatusMock.mockReset();
  getApiBaseUrlMock.mockReset().mockReturnValue("http://127.0.0.1:3001");
  (globalThis as unknown as { EventSource: unknown }).EventSource = EventSourceStub;
});

afterEach(() => {
  delete (globalThis as unknown as { EventSource?: unknown }).EventSource;
});

describe("createEventTransport", () => {
  it("opens a single SSE connection to the current base URL on connect", () => {
    createEventTransport(fakeQueryClient()).connect();

    expect(EventSourceStub.instances).toHaveLength(1);
    expect(EventSourceStub.instances[0].url).toBe("http://127.0.0.1:3001/api/v1/events");
    // All CDC event types plus onmessage are wired up.
    expect(EventSourceStub.instances[0].listeners).toContain("session_updated");
    expect(EventSourceStub.instances[0].onmessage).toBeTypeOf("function");
  });

  it("does not reconnect when a daemon status keeps the same base URL", () => {
    createEventTransport(fakeQueryClient()).connect();
    const onStatusHandler = onStatusMock.mock.calls[0][0] as () => void;

    onStatusHandler();

    expect(EventSourceStub.instances).toHaveLength(1);
  });

  it("closes the old connection and reconnects when the base URL changes", () => {
    createEventTransport(fakeQueryClient()).connect();
    const first = EventSourceStub.instances[0];
    const onStatusHandler = onStatusMock.mock.calls[0][0] as () => void;

    getApiBaseUrlMock.mockReturnValue("http://127.0.0.1:3099");
    onStatusHandler();

    expect(first.closed).toBe(true);
    expect(EventSourceStub.instances).toHaveLength(2);
    expect(EventSourceStub.instances[1].url).toBe("http://127.0.0.1:3099/api/v1/events");
  });

  it("debounces a workspace invalidation after a status change", () => {
    vi.useFakeTimers();
    try {
      const queryClient = fakeQueryClient();
      createEventTransport(queryClient).connect();
      const onStatusHandler = onStatusMock.mock.calls[0][0] as () => void;

      onStatusHandler();
      expect(queryClient.invalidateQueries).not.toHaveBeenCalled();
      vi.advanceTimersByTime(200);
      expect(queryClient.invalidateQueries).toHaveBeenCalledTimes(1);
    } finally {
      vi.useRealTimers();
    }
  });

  it("tears down the source and the daemon listener on disconnect", () => {
    const disconnect = createEventTransport(fakeQueryClient()).connect();

    disconnect();

    expect(EventSourceStub.instances[0].closed).toBe(true);
    expect(removeStatusMock).toHaveBeenCalledTimes(1);
  });

  it("is a no-op when EventSource is unavailable", () => {
    delete (globalThis as unknown as { EventSource?: unknown }).EventSource;

    expect(() => createEventTransport(fakeQueryClient()).connect()).not.toThrow();
    expect(EventSourceStub.instances).toHaveLength(0);
  });
});
