// Deciding whether to ATTACH to an already-running daemon or SPAWN a fresh one.
//
// Two independent signals are consulted, in order:
//   1. the running.json handshake file (whatever the last daemon wrote), and
//   2. a direct probe of the expected port, independent of the run-file.
//
// (2) is the defensive backstop for issue #367: a standalone `ao daemon` may be
// serving the port while running.json is missing, stale, unparseable, names a
// dead PID, or reports a PID that disagrees with /healthz. In every one of those
// cases the run-file check yields null; without the port probe the supervisor
// would spawn a child daemon that the Go bind guard then refuses ("daemon
// already running … refusing to start") and exits 1.
//
// These functions are kept side-effect free and dependency-injected (no node:*
// or electron imports — the vite-plugin-electron-renderer polyfill breaks node:*
// under vitest, see daemon-discovery.ts) so they can be exercised directly; the
// Electron main process owns the real fs reads, process signals, fetch, and
// path identity check.

import type { DaemonStatus } from "./daemon-status";
import { parseRunFile } from "./daemon-discovery";

// The daemon's default bind port (backend/internal/config). AO_PORT overrides it.
export const DEFAULT_DAEMON_PORT = 3001;
// The `service` field every genuine AO daemon stamps on its health payloads.
export const DAEMON_SERVICE_NAME = "agent-orchestrator-daemon";

export type DaemonProbe = {
	status: string;
	service: string;
	pid: number;
	executablePath?: string;
	workingDirectory?: string;
};

/** A /healthz|/readyz probe of a loopback port; resolves null when nothing valid answers. */
export type DaemonProber = (port: number, endpoint: "healthz" | "readyz") => Promise<DaemonProbe | null>;

/**
 * The port a freshly spawned daemon is expected to bind: AO_PORT when set and
 * valid, otherwise the daemon's default. Used to probe for an already-serving
 * daemon before spawning a child that would only refuse and exit.
 */
export function expectedDaemonPort(env: Record<string, string | undefined>): number {
	const configured = env.AO_PORT ? Number(env.AO_PORT) : NaN;
	return Number.isInteger(configured) && configured >= 1 && configured <= 65535 ? configured : DEFAULT_DAEMON_PORT;
}

/**
 * Validate a /healthz or /readyz JSON body against the daemon contract. Returns
 * the typed probe, or null when the body is the wrong shape, status, or service
 * (e.g. some unrelated server happens to occupy the port).
 */
export function parseDaemonProbe(endpoint: "healthz" | "readyz", body: unknown): DaemonProbe | null {
	if (typeof body !== "object" || body === null) return null;
	const candidate = body as Partial<DaemonProbe>;
	if (candidate.status !== (endpoint === "healthz" ? "ok" : "ready")) return null;
	if (candidate.service !== DAEMON_SERVICE_NAME) return null;
	if (typeof candidate.pid !== "number" || !Number.isInteger(candidate.pid)) return null;
	return {
		status: candidate.status,
		service: candidate.service,
		pid: candidate.pid,
		executablePath: typeof candidate.executablePath === "string" ? candidate.executablePath : undefined,
		workingDirectory: typeof candidate.workingDirectory === "string" ? candidate.workingDirectory : undefined,
	};
}

export type RunFileResolveDeps = {
	/** running.json contents, or null when the file has no path or could not be read. */
	runFileContents: string | null;
	isProcessAlive: (pid: number) => boolean;
	probe: DaemonProber;
	/** Foreign-daemon check (dev/bundled identity); returns a message, or null when it is ours. */
	identityError: (probe: DaemonProbe) => string | null;
};

/**
 * Attach decision driven by the running.json handshake. Returns:
 *   - a "ready" status   → attach to the recorded daemon,
 *   - an "error" status  → a daemon is up but unusable (not ready / foreign);
 *                          surface it rather than spawn,
 *   - null               → the run-file is absent/stale/inconsistent; the caller
 *                          should fall through to {@link resolveDaemonFromPort}.
 */
export async function resolveDaemonFromRunFile(deps: RunFileResolveDeps): Promise<DaemonStatus | null> {
	const { runFileContents, isProcessAlive, probe, identityError } = deps;
	if (runFileContents === null) return null;
	const info = parseRunFile(runFileContents);
	if (!info || !isProcessAlive(info.pid)) return null;

	const health = await probe(info.port, "healthz");
	if (!health || health.pid !== info.pid) return null;
	const ready = await probe(info.port, "readyz");
	if (!ready || ready.pid !== info.pid) {
		return {
			state: "error",
			port: info.port,
			pid: info.pid,
			executablePath: health.executablePath,
			workingDirectory: health.workingDirectory,
			message: "An AO daemon is already running, but it is not ready yet.",
		};
	}

	const message = identityError(ready);
	if (message) {
		return {
			state: "error",
			port: info.port,
			pid: info.pid,
			executablePath: ready.executablePath,
			workingDirectory: ready.workingDirectory,
			message,
		};
	}

	return {
		state: "ready",
		port: info.port,
		pid: info.pid,
		executablePath: ready.executablePath,
		workingDirectory: ready.workingDirectory,
	};
}

export type PortProbeResolveDeps = {
	expectedPort: number;
	probe: DaemonProber;
};

/**
 * Attach decision driven by a direct /healthz probe of the expected port,
 * independent of the run-file (issue #367 backstop). Returns a "ready" status to
 * attach to whatever genuine daemon answers, or null when nothing does (the
 * caller should then spawn).
 */
export async function resolveDaemonFromPort(deps: PortProbeResolveDeps): Promise<DaemonStatus | null> {
	const { expectedPort, probe } = deps;
	const health = await probe(expectedPort, "healthz");
	if (!health) return null;
	return {
		state: "ready",
		port: expectedPort,
		pid: health.pid,
		executablePath: health.executablePath,
		workingDirectory: health.workingDirectory,
	};
}
