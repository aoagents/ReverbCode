// Helpers for discovering the daemon's actually-bound port. The configured
// AO_PORT is only a request — the daemon may bind a different port (port 0,
// operator overrides), so the supervisor trusts what the daemon reports:
//   - the slog text line `msg="daemon listening" addr=127.0.0.1:<port>`
//     (backend/internal/httpd/server.go, written to stderr), and
//   - the running.json handshake file (backend/internal/runfile).
// These functions are kept side-effect free and dependency-free (no node:*
// imports — vite-plugin-electron-renderer's polyfill breaks them under vitest)
// so tests can exercise them directly; the Electron main process owns the
// streams, fs polling, and timers.

// Minimal join: "/" works for fs access on every platform Node supports,
// including Windows paths that already contain backslashes (e.g. %APPDATA%).
function joinPath(...segments: string[]): string {
	return segments.map((segment) => segment.replace(/[/\\]+$/, "")).join("/");
}

/**
 * Parse one daemon log line for the listen announcement. slog's TextHandler
 * emits `time=… level=INFO msg="daemon listening" addr=127.0.0.1:3001 pid=…`;
 * the addr value never contains spaces, so it is unquoted. Returns the bound
 * port, or null when the line is not the announcement.
 */
export function parseDaemonListenPort(line: string): number | null {
	if (!line.includes('msg="daemon listening"')) return null;
	const addr = /(?:^|\s)addr=("?)([^"\s]+)\1/.exec(line)?.[2];
	if (!addr) return null;
	return portFromAddr(addr);
}

// Take the segment after the last ":" so IPv6 literals like [::1]:3001 parse too.
function portFromAddr(addr: string): number | null {
	const separator = addr.lastIndexOf(":");
	if (separator === -1) return null;
	const port = Number(addr.slice(separator + 1));
	return Number.isInteger(port) && port >= 1 && port <= 65535 ? port : null;
}

/**
 * Incrementally scan a stdio stream for the listen announcement. Returns a
 * chunk consumer that line-buffers (chunks can split a line anywhere) and
 * invokes onPort exactly once, for the first announcement seen.
 */
export function createListenPortScanner(onPort: (port: number) => void): (chunk: string) => void {
	let pending = "";
	let done = false;
	return (chunk) => {
		if (done) return;
		pending += chunk;
		const lines = pending.split("\n");
		pending = lines.pop() ?? "";
		for (const line of lines) {
			const port = parseDaemonListenPort(line);
			if (port !== null) {
				done = true;
				onPort(port);
				return;
			}
		}
	};
}

/** Parsed running.json handshake — see backend/internal/runfile.Info. */
export type RunFileInfo = {
	pid: number;
	port: number;
	/** startedAt in epoch ms; 0 when missing/unparseable. */
	startedAtMs: number;
};

/** Parse running.json contents. Returns null for malformed JSON or an invalid port. */
export function parseRunFile(contents: string): RunFileInfo | null {
	let raw: unknown;
	try {
		raw = JSON.parse(contents);
	} catch {
		return null;
	}
	if (typeof raw !== "object" || raw === null) return null;
	const { pid, port, startedAt } = raw as { pid?: unknown; port?: unknown; startedAt?: unknown };
	if (typeof port !== "number" || !Number.isInteger(port) || port < 1 || port > 65535) return null;
	const startedAtMs = typeof startedAt === "string" ? Date.parse(startedAt) : NaN;
	return {
		pid: typeof pid === "number" && Number.isInteger(pid) ? pid : 0,
		port,
		startedAtMs: Number.isNaN(startedAtMs) ? 0 : startedAtMs,
	};
}

/**
 * Whether a /healthz response body identifies the agent-orchestrator daemon
 * (backend/internal/httpd/router.go handleHealthz). Guards external-daemon
 * discovery against a foreign process squatting on a stale run-file port.
 */
export function isDaemonHealthz(body: string): boolean {
	let raw: unknown;
	try {
		raw = JSON.parse(body);
	} catch {
		return false;
	}
	if (typeof raw !== "object" || raw === null) return false;
	const { service, status } = raw as { service?: unknown; status?: unknown };
	return service === "agent-orchestrator-daemon" && status === "ok";
}

/**
 * Where the daemon writes running.json when AO_RUN_FILE is unset. Mirrors Go's
 * os.UserConfigDir() + "agent-orchestrator/running.json" resolution in
 * backend/internal/config so the supervisor reads the same file the daemon
 * writes. Returns null when the platform's config root cannot be resolved.
 */
export function defaultRunFilePath(
	platform: NodeJS.Platform,
	env: Record<string, string | undefined>,
	homeDir: string,
): string | null {
	if (platform === "darwin") {
		if (!homeDir) return null;
		return joinPath(homeDir, "Library", "Application Support", "agent-orchestrator", "running.json");
	}
	if (platform === "win32") {
		if (!env.APPDATA) return null;
		return joinPath(env.APPDATA, "agent-orchestrator", "running.json");
	}
	const configRoot = env.XDG_CONFIG_HOME || (homeDir ? joinPath(homeDir, ".config") : "");
	if (!configRoot) return null;
	return joinPath(configRoot, "agent-orchestrator", "running.json");
}
