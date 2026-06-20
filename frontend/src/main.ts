import { app, BrowserWindow, dialog, ipcMain, net, protocol, shell, type OpenDialogOptions } from "electron";
import { updateElectronApp } from "update-electron-app";
import { spawn, type ChildProcessWithoutNullStreams } from "node:child_process";
import { existsSync } from "node:fs";
import { readFile } from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import { pathToFileURL } from "node:url";
import { resolveDaemonLaunch } from "./shared/daemon-launch";
import { createListenPortScanner, defaultRunFilePath, parseRunFile } from "./shared/daemon-discovery";
import type { DaemonStatus } from "./shared/daemon-status";

// Globals injected at compile time by @electron-forge/plugin-vite.
declare const MAIN_WINDOW_VITE_DEV_SERVER_URL: string | undefined;
declare const MAIN_WINDOW_VITE_NAME: string;

// Must run before app ready so the About panel and default-menu role labels use it.
app.setName("Agent Orchestrator");

let mainWindow: BrowserWindow | null = null;
let daemonProcess: ChildProcessWithoutNullStreams | null = null;
let daemonStatus: DaemonStatus = { state: "stopped" };

const isDev = !app.isPackaged;

const RENDERER_SCHEME = "app";
const RENDERER_HOST = "renderer";
const RENDERER_ORIGIN = `${RENDERER_SCHEME}://${RENDERER_HOST}`;

// The packaged renderer is served from a custom standard scheme, not file://.
// A file:// page has the opaque "null" origin, which the daemon must never
// trust (every sandboxed iframe on any website also presents "null"), so its
// fetch/EventSource calls to the loopback API would be CORS-blocked.
// app://renderer is an origin only this app can present, so the daemon's CORS
// allowlist can name it. A standard scheme also makes the build's absolute
// asset URLs (/assets/…) and history-API routing resolve, which file:// breaks.
// Must run before app ready.
protocol.registerSchemesAsPrivileged([
	{
		scheme: RENDERER_SCHEME,
		privileges: { standard: true, secure: true, supportFetchAPI: true },
	},
]);

// Maps app://renderer/<path> to the built renderer in dist/. Paths without a
// file extension are client-side routes and fall back to index.html (SPA).
function registerRendererProtocol(): void {
	const distRoot = path.join(__dirname, `../renderer/${MAIN_WINDOW_VITE_NAME}`);
	protocol.handle(RENDERER_SCHEME, async (request) => {
		const url = new URL(request.url);
		if (url.host !== RENDERER_HOST) {
			return new Response("Not found", { status: 404 });
		}
		const resolved = path.resolve(path.join(distRoot, decodeURIComponent(url.pathname)));
		if (resolved !== distRoot && !resolved.startsWith(distRoot + path.sep)) {
			return new Response("Forbidden", { status: 403 });
		}
		const target = path.extname(resolved) === "" ? path.join(distRoot, "index.html") : resolved;
		try {
			return await net.fetch(pathToFileURL(target).toString());
		} catch {
			return new Response("Not found", { status: 404 });
		}
	});
}

function rendererUrl(): string {
	if (typeof MAIN_WINDOW_VITE_DEV_SERVER_URL !== "undefined" && MAIN_WINDOW_VITE_DEV_SERVER_URL) {
		return MAIN_WINDOW_VITE_DEV_SERVER_URL;
	}

	return `${RENDERER_ORIGIN}/index.html`;
}

function preloadPath(): string {
	return path.join(__dirname, "preload.js");
}

function setDaemonStatus(nextStatus: DaemonStatus): void {
	daemonStatus = nextStatus;
	mainWindow?.webContents.send("daemon:status", daemonStatus);
}

