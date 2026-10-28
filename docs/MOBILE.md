# Mobile app (React Native / Expo)

The app and the PWA ship together from one commit. Same API, same validation, same design tokens;
only the shell differs.

## Layout

```
apps/mobile/
  app.config.ts          Expo config (bundle ids, scheme, plugins, API base per profile)
  eas.json               build + submit profiles
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
```

## Running

```bash
npm run install:mobile       # Expo deps are not part of the root workspace install
cd apps/mobile
EXPO_PUBLIC_API_URL=http://192.168.1.20:8080 npx expo start
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
`ascAppId` and Apple ID, and add the Play service-account key at `secrets/` (git-ignored). Generate
the binary icons with `apps/mobile/assets/README.md`.

## What cannot be done in this repository

This checkout has no Android SDK or Xcode installed, so there are no `.apk`/`.ipa` artefacts here:
the deliverable is **source + configuration + build profiles**, which is what a build service
consumes. `npx expo start` requires the simulator/device to be present.

## Known gaps (see BACKLOG.md)

- Push notifications for exam countdown and study reminders are not wired.
- Offline answer queueing exists in the runner (retry on reconnect) but there is no full offline
  question cache, because serving questions from a stale cache would let the client disagree with
  the server's deadline.
