import { contextBridge, ipcRenderer } from "electron";
import type { DaemonStatus } from "./shared/daemon-status";
import type { TelemetryBootstrap } from "./shared/telemetry";

const api = {
	app: {
		getVersion: () => ipcRenderer.invoke("app:getVersion") as Promise<string>,
		chooseDirectory: () => ipcRenderer.invoke("app:chooseDirectory") as Promise<string | null>,
	},
	clipboard: {
		writeText: (text: string) => ipcRenderer.invoke("clipboard:writeText", text) as Promise<void>,
		readText: () => ipcRenderer.invoke("clipboard:readText") as Promise<string>,
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
	telemetry: {
		getBootstrap: () => ipcRenderer.invoke("telemetry:getBootstrap") as Promise<TelemetryBootstrap | null>,
	},
	notifications: {
		show: (notification: { id: string; title: string; body?: string }) =>
			ipcRenderer.invoke("notifications:show", notification) as Promise<void>,
		onClick: (listener: (id: string) => void) => {
			const wrapped = (_event: Electron.IpcRendererEvent, id: string) => listener(id);
			ipcRenderer.on("notifications:click", wrapped);
			return () => {
				ipcRenderer.off("notifications:click", wrapped);
			};
		},
	},
};

contextBridge.exposeInMainWorld("ao", api);

export type AoBridge = typeof api;
