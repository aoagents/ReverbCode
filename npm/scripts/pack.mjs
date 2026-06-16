#!/usr/bin/env node
// `npm pack` the staged packages from build/packages/ into build/tarballs/.
//
// Run `node scripts/build.mjs [...]` first to stage the packages. Only the
// packages that were actually staged are packed, so `build.mjs --host` followed
// by this script produces just the main + host tarballs for a local smoke test.
//
// Usage:
//   node scripts/pack.mjs

import { execFileSync } from "node:child_process";
import { existsSync, mkdirSync, readdirSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const here = dirname(fileURLToPath(import.meta.url));
const npmRoot = join(here, "..");
const pkgOut = join(npmRoot, "build", "packages");
const tarballsOut = join(npmRoot, "build", "tarballs");

if (!existsSync(pkgOut)) {
  console.error("build/packages/ not found. Run scripts/build.mjs first.");
  process.exit(1);
}

mkdirSync(tarballsOut, { recursive: true });

const staged = readdirSync(pkgOut, { withFileTypes: true })
  .filter((d) => d.isDirectory())
  .map((d) => join(pkgOut, d.name));

if (staged.length === 0) {
  console.error("No staged packages found in build/packages/.");
  process.exit(1);
}

console.log(`Packing ${staged.length} package(s) into ${tarballsOut}\n`);
for (const dir of staged) {
  const out = execFileSync("npm", ["pack", "--pack-destination", tarballsOut, dir], {
    encoding: "utf8",
  });
  console.log(`  ${out.trim().split("\n").pop()}`);
}
console.log("\nDone.");
