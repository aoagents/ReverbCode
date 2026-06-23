# Desktop release & auto-update

The desktop app ships an in-app auto-updater (`update-electron-app`). The **code**
is fully wired (signing config included). What is left is **credentials**: an
Apple Developer ID identity plus a notarization key that only the account holder
can grant. This doc is the checklist and the handoff for that remaining work.

## Status at a glance

| Piece                                                   | State                                           |
| ------------------------------------------------------- | ----------------------------------------------- |
| Auto-updater code (`update-electron-app`)               | Done                                            |
| GitHub Releases publisher + release workflow            | Done                                            |
| macOS signing + notarization config (`forge.config.ts`) | Done, **blocked on credentials**                |
| macOS entitlements (app + bundled `ao` daemon)          | Done                                            |
| Apple Developer ID identity in keychain                 | **TODO (you, after holder sends the .cer)**     |
| `ao-notary` notarytool profile on your machine          | **TODO (you, after holder sends the API key)**  |
| Windows signing                                         | **Intentionally skipped** (see "Windows" below) |
| Linux signing                                           | Not required                                    |

## What already works (in this repo)

- `update-electron-app` is wired in `src/main.ts` (`initAutoUpdates()`), guarded
  by `app.isPackaged` so it is a no-op in `npm run dev`. It reads the GitHub
  Releases feed directly via the Releases API (no `latest-mac.yml` files needed).
- `forge.config.ts > publishers` uses `@electron-forge/publisher-github`, pointed
  at the GitHub Releases feed (draft releases by default).
- `.github/workflows/frontend-release.yml` builds on a `desktop-v*` tag and runs
  `npm run publish` (`electron-forge publish`), which makes the installers and
  uploads them to a GitHub Release.
- **macOS signing + notarization is wired** in `forge.config.ts` and **inert
  until the env vars below are set**, so dev builds are unaffected. The bundled
  `ao` daemon (a standalone Go binary at `Contents/Resources/daemon/ao`) is
  signed with hardened runtime via `assets/entitlements.daemon.plist`; the
  Electron app uses `assets/entitlements.mac.plist`.

## macOS: what is left (blocked on credentials)

The signing model is **delegated**: the Apple account holder validates identity
and grants two artifacts, and from then on you sign and notarize entirely on your
own Mac without ever logging into their account. Two separate permission systems
are involved (Apple and npm); they do not touch each other.

### From the Apple account holder, you need three things

1. A **Developer ID Application** certificate (`.cer`), obtained via the CSR flow
   so the private key never leaves your machine:
   - You: Keychain Access > Certificate Assistant > **Request a Certificate from
     a Certificate Authority** > Saved to disk. This makes a `.certSigningRequest`
     and quietly a private key that stays in your keychain.
   - Holder: developer.apple.com > Certificates > + > **Developer ID Application**
     > upload your CSR > download the `.cer`.
   - You: double-click the `.cer` to install. It pairs with the private key
     already in your keychain. (Developer ID certs are limited in number, so
     coordinate so the holder does not burn them.)
2. An **App Store Connect API key** for notarization (not tied to the holder's
   personal login, revocable on its own):
   - Holder: App Store Connect > Users and Access > Integrations > **App Store
     Connect API** > generate a key with Developer (or Admin) access.
   - This yields an **Issuer ID**, a **Key ID**, and a one-time `.p8` download.
     The `.p8` is the secret; send it through a password manager, not email.
3. The **Team ID** (10-character code under Membership on developer.apple.com).
   Not secret.

### Then, one-time setup on your Mac

```bash
# 1. Confirm the Developer ID identity is in your keychain after installing the .cer:
security find-identity -v -p codesigning
#    Copy the full string, e.g. "Developer ID Application: Acme Inc (TEAMID1234)".

# 2. Store the notarization API key as a reusable profile:
xcrun notarytool store-credentials "ao-notary" \
  --key /path/to/AuthKey_XXXX.p8 \
  --key-id THE_KEY_ID \
  --issuer THE_ISSUER_ID

# 3. Verify the profile authenticates (empty history is fine, an auth error is not):
xcrun notarytool history --keychain-profile ao-notary
```

### Then every release is self-service

```bash
export APPLE_SIGNING_IDENTITY="Developer ID Application: Acme Inc (TEAMID1234)"
export AO_NOTARY_PROFILE="ao-notary"
cd frontend && npm run make
# forge signs with the keychain identity, signs the bundled ao daemon with
# hardened runtime, notarizes via the ao-notary profile, and staples the result.
```

The publisher shown to end users is the **holder's** name (it is their cert).
That is normal and correct.

### How the config consumes the env vars

`forge.config.ts` resolves credentials in priority order, so the same config
works locally and in CI:

- **Signing:** `APPLE_SIGNING_IDENTITY` (local keychain identity) is preferred;
  `CSC_LINK` is a CI fallback (cert pre-imported into a runner keychain).
- **Notarization:** `AO_NOTARY_PROFILE` (your stored profile) is preferred, then
  the raw API key (`APPLE_API_KEY` + `APPLE_API_KEY_ID` + `APPLE_API_ISSUER`) for
  CI, then the legacy `APPLE_ID` + `APPLE_APP_SPECIFIC_PASSWORD` + `APPLE_TEAM_ID`.

For CI, add the chosen set as GitHub Actions secrets (Settings > Secrets >
Actions). `GITHUB_TOKEN` is provided automatically and the workflow already
grants `contents: write` to publish the Release.

## npm publish (separate from Apple)

Publishing `@aoagents/ao` needs access to the `@aoagents` npm scope, granted by
the org owner: either org membership with publish rights, or (better for CI) a
**granular access token** scoped to the `@aoagents` packages with read+write. An
automation token is what lets a pipeline publish without a human tapping 2FA.
Nothing about Apple affects this.

## Windows: shipping unsigned (deliberate)

Windows builds ship **unsigned** for now. This is a deliberate decision, not an
oversight:

- Unlike macOS (where Squirrel.Mac refuses to auto-update an unsigned app),
  **Squirrel.Windows auto-updates fine unsigned**, so the in-app updater keeps
  working.
- The only cost is a first-run SmartScreen prompt ("Unknown publisher",
  More info > Run anyway), which is mild friction for a developer audience.
- **Azure Trusted Signing** (the clean, cheap, auto-rotating path) is **not
  available** to AO: it requires a US/Canada/EU/UK organization with 3+ years of
  verifiable history. The only alternative is a CA-issued OV/EV cert on a
  hardware token/HSM (~$200-700/yr plus a Windows signing host), which is not
  worth it at current install volume.

**Upgrade trigger:** revisit Windows signing when install volume makes the
SmartScreen drop-off measurable, or when the org becomes Trusted-Signing
eligible. Wiring `windowsSign` into the Squirrel maker is then a ~10-minute job.

## Cutting a release

```bash
# bump frontend/package.json "version", commit, then:
git tag desktop-v0.1.0
git push origin desktop-v0.1.0
```

The workflow publishes a GitHub Release with the installers. Installed apps check
the Releases feed on launch (`update-electron-app`) and prompt to restart when an
update is downloaded.
