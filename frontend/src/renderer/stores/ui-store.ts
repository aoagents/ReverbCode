import { create } from "zustand";
import type { Layout } from "react-resizable-panels";

export type ActivePane = "sessions" | "terminal";
export type Theme = "light" | "dark";

type UiState = {
  activePane: ActivePane;
  isSidebarOpen: boolean;
  selectedSessionId: string | null;
  selectedWorkspaceId: string | null;
  theme: Theme;
  /** Persisted resizable-panel layout (Panel id → flexGrow), undefined until first drag. */
  layout: Layout | undefined;
  setActivePane: (pane: ActivePane) => void;
  setSidebarOpen: (isSidebarOpen: boolean) => void;
  setSystemTheme: (theme: Theme) => void;
  setLayout: (layout: Layout) => void;
  toggleSidebar: () => void;
  selectWorkspace: (workspaceId: string) => void;
  selectSession: (sessionId: string) => void;
};

const sidebarStorageKey = "ao.sidebar.open";
const layoutStorageKey = "ao.layout";

function initialSidebarOpen() {
  if (typeof window === "undefined") return true;
  return window.localStorage.getItem(sidebarStorageKey) !== "false";
}

function initialLayout(): Layout | undefined {
  if (typeof window === "undefined") return undefined;
  const raw = window.localStorage.getItem(layoutStorageKey);
  if (!raw) return undefined;
  try {
    return JSON.parse(raw) as Layout;
  } catch {
    return undefined;
  }
}

function initialTheme(): Theme {
  if (typeof window === "undefined") return "dark";

  return window.matchMedia("(prefers-color-scheme: light)").matches ? "light" : "dark";
}

export const useUiStore = create<UiState>((set) => ({
  activePane: "sessions",
  isSidebarOpen: initialSidebarOpen(),
  selectedSessionId: null,
  selectedWorkspaceId: null,
  theme: initialTheme(),
  layout: initialLayout(),
  setActivePane: (activePane) => set({ activePane }),
  setLayout: (layout) => {
    window.localStorage.setItem(layoutStorageKey, JSON.stringify(layout));
    set({ layout });
  },
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
