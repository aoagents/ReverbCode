import type { AoBridge } from "../../preload";

export const aoBridge: AoBridge =
	window.ao ??
	({
		app: {
			getVersion: async () => "0.0.0-preview",
			chooseDirectory: async () => null,
		},
		daemon: {
			getStatus: async () => ({
				state: "stopped",
				message: "Electron preload is not available in browser preview.",
			}),
			start: async () => ({ state: "starting" }),
			stop: async () => ({ state: "stopped" }),
			onStatus: () => () => undefined,
		},
		telemetry: {
			getBootstrap: async () => null,
		},
	} satisfies AoBridge);
