import { create } from "zustand";

export type Theme = "light" | "dark";
/** Orchestrator-led: the app lands on the orchestrator; a worker row drills in. */
export type WorkbenchView = "orchestrator" | "session";
/** Worker topbar view toggles — Changes (Git rail) is the default. */
export type WorkbenchTab = "changes" | "files" | "terminal";

type UiState = {
	view: WorkbenchView;
	workbenchTab: WorkbenchTab;
	isSidebarOpen: boolean;
	selectedSessionId: string | null;
	selectedWorkspaceId: string | null;
	theme: Theme;
	setWorkbenchTab: (tab: WorkbenchTab) => void;
	setSystemTheme: (theme: Theme) => void;
	toggleSidebar: () => void;
	selectOrchestrator: () => void;
	selectWorkspace: (workspaceId: string) => void;
	selectSession: (sessionId: string, workspaceId?: string) => void;
};

const sidebarStorageKey = "ao.sidebar.open";

function getLocalStorage() {
	if (typeof window === "undefined" || !window.localStorage) return null;
	return window.localStorage;
}

function initialSidebarOpen() {
	return getLocalStorage()?.getItem(sidebarStorageKey) !== "false";
}

function initialTheme(): Theme {
	if (typeof window === "undefined") return "dark";

	return window.matchMedia("(prefers-color-scheme: light)").matches ? "light" : "dark";
}

export const useUiStore = create<UiState>((set) => ({
	view: "orchestrator",
	workbenchTab: "changes",
	isSidebarOpen: initialSidebarOpen(),
	selectedSessionId: null,
	selectedWorkspaceId: null,
	theme: initialTheme(),
	setWorkbenchTab: (workbenchTab) => set({ workbenchTab }),
	setSystemTheme: (theme) => set({ theme }),
	toggleSidebar: () =>
		set((state) => {
			const isSidebarOpen = !state.isSidebarOpen;
			getLocalStorage()?.setItem(sidebarStorageKey, String(isSidebarOpen));
			return { isSidebarOpen };
		}),
	selectOrchestrator: () => set({ view: "orchestrator" }),
	selectWorkspace: (selectedWorkspaceId) => set({ selectedWorkspaceId }),
	selectSession: (selectedSessionId, workspaceId) =>
		set((state) => ({
			selectedSessionId,
			selectedWorkspaceId: workspaceId ?? state.selectedWorkspaceId,
			view: "session",
			workbenchTab: "changes",
		})),
}));
