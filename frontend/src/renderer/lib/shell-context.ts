import { createContext, useContext } from "react";
import type { useDaemonStatus } from "../hooks/useDaemonStatus";
import type { AgentProvider } from "../types/workspace";

// Shared state the persistent _shell layout owns and route content reads. The
// daemon status effect (IPC poll + event transport) must run exactly once, so
// it lives in the shell and is handed down here rather than re-run per route.
export type ShellContextValue = {
	daemonStatus: ReturnType<typeof useDaemonStatus>;
	/** Open the spawn-worker modal, optionally pre-selecting a project. */
	openSpawn: (projectId?: string) => void;
	createProject: (input: { path: string }) => Promise<void>;
	createTask: (input: { projectId: string; prompt: string; branch?: string; harness?: AgentProvider }) => Promise<void>;
};

const ShellContext = createContext<ShellContextValue | null>(null);

export const ShellProvider = ShellContext.Provider;

export function useShell(): ShellContextValue {
	const ctx = useContext(ShellContext);
	if (!ctx) throw new Error("useShell must be used within the _shell layout route");
	return ctx;
}
