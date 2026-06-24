#!/usr/bin/env node
"use strict";

// `ao` launcher shim for @aoagents/ao.
//
// The npm tarball ships no binary itself. The prebuilt Go daemon for the host
// arrives via one of the `@aoagents/ao-<os>-<arch>` optionalDependencies — npm
// installs only the one matching `os`/`cpu`. This shim resolves that package,
// locates its binary, and execs it, forwarding argv, stdio, exit code, and
// terminating signals. The CLI verbs behind it talk to the daemon over
// loopback exactly as the native binary does.

const path = require("path");
const { spawnSync } = require("child_process");

const platform = process.platform; // darwin | linux | win32
const arch = process.arch; // arm64 | x64
const pkgName = `@aoagents/ao-${platform}-${arch}`;
const binName = platform === "win32" ? "ao.exe" : "ao";

const SUPPORTED = ["darwin-arm64", "darwin-x64", "linux-x64", "linux-arm64", "win32-x64", "win32-arm64"];

function resolveBinary() {
	// Resolve the sub-package via its package.json (always resolvable; avoids
	// extension/exports edge cases of resolving the binary file directly), then
	// join the known bin path next to it.
	let pkgJsonPath;
	try {
		pkgJsonPath = require.resolve(`${pkgName}/package.json`);
	} catch {
		return null;
	}
	return path.join(path.dirname(pkgJsonPath), "bin", binName);
}

const binary = resolveBinary();
if (!binary) {
	process.stderr.write(
		`ao: no prebuilt binary found for ${platform}-${arch}.\n` +
			`\n` +
			`Expected the optional dependency "${pkgName}" to be installed alongside\n` +
			`@aoagents/ao, but it was not found. This usually means either:\n` +
			`  - your platform is not supported, or\n` +
			`  - the package was installed with optional dependencies disabled\n` +
			`    (e.g. "npm install --omit=optional" / "--no-optional", or a CI cache\n` +
			`    that dropped optionalDependencies).\n` +
			`\n` +
			`Supported platforms: ${SUPPORTED.join(", ")}.\n` +
			`\n` +
			`Reinstall with optional dependencies enabled:\n` +
			`  npm install -g @aoagents/ao\n`,
	);
	process.exit(1);
}

const result = spawnSync(binary, process.argv.slice(2), { stdio: "inherit" });

if (result.error) {
	if (result.error.code === "ENOENT") {
		process.stderr.write(
			`ao: platform package "${pkgName}" is installed but its binary is missing\n` +
				`    at ${binary}. Try reinstalling: npm install -g @aoagents/ao\n`,
		);
	} else {
		process.stderr.write(`ao: failed to launch ${binary}: ${result.error.message}\n`);
	}
	process.exit(1);
}

// Re-raise a terminating signal so the parent shell observes the right cause of
// death (e.g. Ctrl-C). Otherwise propagate the child's exit code verbatim.
if (result.signal) {
	process.kill(process.pid, result.signal);
	// If the re-raise did not terminate us, fall through to a non-zero exit.
	process.exit(1);
}

process.exit(result.status === null ? 1 : result.status);
