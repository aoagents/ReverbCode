import type { ForgeConfig } from "@electron-forge/shared-types";
import { VitePlugin } from "@electron-forge/plugin-vite";

const config: ForgeConfig = {
	packagerConfig: {
		asar: true,
		appBundleId: "dev.agent-orchestrator.desktop",
		name: "Agent Orchestrator",
		executableName: "agent-orchestrator",
		appCategoryType: "public.app-category.developer-tools",
		extraResource: "daemon",
		// macOS signing + notarization — set CSC_LINK/CSC_KEY_PASSWORD and
		// APPLE_ID/APPLE_APP_SPECIFIC_PASSWORD/APPLE_TEAM_ID in CI.
		// See frontend/docs/desktop-release.md.
		osxSign: process.env.CSC_LINK ? {} : undefined,
		osxNotarize: process.env.APPLE_ID
			? {
					appleId: process.env.APPLE_ID,
					appleIdPassword: process.env.APPLE_APP_SPECIFIC_PASSWORD!,
					teamId: process.env.APPLE_TEAM_ID!,
				}
			: undefined,
	},
	rebuildConfig: {},
	makers: [
		{ name: "@electron-forge/maker-squirrel", config: { name: "AgentOrchestrator" } },
		{ name: "@electron-forge/maker-zip", platforms: ["darwin"], config: {} },
		{ name: "@electron-forge/maker-deb", config: {} },
		{ name: "@electron-forge/maker-rpm", config: {} },
	],
	publishers: [
		{
			name: "@electron-forge/publisher-github",
			config: {
				repository: { owner: "aoagents", name: "agent-orchestrator" },
				prerelease: false,
				draft: true,
			},
		},
	],
	plugins: [
		new VitePlugin({
			build: [
				{ entry: "src/main.ts", config: "vite.main.config.ts", target: "main" },
				{ entry: "src/preload.ts", config: "vite.preload.config.ts", target: "preload" },
			],
			renderer: [{ name: "main_window", config: "vite.renderer.config.ts" }],
		}),
	],
};

export default config;
