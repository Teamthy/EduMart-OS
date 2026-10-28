# App assets

Binary icons are generated from the sources below at build time, so the repository stays small and
both shells stay visually identical.

| File | Size | Purpose |
| --- | --- | --- |
| `icon.png` | 1024×1024 | App icon (opaque, no rounded corners — the OS masks it) |
| `adaptive-icon.png` | 1024×1024 | Android adaptive foreground, transparent, inside a 66% safe zone |
| `splash.png` | 1284×2778 | Splash, background `#f8f7f4` |
| `favicon.png` | 48×48 | Expo web favicon |

Regenerate from the web app's `public/icon.svg`:

```bash
# requires ImageMagick
for size in 1024 512 192 48; do
  magick -background none public/icon.svg -resize ${size}x${size} apps/mobile/assets/icon-${size}.png
done
cp apps/mobile/assets/icon-1024.png apps/mobile/assets/icon.png
cp apps/mobile/assets/icon-1024.png apps/mobile/assets/adaptive-icon.png
cp apps/mobile/assets/icon-48.png apps/mobile/assets/favicon.png
```

The splash uses the wordmark centred on the paper background; keep `backgroundColor` in
`app.config.ts` in sync with `palette.paper` in `packages/shared`.
