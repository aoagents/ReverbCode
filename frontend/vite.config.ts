import { defineConfig } from "vitest/config";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";
import electron from "vite-plugin-electron/simple";

export default defineConfig({
  plugins: [
    react(),
    tailwindcss(),
    electron({
      main: {
        entry: "src/main.ts",
      },
      preload: {
        input: "src/preload.ts",
      },
      renderer: {},
    }),
  ],
  test: {
    environment: "jsdom",
    exclude: ["node_modules/**", "dist/**", "dist-electron/**", "e2e/**"],
    globals: true,
    setupFiles: "./src/renderer/test/setup.ts",
  },
});
