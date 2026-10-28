#!/usr/bin/env python3
"""Render the Edumart app icons and splash from the same geometry as the web mark.

The web app uses `apps/web/public/icon.svg`; store builds need real PNGs. This script draws the
mark directly (signed-distance shapes, supersampled edges) so the repository can regenerate every
binary asset with only the Python standard library:

    python3 scripts/render-assets.py

Colours come from `packages/shared/src/tokens.ts` so the two shells cannot drift.
"""

import math
import struct
import zlib
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
OUT = ROOT / "apps" / "mobile" / "assets"

INK = (6, 48, 31)
GREEN = (26, 154, 115)
GOLD = (231, 178, 61)
CREAM = (248, 247, 244)


def mix(a, b, t):
    return tuple(round(x + (y - x) * t) for x, y in zip(a, b))


def clamp(v, lo=0.0, hi=1.0):
    return lo if v < lo else hi if v > hi else v


def smoothstep(edge0, edge1, x):
    t = clamp((x - edge0) / (edge1 - edge0))
    return t * t * (3 - 2 * t)


def sd_round_rect(px, py, cx, cy, hw, hh, r):
    qx = abs(px - cx) - (hw - r)
    qy = abs(py - cy) - (hh - r)
    outside = math.hypot(max(qx, 0.0), max(qy, 0.0))
    inside = min(max(qx, qy), 0.0)
    return outside + inside - r


def sd_parallelogram(px, py, cx, cy, hw, hh, skew):
    x = px - cx - skew * (py - cy)
    qx = abs(x) - hw
    qy = abs(py - cy) - hh
    return math.hypot(max(qx, 0.0), max(qy, 0.0)) + min(max(qx, qy), 0.0)


def cover(sd, px_size):
    """Coverage in [0,1] from a signed distance, one pixel of anti-aliasing."""
    return clamp(0.5 - sd / px_size)


def over(dst, src, alpha):
    return tuple(round(d + (s - d) * alpha) for d, s in zip(dst, src))


def sd_trapezoid(px, py, cx, cy, hh, top_hw, bottom_hw, r):
    """Signed distance to a symmetric trapezoid (rounded by r along its top/bottom edges)."""
    dy = (py - cy) / hh
    t = clamp((dy + 1.0) * 0.5)
    hw = top_hw + (bottom_hw - top_hw) * t
    qx = abs(px - cx) - (hw - r)
    qy = abs(py - cy) - (hh - r)
    return math.hypot(max(qx, 0.0), max(qy, 0.0)) + min(max(qx, qy), 0.0) - r


def draw_mark(size, *, transparent=False, mark_scale=1.0):
    """The Edumart mark: a gold graduation cap above three cream rules, on the brand gradient."""
    px = size / 1024.0
    rows = []
    cx = cy = size / 2.0
    s = mark_scale
    for y in range(size):
        row = bytearray()
        for x in range(size):
            t = clamp((x + y) / (2.0 * size))
            colour = mix(INK, mix(INK, GREEN, 0.7), 0.35 + 0.5 * t)
            alpha_bg = 0.0 if transparent else 1.0

            # Cap crown: wide skewed slab.
            crown = sd_parallelogram(x, y, cx, cy - 150 * px * s, 340 * px * s, 52 * px * s, 0.40)
            a = cover(crown, 1.0)
            colour = over(colour, GOLD, a)
            if transparent:
                alpha_bg = max(alpha_bg, a)

            # Cap body: trapezoid hanging from the crown.
            body = sd_trapezoid(x, y, cx, cy - 2 * px * s, 96 * px * s, 190 * px * s, 250 * px * s, 26 * px * s)
            a = cover(body, 1.0)
            colour = over(colour, GOLD, a)
            if transparent:
                alpha_bg = max(alpha_bg, a)

            # Tassel: stem plus bead on the right-hand side of the crown.
            stem = sd_round_rect(x, y, cx + 316 * px * s, cy - 30 * px * s, 10 * px * s, 92 * px * s, 10 * px * s)
            a = cover(stem, 1.0)
            colour = over(colour, CREAM, a)
            if transparent:
                alpha_bg = max(alpha_bg, a)
            bead = math.hypot(x - (cx + 316 * px * s), y - (cy + 84 * px * s)) - 26 * px * s
            a = cover(bead, 1.0)
            colour = over(colour, CREAM, a)
            if transparent:
                alpha_bg = max(alpha_bg, a)

            # Three cream rules: the "study" note under the cap.
            for index, width in enumerate((300.0, 230.0, 160.0)):
                line_y = cy + 210 * px * s + index * 78 * px * s
                rule = sd_round_rect(x, y, cx, line_y, width * px * s, 18 * px * s, 18 * px * s)
                a = cover(rule, 1.0) * 0.92
                colour = over(colour, CREAM, a)
                if transparent:
                    alpha_bg = max(alpha_bg, a)

            row += bytes((colour[0], colour[1], colour[2], round(clamp(alpha_bg) * 255)))
        rows.append(bytes(row))
    return rows


def write_png(path, width, height, rows):
    raw = b"".join(b"\x00" + row for row in rows)

    def chunk(tag, data):
        body = tag + data
        return struct.pack(">I", len(data)) + body + struct.pack(">I", zlib.crc32(body) & 0xFFFFFFFF)

    header = struct.pack(">IIBBBBB", width, height, 8, 6, 0, 0, 0)
    png = b"\x89PNG\r\n\x1a\n" + chunk(b"IHDR", header) + chunk(b"IDAT", zlib.compress(raw, 9)) + chunk(b"IEND", b"")
    path.write_bytes(png)
    print(f"wrote {path.relative_to(ROOT)} ({width}x{height}, {len(png) // 1024} KiB)")


def write_splash(path, width, height):
    """Paper background with the mark centred at a readable size."""
    mark_size = 620
    mark = draw_mark(mark_size)
    rows = []
    top = (height - mark_size) // 2
    left = (width - mark_size) // 2
    for y in range(height):
        row = bytearray()
        for x in range(width):
            r, g, b = CREAM
            if top <= y < top + mark_size and left <= x < left + mark_size:
                mr, mg, mb, ma = mark[y - top][(x - left) * 4 : (x - left) * 4 + 4]
                # Round the mark's own corners so the splash does not show a hard square.
                corner = sd_round_rect(x - left, y - top,
                                       mark_size / 2, mark_size / 2,
                                       mark_size / 2, mark_size / 2, mark_size * 0.21)
                alpha = (ma / 255.0) * clamp(0.5 - corner)
                r, g, b = (round(c + (m - c) * alpha) for c, m in ((r, mr), (g, mg), (b, mb)))
            row += bytes((r, g, b, 255))
        rows.append(bytes(row))
    write_png(path, width, height, rows)


def main():
    OUT.mkdir(parents=True, exist_ok=True)
    write_png(OUT / "icon.png", 1024, 1024, draw_mark(1024))
    write_png(OUT / "adaptive-icon.png", 1024, 1024, draw_mark(1024, transparent=True, mark_scale=0.66))
    write_png(OUT / "favicon.png", 48, 48, draw_mark(48))
    write_splash(OUT / "splash.png", 1284, 2778)


if __name__ == "__main__":
    main()
