import { defineConfig } from "vitest/config";
import type { Plugin } from "vite";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";
import electron from "vite-plugin-electron/simple";

// CSP for the built renderer. The daemon is loopback-only, so network access is
// pinned to 127.0.0.1 (REST + SSE over http, terminal mux over ws). Injected at
// build time rather than written into index.html because the dev server needs
// inline scripts (react-refresh preamble) that a static meta tag would block.
const CONTENT_SECURITY_POLICY = [
	"default-src 'self'",
	"script-src 'self'",
	// Inline style attributes (xterm, radix) have no nonce path; allow them.
	"style-src 'self' 'unsafe-inline'",
	"img-src 'self' data:",
	"font-src 'self' data:",
	"connect-src 'self' http://127.0.0.1:* ws://127.0.0.1:*",
	"object-src 'none'",
	"base-uri 'self'",
	"frame-src 'none'",
].join("; ");

const injectCspMeta: Plugin = {
	name: "inject-csp-meta",
	apply: "build",
	transformIndexHtml() {
		return [
			{
				tag: "meta",
				attrs: { "http-equiv": "Content-Security-Policy", content: CONTENT_SECURITY_POLICY },
				injectTo: "head-prepend",
			},
		];
	},
};

// VITE_NO_ELECTRON=1 serves the renderer as a plain web app (no Electron child),
// so it can be opened in a browser for design QA / screenshots. `window.ao` is
// then undefined, which the renderer already handles (TerminalPane scaffold).
const skipElectron = process.env.VITE_NO_ELECTRON === "1";

export default defineConfig({
	server: {
		proxy: {
			"/api": {
				target: process.env.AO_DEV_API_TARGET ?? "http://127.0.0.1:3001",
				changeOrigin: false,
			},
			"/mux": {
				target: process.env.AO_DEV_API_TARGET ?? "http://127.0.0.1:3001",
				changeOrigin: false,
				ws: true,
			},
		},
	},
	plugins: [
		react(),
		tailwindcss(),
		injectCspMeta,
		...(skipElectron
			? []
			: [
					electron({
						main: {
							entry: "src/main.ts",
						},
						preload: {
							input: "src/preload.ts",
						},
						renderer: {},
					}),
				]),
	],
	test: {
		environment: "jsdom",
		exclude: ["node_modules/**", "dist/**", "dist-electron/**", "e2e/**"],
		globals: true,
		setupFiles: "./src/renderer/test/setup.ts",
	},
});
