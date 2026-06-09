import { app, BrowserWindow, dialog, ipcMain, shell, type OpenDialogOptions } from "electron";
import { autoUpdater } from "electron-updater";
import { spawn, type ChildProcessWithoutNullStreams } from "node:child_process";
import path from "node:path";

type DaemonStatus = {
  state: "starting" | "ready" | "stopped" | "error";
  port?: number;
  message?: string;
};

let mainWindow: BrowserWindow | null = null;
let daemonProcess: ChildProcessWithoutNullStreams | null = null;
let daemonStatus: DaemonStatus = { state: "stopped" };

const isDev = !app.isPackaged;

function rendererUrl(): string {
  if (process.env.VITE_DEV_SERVER_URL) {
    return process.env.VITE_DEV_SERVER_URL;
  }

  return `file://${path.join(__dirname, "../dist/index.html")}`;
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
    trafficLightPosition: { x: 14, y: 14 },
    webPreferences: {
      preload: preloadPath(),
      contextIsolation: true,
      nodeIntegration: false,
      sandbox: true,
    },
  });

  // Harden navigation: never let renderer/terminal content open in-app windows or
  // navigate the privileged window away from the app origin. External links go to
  // the OS browser. This must stay in place before daemon stdout streaming is wired.
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

function startDaemon(): DaemonStatus {
  if (daemonProcess) {
    return daemonStatus;
  }

  const command = process.env.AO_DAEMON_COMMAND;
  if (!command) {
    setDaemonStatus({
      state: "stopped",
      message: "AO_DAEMON_COMMAND is not configured; renderer uses loopback REST when available.",
    });
    return daemonStatus;
  }

  setDaemonStatus({ state: "starting" });

  // Capture the spawned handle locally so the async lifecycle listeners act only
  // on THIS process. Without this, a stale exit from an already-stopped daemon
  // could null out a newer daemonProcess started in the meantime, orphaning it.
  const child = spawn(command, [], {
    cwd: app.getAppPath(),
    env: process.env,
    shell: true,
  });
  daemonProcess = child;

  child.stdout.on("data", (chunk: Buffer) => {
    mainWindow?.webContents.send("daemon:stdout", chunk.toString("utf8"));
  });

  child.stderr.on("data", (chunk: Buffer) => {
    mainWindow?.webContents.send("daemon:stderr", chunk.toString("utf8"));
  });

  child.once("spawn", () => {
    if (daemonProcess !== child) return;
    setDaemonStatus({
      state: "ready",
      port: process.env.AO_PORT ? Number(process.env.AO_PORT) : undefined,
    });
  });

  child.once("error", (error) => {
    if (daemonProcess !== child) return;
    daemonProcess = null;
    setDaemonStatus({ state: "error", message: error.message });
  });

  child.once("exit", (code, signal) => {
    if (daemonProcess !== child) return;
    daemonProcess = null;
    setDaemonStatus({
      state: "stopped",
      message: signal ? `Daemon exited with ${signal}` : `Daemon exited with code ${code ?? "unknown"}`,
    });
  });

  return daemonStatus;
}

function stopDaemon(): DaemonStatus {
  if (!daemonProcess) {
    setDaemonStatus({ state: "stopped" });
    return daemonStatus;
  }

  daemonProcess.kill();
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

// Auto-update only runs for packaged builds reading a published feed (see
// package.json build.publish). In dev there is no feed and electron-updater
// throws, so it is skipped. A live updater additionally requires a signed +
// notarized build — see frontend/docs/desktop-release.md.
function initAutoUpdates(): void {
  if (!app.isPackaged) return;

  autoUpdater.on("error", (error) => console.error("auto-update: error", error));
  autoUpdater.on("update-available", (info) => console.log("auto-update: available", info.version));
  autoUpdater.on("update-downloaded", (info) => console.log("auto-update: downloaded", info.version));

  autoUpdater.checkForUpdatesAndNotify().catch((error) => console.error("auto-update: check failed", error));
}

app.whenReady().then(() => {
  createWindow();
  initAutoUpdates();

  app.on("activate", () => {
    if (BrowserWindow.getAllWindows().length === 0) {
      createWindow();
    }
  });
});

app.on("before-quit", () => {
  if (daemonProcess) {
    daemonProcess.kill();
  }
});

app.on("window-all-closed", () => {
  if (process.platform !== "darwin") {
    app.quit();
  }
});
