import type { ForgeConfig } from "@electron-forge/shared-types";
import { VitePlugin } from "@electron-forge/plugin-vite";
import MakerNSIS from "./makers/maker-nsis";

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
		// macOS signing + notarization. Two paths are supported:
		//  - CI: set CSC_LINK/CSC_KEY_PASSWORD and
		//    APPLE_ID/APPLE_APP_SPECIFIC_PASSWORD/APPLE_TEAM_ID.
		//  - Local keychain: set APPLE_SIGNING_IDENTITY (a Developer ID Application
		//    identity in the login keychain) and AO_NOTARY_PROFILE (a notarytool
		//    keychain profile created with `notarytool store-credentials`).
		// See frontend/docs/desktop-release.md.
		osxSign: process.env.APPLE_SIGNING_IDENTITY
			? { identity: process.env.APPLE_SIGNING_IDENTITY }
			: process.env.CSC_LINK
				? {}
				: undefined,
		osxNotarize: process.env.AO_NOTARY_PROFILE
			? ({
					tool: "notarytool",
					keychainProfile: process.env.AO_NOTARY_PROFILE,
				} as unknown as ForgeConfig["packagerConfig"]["osxNotarize"])
			: process.env.APPLE_ID
				? {
						appleId: process.env.APPLE_ID,
						appleIdPassword: process.env.APPLE_APP_SPECIFIC_PASSWORD!,
						teamId: process.env.APPLE_TEAM_ID!,
					}
				: undefined,
	},
	rebuildConfig: {},
	makers: [
		// Windows installer: NSIS via electron-builder (see makers/maker-nsis.ts).
		// Replaces Squirrel.Windows, which only does per-user installs with no
		// custom install dir or proper uninstaller (issue #401).
		new MakerNSIS(
			{
				appId: "dev.agent-orchestrator.desktop",
				productName: "Agent Orchestrator",
				icon: "assets/icon.ico",
			},
			["win32"],
		),
		{ name: "@electron-forge/maker-zip", platforms: ["darwin"], config: {} },
		{
			name: "@electron-forge/maker-deb",
			config: {
				options: {
					// Must match packagerConfig.executableName, or the deb maker
					// looks for the package name and fails with "could not find
					// the Electron app binary". (Both are "agent-orchestrator".)
					bin: "agent-orchestrator",
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
				{ entry: "src/annotate-preload.ts", config: "vite.preload.config.ts", target: "preload" },
			],
			renderer: [{ name: "main_window", config: "vite.renderer.config.ts" }],
		}),
	],
};

export default config;
