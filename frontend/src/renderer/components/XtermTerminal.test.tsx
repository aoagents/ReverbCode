import { render } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { XtermTerminal } from "./XtermTerminal";

const state = vi.hoisted(() => ({
	lastTerminal: null as null | {
		keyHandler?: (event: KeyboardEvent) => boolean;
		wheelHandler?: (event: WheelEvent) => boolean;
		selection: string;
		options: Record<string, unknown>;
		modes: { bracketedPasteMode: boolean };
		dataListeners: Set<(data: string) => void>;
		selectionListeners: Set<() => void>;
		_core: {
			element: { classList: { add: ReturnType<typeof vi.fn>; remove: ReturnType<typeof vi.fn> } };
			_selectionService: {
				enable: ReturnType<typeof vi.fn>;
				shouldForceSelection: (event: MouseEvent) => boolean;
			};
		};
	},
}));

vi.mock("@xterm/xterm", () => ({
	Terminal: class FakeTerminal {
		options: Record<string, unknown>;
		cols = 80;
		rows = 24;
		selection = "";
		keyHandler?: (event: KeyboardEvent) => boolean;
		wheelHandler?: (event: WheelEvent) => boolean;
		modes = { bracketedPasteMode: false };
		dataListeners = new Set<(data: string) => void>();
		selectionListeners = new Set<() => void>();
		_core = {
			element: { classList: { add: vi.fn(), remove: vi.fn() } },
			_selectionService: {
				enable: vi.fn(),
				shouldForceSelection: () => false,
			},
		};

		constructor(options: Record<string, unknown>) {
			this.options = options;
			state.lastTerminal = this;
		}

		loadAddon() {}
		open(host: HTMLElement) {
			host.appendChild(document.createElement("textarea"));
		}
		write() {}
		writeln() {}
		dispose() {}
		onData(listener: (data: string) => void) {
			this.dataListeners.add(listener);
			return { dispose: () => this.dataListeners.delete(listener) };
		}
		onResize() {
			return { dispose: () => undefined };
		}
		onRender() {
			return { dispose: () => undefined };
		}
		onKey() {
			return { dispose: () => undefined };
		}
		onSelectionChange(listener: () => void) {
			this.selectionListeners.add(listener);
			return { dispose: () => this.selectionListeners.delete(listener) };
		}
		hasSelection() {
			return this.selection.length > 0;
		}
		getSelection() {
			return this.selection;
		}
		attachCustomKeyEventHandler(listener: (event: KeyboardEvent) => boolean) {
			this.keyHandler = listener;
		}
		attachCustomWheelEventHandler(listener: (event: WheelEvent) => boolean) {
			this.wheelHandler = listener;
		}
		unicode = { activeVersion: "" };
	},
}));

vi.mock("@xterm/addon-fit", () => ({
	FitAddon: class FakeFitAddon {
		fit() {}
	},
}));

vi.mock("@xterm/addon-search", () => ({
	SearchAddon: class FakeSearchAddon {},
}));

vi.mock("@xterm/addon-unicode11", () => ({
	Unicode11Addon: class FakeUnicode11Addon {},
}));

vi.mock("@xterm/addon-web-links", () => ({
	WebLinksAddon: class FakeWebLinksAddon {},
}));

vi.mock("@xterm/addon-canvas", () => ({
	CanvasAddon: class FakeCanvasAddon {},
}));

vi.mock("@xterm/addon-webgl", () => ({
	WebglAddon: class FakeWebglAddon {
		onContextLoss() {}
		dispose() {}
	},
}));

