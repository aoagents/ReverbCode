import "@testing-library/jest-dom/vitest";

class ResizeObserverStub {
  observe() {}
  unobserve() {}
  disconnect() {}
}

Object.defineProperty(window, "ResizeObserver", {
  configurable: true,
  writable: true,
  value: ResizeObserverStub,
});

Object.defineProperty(window, "matchMedia", {
  configurable: true,
  writable: true,
  value: (query: string) => ({
    matches: false,
    media: query,
    onchange: null,
    addEventListener: () => undefined,
    removeEventListener: () => undefined,
    addListener: () => undefined,
    removeListener: () => undefined,
    dispatchEvent: () => false,
  }),
});

HTMLCanvasElement.prototype.getContext = (() => ({})) as unknown as typeof HTMLCanvasElement.prototype.getContext;

window.ao = {
  app: {
    getVersion: async () => "0.0.0-test",
    chooseDirectory: async () => null,
  },
  daemon: {
    getStatus: async () => ({ state: "stopped" }),
    start: async () => ({ state: "starting" }),
    stop: async () => ({ state: "stopped" }),
    onStatus: () => () => undefined,
  },
};
