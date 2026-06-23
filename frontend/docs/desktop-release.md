# Desktop release & auto-update

The desktop app ships an in-app auto-updater (`update-electron-app`). The **code**
is wired; making it **go live** needs infrastructure only the team can provision
(an Apple Developer certificate, notarization, and CI secrets). This is the
checklist.

## What already works (in this repo)

- `update-electron-app` is wired in `src/main.ts` (`initAutoUpdates()`), guarded
  by `app.isPackaged` so it is a no-op in `npm run dev`. It reads the GitHub
  Releases feed directly via the Releases API — no `latest-mac.yml` files needed.
- `forge.config.ts > publishers` uses `@electron-forge/publisher-github`, pointed
  at the GitHub Releases feed (draft releases by default).
- `.github/workflows/frontend-release.yml` builds on a `desktop-v*` tag and runs
  `npm run publish` (`electron-forge publish`), which makes the installers and
  uploads them to a GitHub Release.

## What the team must add (auto-update is inert until these exist)

1. **Apple Developer cert + notarization** (macOS hard requirement — an unsigned
   app cannot auto-update):
   - Enroll in the Apple Developer Program.
   - Export a "Developer ID Application" certificate as a `.p12`.
   - Signing is already gated in `forge.config.ts` on the env vars below:
     `osxSign` activates when `CSC_LINK` is set, `osxNotarize` when `APPLE_ID`
     is set. No config edit needed — just provide the secrets.
2. **GitHub repository secrets** (Settings → Secrets → Actions):
   - `CSC_LINK` — base64 of the `.p12` certificate.
   - `CSC_KEY_PASSWORD` — the `.p12` password.
   - `APPLE_ID`, `APPLE_APP_SPECIFIC_PASSWORD`, `APPLE_TEAM_ID` — for notarization.
   - `GITHUB_TOKEN` is provided automatically; the workflow already grants
     `contents: write` to publish the Release.
3. **(Optional) Windows / Linux** — the `forge.config.ts` makers already include
   NSIS (Windows, via `makers/maker-nsis.ts`), deb, and rpm. To publish them, add
   the matching matrix runners to `frontend-release.yml`; Windows code-signing
   needs its own certificate (still a follow-up, see issue #401).

## Cutting a release

```bash
# bump frontend/package.json "version", commit, then:
git tag desktop-v0.1.0
git push origin desktop-v0.1.0
```

The workflow publishes a GitHub Release with the installers. Installed apps check
the Releases feed on launch (`update-electron-app`) and prompt to restart when an
update is downloaded.
