import type { ReactNode, Ref } from "react";
import { act, fireEvent, render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi, type Mock } from "vitest";
import { SessionView } from "./SessionView";
import { useUiStore } from "../stores/ui-store";
import type { WorkspaceSession, WorkspaceSummary } from "../types/workspace";

type FakePanelHandle = {
	collapse: Mock;
	expand: Mock;
	getSize: Mock;
	isCollapsed: Mock;
	resize: Mock;
};

type PanelEntry = {
	handle: FakePanelHandle;
	onResize?: (size: { asPercentage: number; inPixels: number }) => void;
};

const { workspaces, panels } = vi.hoisted(() => {
	const worker = {
		id: "sess-1",
		workspaceId: "proj-1",
		workspaceName: "my-app",
		title: "do the thing",
		provider: "claude-code",
		kind: "worker",
		branch: "ao/sess-1",
		status: "working",
		updatedAt: "2026-06-10T00:00:00Z",
	} satisfies WorkspaceSession;
	const orchestrator = {
		...worker,
		id: "sess-orch",
		kind: "orchestrator",
		title: "orchestrate",
	} satisfies WorkspaceSession;
	const workspaces: WorkspaceSummary[] = [
		{ id: "proj-1", name: "my-app", path: "/p", type: "main", sessions: [worker, orchestrator] },
	];
	return { workspaces, panels: new Map<string, PanelEntry>() };
});

// The terminal, inspector body, and topbar pull in xterm/router/SSE machinery
// irrelevant to the split under test.
vi.mock("./CenterPane", () => ({ CenterPane: () => <div /> }));
vi.mock("./SessionInspector", () => ({ SessionInspector: () => <div /> }));
vi.mock("./Topbar", () => ({ Topbar: () => <header /> }));
vi.mock("@tanstack/react-router", () => ({ useNavigate: () => vi.fn() }));
vi.mock("../lib/shell-context", () => ({
	useShell: () => ({ daemonStatus: { state: "ready" } }),
}));
vi.mock("../hooks/useWorkspaceQuery", () => ({
	useWorkspaceQuery: () => ({ data: workspaces, isLoading: false }),
}));

// jsdom has no layout engine, so the real react-resizable-panels would never
// produce meaningful sizes — record the props SessionView passes and expose a
// fake imperative handle per panel instead.
vi.mock("./ui/resizable", () => ({
	ResizablePanelGroup: ({ children }: { children?: ReactNode }) => <div>{children}</div>,
	ResizableHandle: () => <div data-testid="resize-handle" />,
	ResizablePanel: ({
		children,
		id,
		defaultSize,
		minSize,
		maxSize,
		collapsible,
		panelRef,
		onResize,
		style: _style,
		...rest
	}: {
		children?: ReactNode;
		id: string;
		defaultSize?: number | string;
		minSize?: number | string;
		maxSize?: number | string;
		collapsible?: boolean;
		panelRef?: Ref<FakePanelHandle | null>;
		onResize?: (size: { asPercentage: number; inPixels: number }) => void;
		style?: React.CSSProperties;
	}) => {
		let entry = panels.get(id);
		if (!entry) {
			entry = {
				handle: {
					collapse: vi.fn(),
					expand: vi.fn(),
					getSize: vi.fn(() => ({ asPercentage: 28, inPixels: 280 })),
					isCollapsed: vi.fn(() => false),
					resize: vi.fn(),
				},
			};
			panels.set(id, entry);
		}
		entry.onResize = onResize;
		if (panelRef && typeof panelRef === "object") {
			(panelRef as { current: FakePanelHandle | null }).current = entry.handle;
		}
		return (
			<div data-testid={`panel-${id}`} data-collapsible={collapsible ? "true" : undefined} {...rest}>
				<span data-testid={`panel-${id}-sizes`}>
					{JSON.stringify([defaultSize, minSize, maxSize].filter((s) => s !== undefined))}
				</span>
				{children}
			</div>
		);
	},
}));

function panelSizes(id: string): unknown[] {
	return JSON.parse(screen.getByTestId(`panel-${id}-sizes`).textContent ?? "[]") as unknown[];
}