describe("XtermTerminal", () => {
	beforeEach(() => {
		state.lastTerminal = null;
		window.ao!.clipboard.writeText = vi.fn().mockResolvedValue(undefined);
	});

	it("copies selected terminal text on the terminal copy shortcut", () => {
		render(<XtermTerminal theme="dark" />);
		state.lastTerminal!.selection = "copied selection";

		const event = {
			key: "c",
			metaKey: true,
			ctrlKey: false,
			shiftKey: false,
			preventDefault: vi.fn(),
		} as unknown as KeyboardEvent;
		const allowed = state.lastTerminal!.keyHandler!(event);

		expect(allowed).toBe(false);
		expect(event.preventDefault).toHaveBeenCalled();
		expect(window.ao!.clipboard.writeText).toHaveBeenCalledWith("copied selection");
	});

	it("handles native copy events from inside the terminal", () => {
		const { container } = render(<XtermTerminal theme="dark" />);
		state.lastTerminal!.selection = "native copied selection";
		const setData = vi.fn();
		const event = new Event("copy", { bubbles: true, cancelable: true }) as ClipboardEvent;
		Object.defineProperty(event, "clipboardData", {
			value: { setData },
		});

		container.firstElementChild!.dispatchEvent(event);

		expect(event.defaultPrevented).toBe(true);
		expect(setData).toHaveBeenCalledWith("text/plain", "native copied selection");
		expect(window.ao!.clipboard.writeText).toHaveBeenCalledWith("native copied selection");
	});

	it("copies from the focused xterm textarea when the window receives the copy shortcut", () => {
		const { container } = render(<XtermTerminal theme="dark" />);
		state.lastTerminal!.selection = "focused copied selection";
		container.querySelector("textarea")!.focus();

		const event = new KeyboardEvent("keydown", {
			bubbles: true,
			cancelable: true,
			key: "c",
			metaKey: true,
		});
		window.dispatchEvent(event);

		expect(event.defaultPrevented).toBe(true);
		expect(window.ao!.clipboard.writeText).toHaveBeenCalledWith("focused copied selection");
	});

	it("auto-copies new selections and retries explicit copy if the auto-copy failed", async () => {
		render(<XtermTerminal theme="dark" />);
		const writeText = vi.fn().mockRejectedValueOnce(new Error("clipboard failed")).mockResolvedValueOnce(undefined);
		window.ao!.clipboard.writeText = writeText;

		state.lastTerminal!.selection = "retry me";
		state.lastTerminal!.selectionListeners.forEach((listener) => listener());
		await new Promise((resolve) => window.setTimeout(resolve, 0));

		const event = {
			key: "c",
			metaKey: true,
			ctrlKey: false,
			shiftKey: false,
			preventDefault: vi.fn(),
		} as unknown as KeyboardEvent;
		const allowed = state.lastTerminal!.keyHandler!(event);

		expect(allowed).toBe(false);
		expect(writeText).toHaveBeenCalledTimes(2);
		expect(writeText).toHaveBeenLastCalledWith("retry me");
	});

	it("forwards generated xterm input data such as wheel scroll reports", () => {
		const onInput = vi.fn();
		render(<XtermTerminal theme="dark" onReady={(terminal) => terminal.onUserInput(onInput)} />);

		state.lastTerminal!.dataListeners.forEach((listener) => listener("\x1b[A"));

		expect(onInput).toHaveBeenCalledWith("\x1b[A", "terminal");
	});

	it("translates wheel motion into SGR wheel reports for zellij scrollback", () => {
		const onInput = vi.fn();
		render(<XtermTerminal theme="dark" onReady={(terminal) => terminal.onUserInput(onInput)} />);
		// rowHeight = fontSize(12) * lineHeight(1.35) = 16.2px; -50px => 3 lines up.
		const suppressed = state.lastTerminal!.wheelHandler!({ deltaY: -50 } as WheelEvent);

		expect(suppressed).toBe(false);
		expect(onInput).toHaveBeenCalledWith("\x1b[<64;1;1M\x1b[<64;1;1M\x1b[<64;1;1M", "terminal");
	});

	it("scrolls down on positive wheel delta and leaves zoom (ctrl/meta) wheel alone", () => {
		const onInput = vi.fn();
		render(<XtermTerminal theme="dark" onReady={(terminal) => terminal.onUserInput(onInput)} />);

		expect(state.lastTerminal!.wheelHandler!({ deltaY: 20 } as WheelEvent)).toBe(false);
		expect(onInput).toHaveBeenCalledWith("\x1b[<65;1;1M", "terminal");

		onInput.mockClear();
		expect(state.lastTerminal!.wheelHandler!({ deltaY: -50, ctrlKey: true } as WheelEvent)).toBe(false);
		expect(onInput).not.toHaveBeenCalled();
	});

	it("forces plain drag selection while preserving xterm data forwarding", () => {
		render(<XtermTerminal theme="dark" />);

		expect(state.lastTerminal!.options.macOptionClickForcesSelection).toBe(true);
		expect(state.lastTerminal!._core._selectionService.enable).toHaveBeenCalled();
		expect(state.lastTerminal!._core.element.classList.remove).toHaveBeenCalledWith("enable-mouse-events");
		expect(state.lastTerminal!._core._selectionService.shouldForceSelection({} as MouseEvent)).toBe(true);
	});
});
