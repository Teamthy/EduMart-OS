# App assets

Binary icons live here because store builds need real PNGs. They are generated from the same mark
as `apps/web/public/icon.svg` by `scripts/render-assets.py`, which needs nothing but Python 3:

```bash
python3 scripts/render-assets.py
```

| File | Size | Purpose |
| --- | --- | --- |
| `icon.png` | 1024×1024 | App icon (opaque — the OS applies its own mask) |
| `adaptive-icon.png` | 1024×1024 | Android adaptive foreground, transparent, inside the 66% safe zone |
| `splash.png` | 1284×2778 | Splash, mark on the paper background |
| `favicon.png` | 48×48 | Expo web favicon |

Edit colours in `scripts/render-assets.py` together with `packages/shared/src/tokens.ts` and the
`splash.backgroundColor` / `adaptiveIcon.backgroundColor` values in `app.config.ts`; the mark and
the web icon are meant to stay indistinguishable.