describe("SessionView", () => {
	beforeEach(() => {
		window.localStorage.clear();
		useUiStore.setState({ isInspectorOpen: true });
		panels.clear();
	});

	// Regression: react-resizable-panels v4 treats bare numeric sizes as PIXELS
	// (numbers were percentages in the older API the shadcn examples use).
	// defaultSize={28}/maxSize={45} clamped the inspector rail to a 45px sliver.
	// Every size must be an explicit percentage string.
	it("sizes the terminal/inspector split in percentages, not pixels", () => {
		render(<SessionView sessionId="sess-1" />);

		for (const panelId of ["terminal", "inspector"]) {
			const sizes = panelSizes(panelId);
			expect(sizes.length).toBeGreaterThan(0);
			for (const size of sizes) {
				expect(size, `${panelId} size ${String(size)} must be a percentage string`).toMatch(/^\d+(\.\d+)?%$/);
			}
		}
	});

	it("marks the inspector collapsible and renders the resize handle", () => {
		render(<SessionView sessionId="sess-1" />);

		expect(screen.getByTestId("panel-inspector")).toHaveAttribute("data-collapsible", "true");
		expect(screen.getByTestId("resize-handle")).toBeInTheDocument();
		expect(screen.getByTestId("panel-inspector")).not.toHaveAttribute("inert");
	});

	it("mounts collapsed and inert when the store says closed", () => {
		useUiStore.setState({ isInspectorOpen: false });
		render(<SessionView sessionId="sess-1" />);

		expect(panelSizes("inspector")[0]).toBe("0%");
		const pane = screen.getByTestId("panel-inspector");
		expect(pane).toHaveAttribute("inert");
		expect(pane).toHaveAttribute("aria-hidden", "true");
		expect(panels.get("inspector")!.handle.collapse).toHaveBeenCalled();
	});

	it("toggles the inspector with mod+shift+B through the imperative panel API", () => {
		render(<SessionView sessionId="sess-1" />);
		const handle = panels.get("inspector")!.handle;

		fireEvent.keyDown(window, { key: "B", metaKey: true, shiftKey: true });
		expect(useUiStore.getState().isInspectorOpen).toBe(false);
		expect(handle.collapse).toHaveBeenCalledTimes(1);

		fireEvent.keyDown(window, { key: "B", ctrlKey: true, shiftKey: true });
		expect(useUiStore.getState().isInspectorOpen).toBe(true);
		expect(handle.expand).toHaveBeenCalled();

		// Plain ⌘B belongs to the sidebar — the inspector must not react.
		fireEvent.keyDown(window, { key: "b", metaKey: true });
		expect(useUiStore.getState().isInspectorOpen).toBe(true);
	});

	it("syncs drag resizes back into the store and persists the split", () => {
		render(<SessionView sessionId="sess-1" />);
		const entry = panels.get("inspector")!;

		// Dragging past minSize collapses the panel → store follows.
		act(() => entry.onResize?.({ asPercentage: 0, inPixels: 0 }));
		expect(useUiStore.getState().isInspectorOpen).toBe(false);

		// Dragging it back open reopens + persists the width.
		act(() => entry.onResize?.({ asPercentage: 31.5, inPixels: 400 }));
		expect(useUiStore.getState().isInspectorOpen).toBe(true);
		expect(window.localStorage.getItem("ao.inspector.split")).toBe("31.5");
	});

	it("restores the persisted split width", () => {
		window.localStorage.setItem("ao.inspector.split", "40");
		render(<SessionView sessionId="sess-1" />);
		expect(panelSizes("inspector")[0]).toBe("40%");
	});

	it("renders no inspector panel or handle for orchestrator sessions", () => {
		render(<SessionView sessionId="sess-orch" />);

		expect(screen.queryByTestId("panel-inspector")).not.toBeInTheDocument();
		expect(screen.queryByTestId("resize-handle")).not.toBeInTheDocument();

		// The shortcut is inactive without an inspector.
		fireEvent.keyDown(window, { key: "B", metaKey: true, shiftKey: true });
		expect(useUiStore.getState().isInspectorOpen).toBe(true);
	});
});
