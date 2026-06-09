import { app, BrowserWindow, dialog, ipcMain, type OpenDialogOptions } from "electron";
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
      sandbox: false,
    },
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

  daemonProcess = spawn(command, [], {
    cwd: app.getAppPath(),
    env: process.env,
    shell: true,
  });

  daemonProcess.stdout.on("data", (chunk: Buffer) => {
    mainWindow?.webContents.send("daemon:stdout", chunk.toString("utf8"));
  });

  daemonProcess.stderr.on("data", (chunk: Buffer) => {
    mainWindow?.webContents.send("daemon:stderr", chunk.toString("utf8"));
  });

  daemonProcess.once("spawn", () => {
    setDaemonStatus({
      state: "ready",
      port: process.env.AO_PORT ? Number(process.env.AO_PORT) : undefined,
    });
  });

  daemonProcess.once("error", (error) => {
    daemonProcess = null;
    setDaemonStatus({ state: "error", message: error.message });
  });

  daemonProcess.once("exit", (code, signal) => {
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

app.whenReady().then(() => {
  createWindow();

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
