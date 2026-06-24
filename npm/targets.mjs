// Single source of truth for the npm packaging platform matrix.
//
// Each target maps an npm host identity (npm `os`/`cpu`, which use Node's
// process.platform/process.arch values) to the Go cross-compile identity
// (GOOS/GOARCH). The per-platform sub-package name is
// `@aoagents/ao-${os}-${arch}` and the `ao` launcher shim resolves exactly
// that name at runtime, so these strings are load-bearing on both ends.

export const SCOPE = "@aoagents";
export const MAIN_PACKAGE = `${SCOPE}/ao`;

// Keep in lockstep with the main package.json `version` and the
// `optionalDependencies` it lists. `scripts/check.mjs` enforces this.
export const VERSION = "0.10.0";

// Go ldflags target for build metadata (matches backend/internal/cli/version.go).
export const VERSION_PKG = "github.com/aoagents/agent-orchestrator/backend/internal/cli";

export const targets = [
	{ os: "darwin", arch: "arm64", goos: "darwin", goarch: "arm64" },
	{ os: "darwin", arch: "x64", goos: "darwin", goarch: "amd64" },
	{ os: "linux", arch: "x64", goos: "linux", goarch: "amd64" },
	{ os: "linux", arch: "arm64", goos: "linux", goarch: "arm64" },
	{ os: "win32", arch: "x64", goos: "windows", goarch: "amd64" },
	{ os: "win32", arch: "arm64", goos: "windows", goarch: "arm64" },
];

// Package name for a given target (or the host, given process.platform/arch).
export function packageName(os, arch) {
	return `${SCOPE}/ao-${os}-${arch}`;
}

// Binary file name shipped inside a sub-package's `bin/` directory.
export function binaryName(os) {
	return os === "win32" ? "ao.exe" : "ao";
}

// The target matching the current host, or undefined if unsupported.
export function hostTarget() {
	return targets.find((t) => t.os === process.platform && t.arch === process.arch);
}
