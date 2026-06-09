import js from "@eslint/js";
import globals from "globals";
import tseslint from "typescript-eslint";

// Shared flat config for the pnpm workspace (apps/* + packages/*).
// Kept intentionally lean for phase 0: base JS + TypeScript recommended rules.
// React-specific lint plugins land alongside the first real UI components.
export default tseslint.config(
  {
    ignores: [
      "**/dist/**",
      "**/node_modules/**",
      "**/.tanstack/**",
      // Legacy reference UI under frontend/src is NOT part of this workspace.
      // It is retained only for the api-drift (schema.ts) and react-doctor
      // (landing) CI jobs and is tooled separately — keep it out of lint.
      "src/**",
    ],
  },
  js.configs.recommended,
  ...tseslint.configs.recommended,
  {
    files: ["**/*.{ts,tsx}"],
    languageOptions: {
      ecmaVersion: 2022,
      sourceType: "module",
      globals: {
        ...globals.browser,
        ...globals.node,
      },
    },
  },
);
