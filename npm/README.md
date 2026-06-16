# @aoagents/ao

Agent Orchestrator: a local daemon that supervises parallel coding-agent
sessions, driven by the `ao` CLI.

```bash
npm install -g @aoagents/ao
ao start        # start the loopback daemon
ao status       # talk to it
```

## How the binary gets onto your machine

The published `@aoagents/ao` tarball ships **no** binary. The prebuilt Go daemon
(~20 MB, per platform) is delivered through per-platform
`optionalDependencies` (the esbuild/swc pattern):

| optional dependency        | `os`     | `cpu`   |
| -------------------------- | -------- | ------- |
| `@aoagents/ao-darwin-arm64`| darwin   | arm64   |
| `@aoagents/ao-darwin-x64`  | darwin   | x64     |
| `@aoagents/ao-linux-x64`   | linux    | x64     |
| `@aoagents/ao-linux-arm64` | linux    | arm64   |
| `@aoagents/ao-win32-x64`   | win32    | x64     |
| `@aoagents/ao-win32-arm64` | win32    | arm64   |

npm installs **only** the one matching your host. The `ao` command is a thin
Node shim (`bin/ao.js`) that resolves the installed sub-package, finds its
`bin/ao` (`bin/ao.exe` on Windows), and execs it, forwarding argv, stdio, exit
code, and signals. There is no `postinstall` and no install-time network call.

If no matching sub-package is present (unsupported platform, or installed with
`--omit=optional`), the shim exits 1 with a clear message.

> Only the Go daemon ships via npm. The Electron desktop app is fetched from
> GitHub Releases during migration and is out of scope for this package.

## Maintainer workflow

The matrix is defined once in [`targets.mjs`](./targets.mjs). The committed
`package.json` `optionalDependencies` must stay in lockstep with it; CI and
`scripts/check.mjs` enforce that.

```bash
# Validate that package.json matches the matrix.
node scripts/check.mjs

# Cross-compile + assemble every platform sub-package and stage the main package.
node scripts/build.mjs            # all targets (pure-Go, CGO disabled, cross-compiles anywhere)
node scripts/build.mjs --host     # only the host target (fast local smoke test)

# Produce .tgz tarballs from whatever was staged.
node scripts/pack.mjs             # -> npm/build/tarballs/*.tgz
```

Everything under `npm/build/` is generated and gitignored.

### Local smoke test

```bash
node scripts/build.mjs --host && node scripts/pack.mjs
prefix="$(mktemp -d)"
npm install -g --prefix "$prefix" \
  npm/build/tarballs/aoagents-ao-*.tgz \
  npm/build/tarballs/aoagents-ao-$(node -p "process.platform")-$(node -p "process.arch")-*.tgz
"$prefix/bin/ao" version      # execs the host Go binary -> 0.10.0
```

### Release & publish

- `.github/workflows/release.yml` (on a `v*` tag): cross-compiles every target,
  publishes the raw binaries to the GitHub Release, builds the npm sub-packages
  from those binaries, and runs `npm publish --dry-run` as a verification gate.
- `.github/workflows/npm-publish.yml` (manual `workflow_dispatch`): builds the
  packages and publishes. It defaults to a **dry run**; a real publish requires
  `dry_run=false` **and** an `NPM_TOKEN` secret, neither of which is wired by
  default. Publishing is a deliberate human action.
