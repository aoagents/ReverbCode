import type { ForgeConfig } from "@electron-forge/shared-types";
import { VitePlugin } from "@electron-forge/plugin-vite";

const config: ForgeConfig = {
	packagerConfig: {
		asar: true,
		appBundleId: "dev.agent-orchestrator.desktop",
		name: "Agent Orchestrator",
		executableName: "agent-orchestrator",
		appCategoryType: "public.app-category.developer-tools",
		// App icon. electron-packager appends the per-platform extension
		// (.icns on macOS, .ico on Windows); Linux menu icons come from the
		// deb/rpm makers below, and the runtime window icon from src/main.ts.
		icon: "assets/icon",
		extraResource: ["daemon", "assets/icon.png"],
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
		{
			name: "@electron-forge/maker-squirrel",
			config: {
				name: "AgentOrchestrator",
				// NuGet requires a non-empty <authors>; without it `nuget pack`
				// exits 1 and the Squirrel maker fails. Mirror package.json.author.
				authors: "Agent Orchestrator",
				setupIcon: "assets/icon.ico",
			},
		},
		{ name: "@electron-forge/maker-zip", platforms: ["darwin"], config: {} },
		{
			name: "@electron-forge/maker-deb",
			config: {
				options: {
					icon: "assets/icon.png",
					maintainer: "Agent Orchestrator",
					homepage: "https://github.com/aoagents/agent-orchestrator",
				},
			},
		},
		{
			name: "@electron-forge/maker-rpm",
			config: {
				options: {
					icon: "assets/icon.png",
					// rpmbuild rejects a spec with an empty License field.
					license: "MIT",
					homepage: "https://github.com/aoagents/agent-orchestrator",
				},
			},
		},
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