function createWindow(): void {
	mainWindow = new BrowserWindow({
		width: 1320,
		height: 860,
		minWidth: 960,
		minHeight: 640,
		title: "Agent Orchestrator",
		backgroundColor: "#0f1014",
		titleBarStyle: "hiddenInset",
		// Lights visually centered at y=28 — the 56px topbar/.titlebar-nav center
		// line — so lights + nav cluster + header content share one row. macOS
		// draws the 12pt disc 2pt below the given y (measured: center = y + 8),
		// hence 20, not 22.
		trafficLightPosition: { x: 14, y: 20 },
		webPreferences: {
			preload: preloadPath(),
			contextIsolation: true,
			nodeIntegration: false,
			sandbox: true,
		},
	});

	// Harden navigation: never let renderer/terminal content open in-app windows or
	// navigate the privileged window away from the app origin. External links go to
	// the OS browser. Keep this in place before exposing any daemon output to the renderer.
	mainWindow.webContents.setWindowOpenHandler(({ url }) => {
		if (/^https?:\/\//.test(url)) {
			void shell.openExternal(url);
		}
		return { action: "deny" };
	});

	mainWindow.webContents.on("will-navigate", (event, url) => {
		if (url !== mainWindow?.webContents.getURL()) {
			event.preventDefault();
		}
	});

	void mainWindow.loadURL(rendererUrl());

	if (isDev && process.env.AO_OPEN_DEVTOOLS === "1") {
		mainWindow.webContents.once("did-frame-finish-load", () => {
			mainWindow?.webContents.openDevTools({ mode: "detach" });
		});
	}

	mainWindow.on("closed", () => {
		mainWindow = null;
	});
}

// How long the supervisor waits for the daemon to confirm its bound port (via
// the listen log line or running.json) before reporting the configured port as
// a best-effort fallback.
const PORT_DISCOVERY_TIMEOUT_MS = 15_000;
const RUN_FILE_POLL_MS = 300;
// Accept run-files stamped slightly before our spawn timestamp: the daemon's
// clock reading and ours race within normal scheduling jitter.
const RUN_FILE_FRESHNESS_SKEW_MS = 2_000;

function runFilePath(): string | null {
	if (process.env.AO_RUN_FILE) return process.env.AO_RUN_FILE;
	return defaultRunFilePath(process.platform, process.env, os.homedir());
}

function startDaemon(): DaemonStatus {
	if (daemonProcess) {
		return daemonStatus;
	}

	const launch = resolveDaemonLaunch(
		process.env,
		app.isPackaged,
		process.resourcesPath,
		app.getAppPath(),
		process.platform,
	);
	if (!launch) {
		setDaemonStatus({
			state: "stopped",
			message: "AO_DAEMON_COMMAND is not configured; renderer uses loopback REST when available.",
		});
		return daemonStatus;
	}

	if (launch.source === "bundled" && !existsSync(launch.command)) {
		setDaemonStatus({
			state: "error",
			message: `Bundled AO daemon binary was not found at ${launch.command}. Rebuild the desktop package.`,
		});
		return daemonStatus;
	}

	setDaemonStatus({ state: "starting" });

	// Capture the spawned handle locally so the async lifecycle listeners act only
	// on THIS process. Without this, a stale exit from an already-stopped daemon
	// could null out a newer daemonProcess started in the meantime, orphaning it.
	//
	// `detached` makes the child its own process-group leader. Because shell:true
	// runs the command through /bin/sh, a plain kill() would only signal the shell
	// wrapper and orphan the real daemon (which keeps holding the port). Killing
	// the whole group via killDaemon() reaches the daemon and any PTY children.
	const child = spawn(launch.command, launch.args, {
		cwd: launch.cwd,
		env: process.env,
		shell: launch.shell,
		detached: true,
	});
	daemonProcess = child;

	// Discover the port the daemon ACTUALLY bound rather than trusting AO_PORT:
	// the daemon may fall back to a different port than the one requested. Two
	// confirmed sources race — the "daemon listening" slog line (stderr, but both
	// streams are scanned) and the running.json handshake — first one wins.
	const spawnedAtMs = Date.now();
	let portConfirmed = false;
	let runFileTimer: ReturnType<typeof setInterval> | undefined;
	let fallbackTimer: ReturnType<typeof setTimeout> | undefined;

	const stopDiscovery = () => {
		if (runFileTimer) clearInterval(runFileTimer);
		runFileTimer = undefined;
		if (fallbackTimer) clearTimeout(fallbackTimer);
		fallbackTimer = undefined;
	};

	const reportBoundPort = (port: number) => {
		if (portConfirmed || daemonProcess !== child) return;
		portConfirmed = true;
		stopDiscovery();
		setDaemonStatus({ state: "ready", port });
	};

	// One scanner per stream: each keeps its own partial-line buffer.
	const scanStdout = createListenPortScanner(reportBoundPort);
	const scanStderr = createListenPortScanner(reportBoundPort);

	child.stdout.on("data", (chunk: Buffer) => {
		const text = chunk.toString("utf8");
		console.log(text.trimEnd());
		scanStdout(text);
	});

	child.stderr.on("data", (chunk: Buffer) => {
		const text = chunk.toString("utf8");
		console.error(text.trimEnd());
		scanStderr(text);
	});

	const handshakePath = runFilePath();
	if (handshakePath) {
		runFileTimer = setInterval(() => {
			readFile(handshakePath, "utf8")
				.then((contents) => {
					const info = parseRunFile(contents);
					// Ignore a stale handshake left by a previous daemon: only trust a
					// file written at/after this spawn.
					if (info && info.startedAtMs >= spawnedAtMs - RUN_FILE_FRESHNESS_SKEW_MS) {
						reportBoundPort(info.port);
					}
				})
				.catch(() => undefined); // absent until the daemon binds; keep polling
		}, RUN_FILE_POLL_MS);
	}

	// Last resort: neither source confirmed (e.g. an older daemon build). Report
	// the configured port so the renderer is not stuck on "starting" forever.
	fallbackTimer = setTimeout(() => {
		if (portConfirmed || daemonProcess !== child) return;
		stopDiscovery();
		setDaemonStatus({
			state: "ready",
			port: process.env.AO_PORT ? Number(process.env.AO_PORT) : undefined,
			message: "Daemon port not confirmed from logs or running.json; assuming the configured port.",
		});
	}, PORT_DISCOVERY_TIMEOUT_MS);

	child.once("error", (error) => {
		stopDiscovery();
		if (daemonProcess !== child) return;
		daemonProcess = null;
		setDaemonStatus({ state: "error", message: error.message });
	});

	child.once("exit", (code, signal) => {
		stopDiscovery();
		if (daemonProcess !== child) return;
		daemonProcess = null;
		setDaemonStatus({
			state: "stopped",
			message: signal ? `Daemon exited with ${signal}` : `Daemon exited with code ${code ?? "unknown"}`,
		});
	});

	return daemonStatus;
}

