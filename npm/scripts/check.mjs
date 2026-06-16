#!/usr/bin/env node
// Guard that the committed main package.json stays in lockstep with the
// platform matrix in targets.mjs. Run in CI and locally before publishing.
//
// Checks:
//   - main package.json version === VERSION
//   - optionalDependencies lists exactly one entry per matrix target
//   - every optionalDependency is pinned to VERSION
//
// Usage:
//   node scripts/check.mjs

import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

import { targets, packageName, VERSION } from "../targets.mjs";

const here = dirname(fileURLToPath(import.meta.url));
const npmRoot = join(here, "..");

const pkg = JSON.parse(readFileSync(join(npmRoot, "package.json"), "utf8"));
const errors = [];

if (pkg.version !== VERSION) {
  errors.push(`package.json version ${pkg.version} != targets.mjs VERSION ${VERSION}`);
}

const expected = new Set(targets.map((t) => packageName(t.os, t.arch)));
const actual = new Set(Object.keys(pkg.optionalDependencies || {}));

for (const name of expected) {
  if (!actual.has(name)) errors.push(`optionalDependencies missing matrix target: ${name}`);
}
for (const name of actual) {
  if (!expected.has(name)) errors.push(`optionalDependencies has non-matrix entry: ${name}`);
}
for (const [name, range] of Object.entries(pkg.optionalDependencies || {})) {
  if (range !== VERSION) {
    errors.push(`optionalDependencies["${name}"] = "${range}", expected exact "${VERSION}"`);
  }
}

if (errors.length) {
  console.error("Packaging consistency check FAILED:");
  for (const e of errors) console.error(`  - ${e}`);
  process.exit(1);
}

console.log(`Packaging consistency OK (${targets.length} targets @ ${VERSION}).`);
