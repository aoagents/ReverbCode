import { defineConfig } from "vitest/config";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";
import electron from "vite-plugin-electron/simple";

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
