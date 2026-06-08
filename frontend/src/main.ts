import { app, BrowserWindow, ipcMain } from "electron";
import { readFileSync } from "node:fs";
import { homedir } from "node:os";
import { join } from "node:path";

// The Electron main process is the app's thin supervisor: it owns the window
// and is the ONLY layer that talks to the loopback daemon over HTTP. The
// renderer never reaches the network directly — it calls `window.ao.request`,
// which round-trips through the `ao:request` IPC channel below. This keeps the
// daemon loopback-only, sidesteps browser CORS, and matches the repo rule that
// daemon logic must not move into the UI surface.

/** Shape returned to the renderer for every proxied daemon call. */
interface AoResponse {
  ok: boolean;
  status: number;
  data: unknown;
  error?: string;
}

interface AoRequest {
  method: string;
  path: string;
  query?: Record<string, string | number | boolean | undefined>;
  body?: unknown;
}

/**
 * Resolve the daemon base URL. The daemon writes its live pid/port to a
 * handshake file on start (AO_RUN_FILE, defaulting to the user config dir), so
 * we read the port from there and fall back to AO_PORT / 3001. The host is
 * always loopback — the daemon refuses to bind anything else.
 */
function daemonBaseURL(): string {
  const port = discoverPort();
  return `http://127.0.0.1:${port}`;
}

function runFilePath(): string {
  if (process.env.AO_RUN_FILE) return process.env.AO_RUN_FILE;
  const configHome =
    process.env.XDG_CONFIG_HOME || join(homedir(), ".config");
  return join(configHome, "agent-orchestrator", "running.json");
}

function discoverPort(): number {
  try {
    const raw = readFileSync(runFilePath(), "utf8");
    const parsed = JSON.parse(raw) as { port?: number };
    if (parsed.port) return parsed.port;
  } catch {
    // No handshake file yet (daemon not started) — fall through to env/default.
  }
  const envPort = Number(process.env.AO_PORT);
  return Number.isFinite(envPort) && envPort > 0 ? envPort : 3001;
}

function buildURL(base: string, path: string, query?: AoRequest["query"]): string {
  const url = new URL(path.replace(/^\/+/, "/"), base);
  if (query) {
    for (const [key, value] of Object.entries(query)) {
      if (value !== undefined) url.searchParams.set(key, String(value));
    }
  }
  return url.toString();
}

async function proxyToDaemon(req: AoRequest): Promise<AoResponse> {
  const base = daemonBaseURL();
  const url = buildURL(base, req.path, req.query);
  try {
    const res = await fetch(url, {
      method: req.method,
      headers: req.body ? { "Content-Type": "application/json" } : undefined,
      body: req.body ? JSON.stringify(req.body) : undefined,
    });
    const text = await res.text();
    const data = text ? safeJSON(text) : null;
    return { ok: res.ok, status: res.status, data };
  } catch (err) {
    // Connection refused etc. — the daemon is almost certainly not running.
    return {
      ok: false,
      status: 0,
      data: null,
      error: err instanceof Error ? err.message : String(err),
    };
  }
}

function safeJSON(text: string): unknown {
  try {
    return JSON.parse(text);
  } catch {
    return text;
  }
}

function createWindow(): void {
  const window = new BrowserWindow({
    width: 1280,
    height: 860,
    minWidth: 900,
    minHeight: 600,
    backgroundColor: "#0b0d12",
    title: "ReverbCode",
    webPreferences: {
      preload: join(__dirname, "preload.js"),
      contextIsolation: true,
      nodeIntegration: false,
    },
  });

  const devServer = process.env.VITE_DEV_SERVER_URL;
  if (devServer) {
    void window.loadURL(devServer);
    window.webContents.openDevTools({ mode: "detach" });
  } else {
    void window.loadFile(join(__dirname, "renderer", "index.html"));
  }
}

app.whenReady().then(() => {
  ipcMain.handle("ao:request", (_event, req: AoRequest) => proxyToDaemon(req));

  createWindow();

  app.on("activate", () => {
    if (BrowserWindow.getAllWindows().length === 0) {
      createWindow();
    }
  });
});

app.on("window-all-closed", () => {
  if (process.platform !== "darwin") {
    app.quit();
  }
});
