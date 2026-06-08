import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";
import { resolve } from "node:path";

// The renderer is a standalone Vite + React app rooted at src/renderer. In dev
// it serves on :5173 (main loads VITE_DEV_SERVER_URL); for a packaged build it
// emits static files into dist/renderer, which the Electron main loads from
// disk. base "./" keeps asset URLs relative so they resolve under file://.
// The "@/" alias matches shadcn/emdash conventions for clean component imports.
export default defineConfig({
  root: resolve(__dirname, "src/renderer"),
  base: "./",
  plugins: [react(), tailwindcss()],
  resolve: {
    alias: { "@": resolve(__dirname, "src/renderer") },
  },
  server: {
    port: 5173,
    strictPort: true,
  },
  build: {
    outDir: resolve(__dirname, "dist/renderer"),
    emptyOutDir: true,
  },
});