// Signal the daemon's whole process group so the kill reaches the real daemon
// behind the /bin/sh wrapper (and any PTY children it forked), not just the
// shell. Falls back to a direct kill if the group signal can't be delivered
// (e.g. the process already exited).
function killDaemon(child: ChildProcessWithoutNullStreams): void {
	if (child.pid === undefined) return;
	try {
		process.kill(-child.pid, "SIGTERM");
	} catch {
		child.kill("SIGTERM");
	}
}

function stopDaemon(): DaemonStatus {
	if (!daemonProcess) {
		setDaemonStatus({ state: "stopped" });
		return daemonStatus;
	}

	killDaemon(daemonProcess);
	daemonProcess = null;
	setDaemonStatus({ state: "stopped" });
	return daemonStatus;
}

ipcMain.handle("daemon:getStatus", () => daemonStatus);
ipcMain.handle("daemon:start", () => startDaemon());
ipcMain.handle("daemon:stop", () => stopDaemon());
ipcMain.handle("app:getVersion", () => app.getVersion());
ipcMain.handle("app:chooseDirectory", async () => {
	const options: OpenDialogOptions = {
		properties: ["openDirectory"],
		title: "Choose a git repository",
	};
	const result = mainWindow ? await dialog.showOpenDialog(mainWindow, options) : await dialog.showOpenDialog(options);

	if (result.canceled) return null;
	return result.filePaths[0] ?? null;
});

// Auto-update only runs for packaged builds reading the GitHub Releases feed
// (see forge.config.ts publishers). In dev there is no feed, so it is skipped.
// A live updater additionally requires a signed + notarized build — see
// frontend/docs/desktop-release.md.
function initAutoUpdates(): void {
	if (!app.isPackaged) return;
	updateElectronApp();
}

app.whenReady().then(() => {
	registerRendererProtocol();
	createWindow();
	startDaemon();
	initAutoUpdates();

	app.on("activate", () => {
		if (BrowserWindow.getAllWindows().length === 0) {
			createWindow();
		}
	});
});

app.on("before-quit", () => {
	if (daemonProcess) {
		killDaemon(daemonProcess);
	}
});

app.on("window-all-closed", () => {
	if (process.platform !== "darwin") {
		app.quit();
	}
});
