# Mobile app (React Native / Expo)

The app and the PWA ship together from one commit. Same API, same validation, same design tokens;
only the shell differs.

## Layout

```
apps/mobile/
  app.config.ts          Expo config (bundle ids, scheme, plugins, API base per profile)
  eas.json               build + submit profiles
  metro.config.js        watches packages/shared (a symlink outside the app root)
  assets/                icon, adaptive icon, splash, favicon (generated)
  app/
    _layout.tsx          stack + AuthProvider
    index.tsx            session restore → tabs or sign-in
    signin.tsx
    signup/              five separate screens, mirroring the web wizard
    (tabs)/              index (home), practice, progress, account
    attempt/[id].tsx     attempt runner (countdown, save states, submit)
    results/[id].tsx     result + review
  src/api.ts             ApiClient + secure token storage + signup draft store
  src/auth.tsx           session context
  src/components/ui.tsx  themed primitives (44pt targets, accessible roles)
  src/theme.ts           tokens re-exported from @edumart/shared
scripts/
  render-assets.py       draws the icons + splash with the standard library only
  smoke.py               HTTP journey shared with CI
```

## Running

```bash
npm run install:mobile       # Expo deps are not part of the root workspace install
npm run dev:mobile           # expo start
# or, with an explicit API host for a physical device:
cd apps/mobile && EXPO_PUBLIC_API_URL=http://192.168.1.20:8080 npx expo start
```

On a physical device `localhost` is the phone, not your laptop — use your machine's LAN address (or
a tunnel). The API already binds `0.0.0.0` and allows the Expo origin.

## Authentication difference

React Native has no httpOnly cookie jar, so the app keeps the same opaque session token in
`expo-secure-store` and sends `Authorization: Bearer …`. Revoking the session server-side
(`POST /v1/auth/logout`, or a password reset) invalidates both shells at once.

## Building

```bash
npm run build:preview --workspace @edumart/mobile     # internal distribution, all platforms
npm run build:production --workspace @edumart/mobile  # store builds
```

Profiles in `eas.json`:

| Profile | API base | Purpose |
| --- | --- | --- |
| `development` | `http://localhost:8080` | Dev client against a local API |
| `preview` | `https://api.staging.edumart.ng` | Internal QA on real devices |
| `production` | `https://api.edumart.ng` | Store submission |

Before the first store build, replace in `app.config.ts`/`eas.json`: the EAS `projectId`, the iOS
`ascAppId` and Apple ID, and add the Play service-account key at `secrets/` (git-ignored). The icons
and splash are already committed; regenerate them from the shared mark with `npm run assets`.

## What is verified here, and what is not

`npx expo export` bundles the app for Android, iOS and web from this checkout, so the JavaScript and
every native module import are known to resolve — CI runs that for all three platforms on every
push. What this repository cannot produce is a signed binary: there is no Android SDK, Xcode or
signing identity here, so `.apk`/`.aab`/`.ipa` artefacts come from EAS using `eas.json`. A
simulator or device is still needed to click through the UI.

## Known gaps (see BACKLOG.md)

- Push notifications for exam countdown and study reminders are not wired.
- Offline answer queueing exists in the runner (retry on reconnect) but there is no full offline
  question cache, because serving questions from a stale cache would let the client disagree with
  the server's deadline.
