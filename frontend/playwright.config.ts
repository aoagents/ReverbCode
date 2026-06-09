import { defineConfig } from "@playwright/test";

export default defineConfig({
  testDir: "e2e",
  use: {
    baseURL: "http://127.0.0.1:5173",
  },
  webServer: {
    command: "npm run dev",
    port: 5173,
    reuseExistingServer: !process.env.CI,
    // vite-plugin-electron launches the Electron app on dev-server start; the e2e
    // suite only needs the renderer served in a browser, so suppress the launch.
    env: { ELECTRON_STARTUP_PREVENT: "1" },
  },
});
