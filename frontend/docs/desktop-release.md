# Desktop release & auto-update

The desktop app ships an in-app auto-updater (`electron-updater`). The **code** is
wired; making it **go live** needs infrastructure only the team can provision
(an Apple Developer certificate, notarization, and CI secrets). This is the
checklist.

## What already works (in this repo)

- `electron-updater` is wired in `src/main.ts` (`initAutoUpdates()`), guarded by
  `app.isPackaged` so it is a no-op in `npm run dev`.
- `package.json > build.publish` points at the GitHub Releases feed.
- `.github/workflows/frontend-release.yml` builds on a `desktop-v*` tag and runs
  `electron-builder --publish always`, which uploads the installers **and** the
  `latest-mac.yml` / `latest.yml` feed files the updater reads.

## What the team must add (auto-update is inert until these exist)

1. **Apple Developer cert + notarization** (macOS hard requirement — an unsigned
   app cannot auto-update):
   - Enroll in the Apple Developer Program.
   - Export a "Developer ID Application" certificate as a `.p12`.
   - Remove `"identity": null` from `package.json > build.mac` (it currently
     forces unsigned builds).
2. **GitHub repository secrets** (Settings → Secrets → Actions):
   - `CSC_LINK` — base64 of the `.p12` certificate.
   - `CSC_KEY_PASSWORD` — the `.p12` password.
   - `APPLE_ID`, `APPLE_APP_SPECIFIC_PASSWORD`, `APPLE_TEAM_ID` — for notarization.
   - `GITHUB_TOKEN` is provided automatically; the workflow already grants
     `contents: write` to publish the Release.
3. **(Optional) Windows / Linux** — add `win`/`linux` targets to the build config
   and matrix runners to `frontend-release.yml`; Windows signing needs its own
   certificate.

## Cutting a release

```bash
# bump frontend/package.json "version", commit, then:
git tag desktop-v0.1.0
git push origin desktop-v0.1.0
```

The workflow publishes a GitHub Release with the installers and feed files.
Installed apps check the feed on launch (`checkForUpdatesAndNotify`) and prompt
to restart when an update is downloaded.
