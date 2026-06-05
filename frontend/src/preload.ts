import { contextBridge, ipcRenderer } from "electron";

// The preload is the ONLY bridge between the sandboxed renderer and the main
// process. It exposes a single typed `request` that forwards to the `ao:request`
// IPC channel (which proxies to the loopback daemon). The renderer therefore
// never sees `ipcRenderer` or the network directly — it just awaits `window.ao`.

export interface AoRequest {
  method: string;
  path: string;
  query?: Record<string, string | number | boolean | undefined>;
  body?: unknown;
}

export interface AoResponse<T = unknown> {
  ok: boolean;
  status: number;
  data: T;
  error?: string;
}

const api = {
  request: <T = unknown>(req: AoRequest): Promise<AoResponse<T>> =>
    ipcRenderer.invoke("ao:request", req),
};

contextBridge.exposeInMainWorld("ao", api);

export type AoBridge = typeof api;
