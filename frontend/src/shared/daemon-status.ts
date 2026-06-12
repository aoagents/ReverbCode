// DaemonStatus is the supervisor → renderer handshake payload, shared by the
// Electron main process (which derives it) and the preload bridge (which types
// the IPC surface). The renderer picks it up through the preload's AoBridge type.
export type DaemonStatus = {
	state: "starting" | "ready" | "stopped" | "error";
	port?: number;
	message?: string;
};

/** Value equality so status emitters can skip no-op broadcasts. */
export function daemonStatusEquals(a: DaemonStatus, b: DaemonStatus): boolean {
	return a.state === b.state && a.port === b.port && a.message === b.message;
}
