// `vitest run` only auto-loads vite.config.ts / vitest.config.ts — it never
// sees Forge's per-target vite.*.config.ts files, so the renderer config's
// `test` block (jsdom environment, setup file) was dead config and every
// DOM-touching test failed with "window is not defined". Re-export the
// renderer config so tests run under the same plugins and test settings.
export { default } from "./vite.renderer.config";
