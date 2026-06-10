import type { AoBridge } from "../preload";

declare global {
	interface Window {
		ao?: AoBridge;
	}
}

export {};
