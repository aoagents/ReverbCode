#!/usr/bin/env node
// Flatten the per-target daemon binaries into build/release/ with descriptive,
// download-friendly names plus a SHA256SUMS manifest. These are the raw
// artifacts uploaded to the GitHub Release; the npm sub-packages are built from
// the same binaries under build/bin/.
//
// Run `node scripts/build.mjs` first to produce build/bin/<os>-<arch>/.
//
// Output:
//   build/release/ao-<os>-<arch>[.exe]
//   build/release/SHA256SUMS
//
// Usage:
//   node scripts/collect-release.mjs

import { createHash } from "node:crypto";
import { copyFileSync, existsSync, mkdirSync, readFileSync, writeFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

import { targets, binaryName } from "../targets.mjs";

const here = dirname(fileURLToPath(import.meta.url));
const npmRoot = join(here, "..");
const binDir = join(npmRoot, "build", "bin");
const releaseDir = join(npmRoot, "build", "release");

mkdirSync(releaseDir, { recursive: true });

const sums = [];
let collected = 0;
for (const t of targets) {
	const src = join(binDir, `${t.os}-${t.arch}`, binaryName(t.os));
	if (!existsSync(src)) {
		console.log(`  skip ${t.os}-${t.arch} (not built)`);
		continue;
	}
	const ext = t.os === "win32" ? ".exe" : "";
	const name = `ao-${t.os}-${t.arch}${ext}`;
	const dest = join(releaseDir, name);
	copyFileSync(src, dest);
	const hash = createHash("sha256").update(readFileSync(dest)).digest("hex");
	sums.push(`${hash}  ${name}`);
	console.log(`  ${name}  ${hash.slice(0, 12)}…`);
	collected++;
}

if (collected === 0) {
	console.error("No binaries found under build/bin/. Run scripts/build.mjs first.");
	process.exit(1);
}

writeFileSync(join(releaseDir, "SHA256SUMS"), sums.join("\n") + "\n");
console.log(`\nCollected ${collected} binary(ies) into ${releaseDir}`);
