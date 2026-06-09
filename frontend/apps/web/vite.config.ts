import tailwindcss from "@tailwindcss/vite";
import react from "@vitejs/plugin-react";
import { defineConfig } from "vitest/config";

// vitest/config re-exports Vite's defineConfig with the `test` field typed, so
// the dev/build config and the unit-test config live in one place.
export default defineConfig({
  plugins: [react(), tailwindcss()],
  server: {
    port: 5173,
  },
  test: {
    environment: "node",
  },
});
