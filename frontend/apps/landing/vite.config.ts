import { defineConfig } from "vite";

// Empty Vite scaffold for the landing app. The existing Next.js landing under
// frontend/src/landing is intentionally NOT moved here this session (it is
// tied to the react-doctor CI job); its port to this app lands later.
export default defineConfig({
  server: {
    port: 5174,
  },
});
