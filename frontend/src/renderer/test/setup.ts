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

const localStorageStub = (() => {
	const values = new Map<string, string>();
	return {
		clear: () => values.clear(),
		getItem: (key: string) => values.get(key) ?? null,
		removeItem: (key: string) => values.delete(key),
		setItem: (key: string, value: string) => values.set(key, value),
	};
})();

Object.defineProperty(window, "localStorage", {
	configurable: true,
	writable: true,
	value: localStorageStub,
});

HTMLCanvasElement.prototype.getContext = (() => ({})) as unknown as typeof HTMLCanvasElement.prototype.getContext;

Element.prototype.hasPointerCapture = (() => false) as typeof Element.prototype.hasPointerCapture;
Element.prototype.setPointerCapture = (() => undefined) as typeof Element.prototype.setPointerCapture;
Element.prototype.releasePointerCapture = (() => undefined) as typeof Element.prototype.releasePointerCapture;
Element.prototype.scrollIntoView = (() => undefined) as typeof Element.prototype.scrollIntoView;

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
