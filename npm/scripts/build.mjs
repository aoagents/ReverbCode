#!/usr/bin/env node
// Cross-compile the Go daemon and assemble the npm packages.
//
// Outputs (all under npm/build/, which is gitignored):
//   build/bin/<os>-<arch>/<ao|ao.exe>     prebuilt daemon per target
//   build/packages/ao-<os>-<arch>/        per-platform sub-package (binary + package.json)
//   build/packages/ao/                    staged main package (shim + package.json + README)
//
// Usage:
//   node scripts/build.mjs                 build every target in the matrix
//   node scripts/build.mjs --host          build only the host target
//   node scripts/build.mjs --os linux --arch arm64   build one explicit target
//
// Env:
//   AO_VERSION   override the version stamped into the binary + package.json
//                (defaults to VERSION from targets.mjs)
//   AO_COMMIT    git commit to stamp (default: short HEAD if available)

import { execFileSync } from "node:child_process";
import { existsSync, mkdirSync, rmSync, copyFileSync, writeFileSync, chmodSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

import {
  targets,
  hostTarget,
  packageName,
  binaryName,
  VERSION,
  VERSION_PKG,
} from "../targets.mjs";

const here = dirname(fileURLToPath(import.meta.url));
const npmRoot = join(here, "..");
const repoRoot = join(npmRoot, "..");
const backendDir = join(repoRoot, "backend");
const buildDir = join(npmRoot, "build");
const binOut = join(buildDir, "bin");
const pkgOut = join(buildDir, "packages");

function parseArgs(argv) {
  const args = { host: false, os: null, arch: null };
  for (let i = 0; i < argv.length; i++) {
    const a = argv[i];
    if (a === "--host") args.host = true;
    else if (a === "--os") args.os = argv[++i];
    else if (a === "--arch") args.arch = argv[++i];
    else throw new Error(`unknown argument: ${a}`);
  }
  return args;
}

function selectedTargets(args) {
  if (args.host) {
    const t = hostTarget();
    if (!t) throw new Error(`unsupported host: ${process.platform}-${process.arch}`);
    return [t];
  }
  if (args.os || args.arch) {
    const t = targets.find((t) => t.os === args.os && t.arch === args.arch);
    if (!t) throw new Error(`no matrix target for ${args.os}-${args.arch}`);
    return [t];
  }
  return targets;
}

function gitCommit() {
  if (process.env.AO_COMMIT) return process.env.AO_COMMIT;
  try {
    return execFileSync("git", ["rev-parse", "--short", "HEAD"], { cwd: repoRoot })
      .toString()
      .trim();
  } catch {
    return "";
  }
}

const version = process.env.AO_VERSION || VERSION;
const commit = gitCommit();

function ldflags() {
  const parts = [`-s`, `-w`, `-X ${VERSION_PKG}.Version=${version}`];
  if (commit) parts.push(`-X ${VERSION_PKG}.Commit=${commit}`);
  return parts.join(" ");
}

function buildBinary(t) {
  const outDir = join(binOut, `${t.os}-${t.arch}`);
  mkdirSync(outDir, { recursive: true });
  const outFile = join(outDir, binaryName(t.os));
  console.log(`  go build ${t.goos}/${t.goarch} -> ${outFile}`);
  execFileSync(
    "go",
    ["build", "-trimpath", "-ldflags", ldflags(), "-o", outFile, "./cmd/ao"],
    {
      cwd: backendDir,
      stdio: "inherit",
      env: { ...process.env, GOOS: t.goos, GOARCH: t.goarch, CGO_ENABLED: "0" },
    }
  );
  return outFile;
}

function subPackageJson(t) {
  return {
    name: packageName(t.os, t.arch),
    version,
    description: `Prebuilt ao daemon for ${t.os}-${t.arch}. Installed automatically by @aoagents/ao.`,
    license: "MIT",
    homepage: "https://github.com/aoagents/ReverbCode#readme",
    repository: {
      type: "git",
      url: "git+https://github.com/aoagents/ReverbCode.git",
    },
    bugs: { url: "https://github.com/aoagents/ReverbCode/issues" },
    os: [t.os],
    cpu: [t.arch],
    files: [`bin/${binaryName(t.os)}`],
  };
}

function assembleSubPackage(t, binaryPath) {
  const dir = join(pkgOut, `ao-${t.os}-${t.arch}`);
  rmSync(dir, { recursive: true, force: true });
  mkdirSync(join(dir, "bin"), { recursive: true });

  const dest = join(dir, "bin", binaryName(t.os));
  copyFileSync(binaryPath, dest);
  // npm preserves file modes in the tarball; the daemon must be executable.
  chmodSync(dest, 0o755);

  writeFileSync(join(dir, "package.json"), JSON.stringify(subPackageJson(t), null, 2) + "\n");
  console.log(`  assembled ${packageName(t.os, t.arch)} -> ${dir}`);
  return dir;
}

function stageMainPackage() {
  const dir = join(pkgOut, "ao");
  rmSync(dir, { recursive: true, force: true });
  mkdirSync(join(dir, "bin"), { recursive: true });
  copyFileSync(join(npmRoot, "bin", "ao.js"), join(dir, "bin", "ao.js"));
  copyFileSync(join(npmRoot, "package.json"), join(dir, "package.json"));
  const readme = join(npmRoot, "README.md");
  if (existsSync(readme)) copyFileSync(readme, join(dir, "README.md"));
  console.log(`  staged @aoagents/ao -> ${dir}`);
  return dir;
}

function main() {
  const args = parseArgs(process.argv.slice(2));
  const sel = selectedTargets(args);

  mkdirSync(buildDir, { recursive: true });
  console.log(`Building @aoagents/ao ${version}${commit ? ` (${commit})` : ""}`);
  console.log(`Targets: ${sel.map((t) => `${t.os}-${t.arch}`).join(", ")}\n`);

  console.log("Compiling daemon binaries:");
  for (const t of sel) {
    const bin = buildBinary(t);
    assembleSubPackage(t, bin);
  }

  console.log("\nStaging main package:");
  stageMainPackage();

  console.log("\nDone. Tarballs can be produced with: node scripts/pack.mjs");
}

main();
