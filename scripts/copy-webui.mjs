#!/usr/bin/env node
// Copies the built ao-web SPA into the Go embed directory so `go build` bakes
// the current UI into the daemon binary. Run by `npm run build:frontend` after
// the Vite build. The committed placeholder index.html and the .gitkeep
// sentinel are preserved; everything else copied here is gitignored.
import { cpSync, existsSync, mkdirSync, readdirSync, rmSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const repoRoot = join(dirname(fileURLToPath(import.meta.url)), "..");
const src = join(repoRoot, "frontend", "apps", "web", "dist");
const dest = join(repoRoot, "backend", "internal", "webui", "dist");

if (!existsSync(src)) {
  console.error(`build:frontend: missing Vite output at ${src} — run the web build first`);
  process.exit(1);
}

mkdirSync(dest, { recursive: true });

// Clear previous build output but keep the .gitkeep sentinel so the directory
// (and the //go:embed dist target) always exists in a fresh checkout.
for (const entry of readdirSync(dest)) {
  if (entry === ".gitkeep") continue;
  rmSync(join(dest, entry), { recursive: true, force: true });
}

cpSync(src, dest, { recursive: true });
console.log(`build:frontend: copied ${src} -> ${dest}`);
