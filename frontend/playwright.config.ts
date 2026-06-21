import { defineConfig } from "@playwright/test";

const e2ePort = parseE2EPort(process.env.PLAYWRIGHT_E2E_PORT);
const e2eHost = "127.0.0.1";
const webServerEnv = Object.fromEntries(
	Object.entries(process.env).filter(
		(entry): entry is [string, string] => entry[0].startsWith("VITE_") && typeof entry[1] === "string",
	),
);
webServerEnv.VITE_AO_API_BASE_URL = `http://${e2eHost}:${e2ePort}`;

function parseE2EPort(value: string | undefined): number {
	const port = Number(value ?? 5174);
	if (!Number.isInteger(port) || port < 1 || port > 65535) {
		throw new Error(`PLAYWRIGHT_E2E_PORT must be an integer TCP port, got ${value ?? "5174"}`);
	}
	return port;
}

export default defineConfig({
	testDir: "e2e",
	use: {
		baseURL: `http://${e2eHost}:${e2ePort}`,
	},
	webServer: {
		// dev:web serves the renderer alone (VITE_NO_ELECTRON=1) — no Electron child to
		// launch, which is all the browser-based e2e suite needs.
		command: `npm run dev:web -- --host ${e2eHost} --port ${e2ePort} --strictPort`,
		env: webServerEnv,
		port: e2ePort,
		reuseExistingServer: false,
	},
});
