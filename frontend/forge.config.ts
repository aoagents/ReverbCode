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
		// macOS signing + notarization. See frontend/docs/desktop-release.md.
		//
		// Signing: set APPLE_SIGNING_IDENTITY to the Developer ID Application
		// identity already imported in the local keychain (the CSR/.cer flow),
		// e.g. "Developer ID Application: Acme Inc (TEAMID1234)". The bundled
		// ao daemon (Contents/Resources/daemon/ao) is a standalone Go binary
		// that must be signed with hardened runtime + its own entitlements, or
		// notarization rejects the whole app. CSC_LINK is kept as a CI fallback
		// (cert pre-imported into a keychain by the runner).
		osxSign:
			process.env.APPLE_SIGNING_IDENTITY || process.env.CSC_LINK
				? {
						...(process.env.APPLE_SIGNING_IDENTITY ? { identity: process.env.APPLE_SIGNING_IDENTITY } : {}),
						optionsForFile: (filePath) =>
							filePath.includes("/daemon/ao")
								? { entitlements: "assets/entitlements.daemon.plist" }
								: { entitlements: "assets/entitlements.mac.plist" },
					}
				: undefined,
		// Notarization (notarytool). Primary: a stored App Store Connect API key
		// profile created once with `xcrun notarytool store-credentials`, named
		// via AO_NOTARY_PROFILE (e.g. "ao-notary"). Fallbacks: the raw API key
		// for CI, then the legacy Apple ID + app-specific password.
		osxNotarize: process.env.AO_NOTARY_PROFILE
			? { keychainProfile: process.env.AO_NOTARY_PROFILE }
			: process.env.APPLE_API_KEY
				? {
						appleApiKey: process.env.APPLE_API_KEY,
						appleApiKeyId: process.env.APPLE_API_KEY_ID!,
						appleApiIssuer: process.env.APPLE_API_ISSUER!,
					}
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
					// Must match packagerConfig.executableName; otherwise the deb
					// maker looks for `agent-orchestrator-frontend` (the package name)
					// and fails with "could not find the Electron app binary".
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
