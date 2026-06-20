import { contextBridge, ipcRenderer } from "electron";
import type { DaemonStatus } from "./shared/daemon-status";

const api = {
	app: {
		getVersion: () => ipcRenderer.invoke("app:getVersion") as Promise<string>,
		chooseDirectory: () => ipcRenderer.invoke("app:chooseDirectory") as Promise<string | null>,
	},
	daemon: {
		getStatus: () => ipcRenderer.invoke("daemon:getStatus") as Promise<DaemonStatus>,
		start: () => ipcRenderer.invoke("daemon:start") as Promise<DaemonStatus>,
		stop: () => ipcRenderer.invoke("daemon:stop") as Promise<DaemonStatus>,
		onStatus: (listener: (status: DaemonStatus) => void) => {
			const wrapped = (_event: Electron.IpcRendererEvent, status: DaemonStatus) => listener(status);
			ipcRenderer.on("daemon:status", wrapped);
			return () => {
				ipcRenderer.off("daemon:status", wrapped);
			};
		},
	},
	diagnostics: {
		logTerminal: (event: string, fields?: Record<string, string | number | boolean | null | undefined>) => {
			ipcRenderer.send("diagnostics:terminal-flow", event, fields ?? {});
		},
	},
};

contextBridge.exposeInMainWorld("ao", api);

export type AoBridge = typeof api;
