import { create } from "zustand";

export type Theme = "light" | "dark";
/** Whether a terminal pane shows the orchestrator or a worker session. */
export type WorkbenchView = "orchestrator" | "session";
/** Worker detail view toggles — Changes (Git rail) is the default. */
export type WorkbenchTab = "changes" | "files" | "terminal";

// Selection (which project/session is open) now lives in the URL — the router
// is the single source of truth, read via route params. This store holds only
// ephemeral, route-independent UI: theme, sidebar collapse, and the active
// workbench tab within a session.
type UiState = {
  workbenchTab: WorkbenchTab;
  isSidebarOpen: boolean;
  theme: Theme;
  setWorkbenchTab: (tab: WorkbenchTab) => void;
  setSystemTheme: (theme: Theme) => void;
  toggleSidebar: () => void;
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
  workbenchTab: "changes",
  isSidebarOpen: initialSidebarOpen(),
  theme: initialTheme(),
  setWorkbenchTab: (workbenchTab) => set({ workbenchTab }),
  setSystemTheme: (theme) => set({ theme }),
  toggleSidebar: () =>
    set((state) => {
      const isSidebarOpen = !state.isSidebarOpen;
      getLocalStorage()?.setItem(sidebarStorageKey, String(isSidebarOpen));
      return { isSidebarOpen };
    }),
}));
