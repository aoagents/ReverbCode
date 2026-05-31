import { app, BrowserWindow, Notification as ElectronNotification } from "electron";
import { mkdir, readFile, writeFile } from "node:fs/promises";
import { dirname, join } from "node:path";

import {
  CDCEvent,
  NotificationAction,
  NotificationCreatedPayload,
  desktopPayloadFor,
  invokeNotificationAction,
  isSafeInternalRoute,
  notificationEventsURL,
  notificationRecordFromCDCEvent,
  patchNotification,
  shouldShowDesktop,
} from "./notifications";

let mainWindow: BrowserWindow | undefined;
let daemonOrigin = daemonOriginFromEnv();
const shownNotifications = new Set<string>();
const nativeNotifications = new Set<ElectronNotification>();

function createWindow(): BrowserWindow {
  const window = new BrowserWindow({
    width: 1200,
    height: 800,
    webPreferences: {
      contextIsolation: true,
      nodeIntegration: false,
    },
  });
  mainWindow = window;
  window.on("closed", () => {
    if (mainWindow === window) {
      mainWindow = undefined;
    }
  });
  void window.loadURL(daemonOrigin);
  return window;
}

app.whenReady().then(async () => {
  daemonOrigin = daemonOriginFromEnv();
  await waitForDaemonReady(daemonOrigin);
  createWindow();
  void startNotificationSSE(daemonOrigin);

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

async function startNotificationSSE(origin: string): Promise<void> {
  let lastSeq = await readLastSeq();
  for (;;) {
    try {
      const url = notificationEventsURL(origin);
      const headers: Record<string, string> = {};
      if (lastSeq > 0) {
        headers["Last-Event-ID"] = String(lastSeq);
      }
      const response = await fetch(url, { headers });
      if (!response.ok || response.body === null) {
        throw new Error(`SSE failed with ${response.status}`);
      }
      for await (const event of readSSE(response.body)) {
        if (event.id !== undefined) {
          const seq = Number(event.id);
          if (Number.isFinite(seq)) {
            lastSeq = Math.max(lastSeq, seq);
            await writeLastSeq(lastSeq);
          }
        }
        if (event.event === "notification_created") {
          await handleNotificationCreated(origin, event.data);
        }
      }
    } catch (error) {
      console.warn("AO notification SSE disconnected", error);
      await delay(2_000);
    }
  }
}

async function handleNotificationCreated(origin: string, data: string): Promise<void> {
  let event: CDCEvent<NotificationCreatedPayload>;
  try {
    event = JSON.parse(data) as CDCEvent<NotificationCreatedPayload>;
  } catch (error) {
    console.warn("Ignoring malformed notification SSE payload", error);
    return;
  }
  const record = notificationRecordFromCDCEvent(event);
  const key = `${record.id}:${event.seq}`;
  if (shownNotifications.has(key) || !shouldShowDesktop(record)) {
    return;
  }
  shownNotifications.add(key);
  const payload = desktopPayloadFor(record);
  const native = new ElectronNotification({
    title: payload.title,
    body: payload.body,
    silent: payload.silent,
    actions: payload.actions.map((action) => ({ type: "button" as const, text: action.label })),
  });
  nativeNotifications.add(native);
  native.once("close", () => nativeNotifications.delete(native));
  native.once("failed", (_event, error) => {
    nativeNotifications.delete(native);
    console.warn("AO desktop notification failed", error);
  });
  native.on("click", () => {
    focusDashboard(payload.route);
    void patchNotification(origin, record.id, { read: true }).catch((error) => {
      console.warn("Failed to mark notification read after click", error);
    });
  });
  native.on("action", (_event, index) => {
    const action = payload.actions[index];
    if (action !== undefined) {
      void handleNativeAction(origin, record.id, action);
    }
  });
  native.show();
}

async function handleNativeAction(origin: string, notificationId: string, action: NotificationAction): Promise<void> {
  if (action.kind === "mark-read") {
    await patchNotification(origin, notificationId, { read: true });
    return;
  }
  if ((action.kind === "open-session" || action.kind === "restore-session") && action.route !== undefined) {
    focusDashboard(action.route);
  }
  const token = process.env.AO_ACTION_TOKEN;
  if (token !== undefined && token !== "") {
    await invokeNotificationAction(origin, notificationId, action.id, token);
  }
}

function focusDashboard(route?: string): void {
  const window = mainWindow ?? createWindow();
  if (route !== undefined && isSafeInternalRoute(route)) {
    void window.loadURL(new URL(route, daemonOrigin).toString());
  }
  if (window.isMinimized()) {
    window.restore();
  }
  window.show();
  window.focus();
}

interface SSEMessage {
  id?: string;
  event: string;
  data: string;
}

async function* readSSE(body: ReadableStream<Uint8Array>): AsyncGenerator<SSEMessage> {
  const reader = body.getReader();
  const decoder = new TextDecoder();
  let buffer = "";
  for (;;) {
    const { done, value } = await reader.read();
    if (done) {
      return;
    }
    buffer += decoder.decode(value, { stream: true });
    let boundary = buffer.indexOf("\n\n");
    while (boundary !== -1) {
      const raw = buffer.slice(0, boundary);
      buffer = buffer.slice(boundary + 2);
      const parsed = parseSSEMessage(raw);
      if (parsed !== undefined) {
        yield parsed;
      }
      boundary = buffer.indexOf("\n\n");
    }
  }
}

function parseSSEMessage(raw: string): SSEMessage | undefined {
  let id: string | undefined;
  let event = "message";
  const data: string[] = [];
  for (const line of raw.split("\n")) {
    if (line.startsWith(":") || line === "") {
      continue;
    }
    if (line.startsWith("id:")) {
      id = line.slice(3).trimStart();
    } else if (line.startsWith("event:")) {
      event = line.slice(6).trimStart();
    } else if (line.startsWith("data:")) {
      data.push(line.slice(5).trimStart());
    }
  }
  if (data.length === 0) {
    return undefined;
  }
  return { id, event, data: data.join("\n") };
}

function daemonOriginFromEnv(): string {
  if (process.env.AO_DAEMON_ORIGIN !== undefined && process.env.AO_DAEMON_ORIGIN !== "") {
    const candidate = process.env.AO_DAEMON_ORIGIN;
    if (isLoopbackOrigin(candidate)) {
      return candidate;
    }
    console.warn("Ignoring non-loopback AO_DAEMON_ORIGIN");
  }
  const port = process.env.AO_PORT ?? "3001";
  return `http://127.0.0.1:${port}`;
}

function isLoopbackOrigin(raw: string): boolean {
  try {
    const url = new URL(raw);
    return url.protocol === "http:" && ["127.0.0.1", "localhost", "[::1]"].includes(url.hostname);
  } catch {
    return false;
  }
}

async function waitForDaemonReady(origin: string): Promise<void> {
  for (let attempt = 0; attempt < 60; attempt += 1) {
    try {
      const response = await fetch(new URL("/readyz", origin));
      if (response.ok) {
        return;
      }
    } catch {
      // Daemon may still be starting.
    }
    await delay(500);
  }
}

function statePath(): string {
  return join(app.getPath("userData"), "notification-state.json");
}

async function readLastSeq(): Promise<number> {
  try {
    const parsed = JSON.parse(await readFile(statePath(), "utf8")) as { lastSeq?: number };
    return typeof parsed.lastSeq === "number" && Number.isFinite(parsed.lastSeq) ? parsed.lastSeq : 0;
  } catch {
    return 0;
  }
}

async function writeLastSeq(lastSeq: number): Promise<void> {
  const path = statePath();
  await mkdir(dirname(path), { recursive: true });
  await writeFile(path, `${JSON.stringify({ lastSeq })}\n`, "utf8");
}

function delay(ms: number): Promise<void> {
  return new Promise((resolve) => {
    setTimeout(resolve, ms);
  });
}
