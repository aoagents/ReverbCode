import { create } from "zustand";

export type ActivePane = "sessions" | "terminal";
export type Theme = "light" | "dark";

type UiState = {
  activePane: ActivePane;
  isSidebarOpen: boolean;
  selectedSessionId: string;
  selectedWorkspaceId: string;
  theme: Theme;
  setActivePane: (pane: ActivePane) => void;
  setSidebarOpen: (isSidebarOpen: boolean) => void;
  setSystemTheme: (theme: Theme) => void;
  toggleSidebar: () => void;
  selectWorkspace: (workspaceId: string) => void;
  selectSession: (sessionId: string) => void;
};

const sidebarStorageKey = "ao.sidebar.open";

function initialSidebarOpen() {
  if (typeof window === "undefined") return true;
  return window.localStorage.getItem(sidebarStorageKey) !== "false";
}

function initialTheme(): Theme {
  if (typeof window === "undefined") return "dark";

  return window.matchMedia("(prefers-color-scheme: light)").matches ? "light" : "dark";
}

export const useUiStore = create<UiState>((set) => ({
  activePane: "sessions",
  isSidebarOpen: initialSidebarOpen(),
  selectedSessionId: "ao-shell-scaffold",
  selectedWorkspaceId: "agent-orchestrator",
  theme: initialTheme(),
  setActivePane: (activePane) => set({ activePane }),
  setSidebarOpen: (isSidebarOpen) => {
    window.localStorage.setItem(sidebarStorageKey, String(isSidebarOpen));
    set({ isSidebarOpen });
  },
  setSystemTheme: (theme) => set({ theme }),
  toggleSidebar: () =>
    set((state) => {
      const isSidebarOpen = !state.isSidebarOpen;
      window.localStorage.setItem(sidebarStorageKey, String(isSidebarOpen));
      return { isSidebarOpen };
    }),
  selectWorkspace: (selectedWorkspaceId) => set({ selectedWorkspaceId, activePane: "terminal" }),
  selectSession: (selectedSessionId) => set({ selectedSessionId, activePane: "terminal" }),
}));
