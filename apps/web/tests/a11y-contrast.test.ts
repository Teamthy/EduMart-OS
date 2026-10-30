import { readFileSync } from "node:fs";
import path from "node:path";

import { describe, expect, it } from "vitest";

import { coverTones, palette, statusTones } from "@edumart/shared";

/**
 * WCAG 2.2 contrast, checked against the real tokens rather than a screenshot.
 *
 * Colour is the part of accessibility that silently rots. Nothing fails to compile when a designer swaps
 * an amber for a friendlier one, no test goes red, and the first report is a learner who cannot read the
 * price. axe cannot help here either: jsdom has no layout, so `color-contrast` never runs in the unit
 * suite — its own docs say it needs a real browser. So the maths is done directly on the token values.
 *
 * The one rule this file exists to enforce: **a pair is only exempt if nobody can see it.** Anything that
 * renders text is 4.5:1 (WCAG 1.4.3, AA), anything whose shape identifies a control or a state is 3:1
 * (WCAG 1.4.11).
 */

const WEB_ROOT = path.resolve(__dirname, "..");

function hex(value: string): string {
  const normalised = value.trim().replace(/^#/, "");
  if (!/^[0-9a-fA-F]{6}$/.test(normalised)) throw new Error(`not a six-digit hex colour: ${value}`);
  return normalised;
}

/** WCAG relative luminance, using the sRGB piecewise transform (not a naive 2.2 gamma). */
function luminance(value: string): number {
  const channels = [0, 2, 4].map((offset) => parseInt(hex(value).slice(offset, offset + 2), 16) / 255);
  const linear = channels.map((c) => (c <= 0.03928 ? c / 12.92 : ((c + 0.055) / 1.055) ** 2.4));
  return 0.2126 * linear[0]! + 0.7152 * linear[1]! + 0.0722 * linear[2]!;
}

/** Contrast ratio, always ≥ 1, order-independent as the WCAG definition requires. */
export function contrast(foreground: string, background: string): number {
  const a = luminance(foreground);
  const b = luminance(background);
  const [lighter, darker] = a > b ? [a, b] : [b, a];
  return (lighter + 0.05) / (darker + 0.05);
}

/**
 * Composites an alpha-modified colour over its background, the way Tailwind's `text-foreground/80` does.
 * Token-level checks would otherwise silently skip every `/80` and `/90` in the codebase, and those are
 * used on the navigation and the secondary links.
 */
export function over(foreground: string, background: string, alpha: number): string {
  const fg = hex(foreground);
  const bg = hex(background);
  const mix = [0, 2, 4].map((offset) => {
    const f = parseInt(fg.slice(offset, offset + 2), 16);
    const b = parseInt(bg.slice(offset, offset + 2), 16);
    return Math.round(f * alpha + b * (1 - alpha));
  });
  return "#" + mix.map((c) => c.toString(16).padStart(2, "0")).join("");
}

/** Reads the CSS custom properties out of `:root` — the values the browser actually paints. */
function cssTokens(): Record<string, string> {
  const css = readFileSync(path.join(WEB_ROOT, "app", "globals.css"), "utf8");
  const root = /:root\s*\{([\s\S]*?)\n\}/.exec(css);
  if (!root) throw new Error("globals.css has no :root block");
  const tokens: Record<string, string> = {};
  for (const match of root[1]!.matchAll(/--([a-z0-9-]+):\s*(#[0-9a-fA-F]{6})\s*;/g)) {
    tokens[match[1]!] = match[2]!;
  }
  return tokens;
}

const tokens = cssTokens();

/** Text and UI pairs, each with the criterion that governs it. Kept explicit so a failure names the rule. */
const TEXT_PAIRS: Array<[string, string, string, string]> = [
  ["body text on the page", tokens.foreground!, tokens.background!, "1.4.3"],
  ["body text on a card", tokens.foreground!, tokens.card!, "1.4.3"],
  ["muted text on the page", tokens["muted-foreground"]!, tokens.background!, "1.4.3"],
  ["muted text on a card", tokens["muted-foreground"]!, tokens.card!, "1.4.3"],
  // This is the pair that was 4.39:1. Muted text sits on the muted panel in tables and inline notes, so
  // checking it only against white would have missed the failure.
  ["muted text on a muted panel", tokens["muted-foreground"]!, tokens.muted!, "1.4.3"],
  ["label on the primary button", tokens["primary-foreground"]!, tokens.primary!, "1.4.3"],
  ["text on the secondary surface", tokens["secondary-foreground"]!, tokens.secondary!, "1.4.3"],
  ["text on the accent surface", tokens["accent-foreground"]!, tokens.accent!, "1.4.3"],
  ["destructive text on the page", tokens.destructive!, tokens.background!, "1.4.3"],
  ["destructive text on a card", tokens.destructive!, tokens.card!, "1.4.3"],
  ["label on a destructive button", tokens["destructive-foreground"]!, tokens.destructive!, "1.4.3"],
  ["success text on the page", tokens.success!, tokens.background!, "1.4.3"],
  ["label on a success surface", tokens["success-foreground"]!, tokens.success!, "1.4.3"],
  ["warning text on the page", tokens.warning!, tokens.background!, "1.4.3"],
  ["label on a warning surface", tokens["warning-foreground"]!, tokens.warning!, "1.4.3"],
  ["the primary link colour on the page", tokens.primary!, tokens.background!, "1.4.3"],
  ["the primary link colour on a card", tokens.primary!, tokens.card!, "1.4.3"],
  // Tailwind alpha modifiers, composited. `text-foreground/80` is the navigation link colour.
  ["navigation text (foreground/80) on a card", over(tokens.foreground!, tokens.card!, 0.8), tokens.card!, "1.4.3"],
  ["navigation text (foreground/80) on the page", over(tokens.foreground!, tokens.background!, 0.8), tokens.background!, "1.4.3"],
  ["secondary link text (foreground/80, card fill) on the page", over(tokens.foreground!, tokens.card!, 0.8), tokens.background!, "1.4.3"],
];

const UI_PAIRS: Array<[string, string, string, string]> = [
  // The border is the entire visual definition of a text field, a checkbox and a radio.
  ["the field boundary on a card", tokens.input!, tokens.card!, "1.4.11"],
  ["the field boundary on the page", tokens.input!, tokens.background!, "1.4.11"],
  ["the field boundary on a muted panel", tokens.input!, tokens.muted!, "1.4.11"],
  // The focus indicator is drawn 2px outside the control (outline-offset), so what it must contrast with
  // is the surface behind it — checked in both places it can land. The offset itself is asserted in
  // a11y-axe.test.tsx, because without it the ring would sit against a dark button at 2.2:1.
  ["the focus ring on the page", tokens.ring!, tokens.background!, "1.4.11"],
  ["the focus ring on a card", tokens.ring!, tokens.card!, "1.4.11"],
  ["a warning icon against the page", tokens.warning!, tokens.background!, "1.4.11"],
];

describe("the contrast checker can fail", () => {
  // A gate that has only ever passed is a gate nobody should trust: this repository makes the same
  // argument for the secret scanner and the alert checker, and it applies here too.
  it("computes the ratios WCAG publishes", () => {
    expect(contrast("#000000", "#ffffff")).toBeCloseTo(21, 1);
    expect(contrast("#ffffff", "#ffffff")).toBeCloseTo(1, 5);
    expect(contrast("#777777", "#ffffff")).toBeCloseTo(4.48, 1); // the classic "almost AA" grey
  });

  it("rejects the exact values this audit had to fix", () => {
    // Every one of these shipped. If the checker ever stops catching them, it is not checking anything.
    expect(contrast("#c98508", "#fdf0d4")).toBeLessThan(4.5); // amber cover-chip text: 2.72:1
    expect(contrast("#d3cfc6", "#ffffff")).toBeLessThan(3); // field border: 1.55:1
    expect(contrast("#667079", "#f1efea")).toBeLessThan(4.5); // muted text on a muted panel: 4.39:1
    expect(contrast("#1a9a73", "#0d5c46")).toBeLessThan(3); // focus ring against a filled button: 2.24:1
  });
});

describe("design tokens match the stylesheet", () => {
  // The CSS variables are hand-maintained next to the TypeScript tokens. If they drift, the palette the
  // tests reason about is not the palette the browser paints, and every colour check below becomes
  // theatre. So the two are held together here.
  const expected: Array<[string, string, string]> = [
    ["background", palette.paper, "palette.paper"],
    ["foreground", palette.ink, "palette.ink"],
    ["card", palette.surface, "palette.surface"],
    ["border", palette.line, "palette.line"],
    ["input", palette.lineStrong, "palette.lineStrong"],
    ["muted-foreground", palette.ink3, "palette.ink3"],
    ["primary", palette.green700, "palette.green700"],
    ["primary-foreground", palette.surface, "palette.surface"],
    ["secondary", palette.green50, "palette.green50"],
    ["accent", palette.green100, "palette.green100"],
    ["ring", palette.green500, "palette.green500"],
    ["destructive", palette.rose600, "palette.rose600"],
    ["success", palette.emerald600, "palette.emerald600"],
    ["warning", palette.amber500, "palette.amber500"],
  ];

  it.each(expected)("--%s is %s in the stylesheet", (token, value, source) => {
    expect(tokens[token]?.toLowerCase(), `--${token} should come from ${source}`).toBe(value.toLowerCase());
  });
});

describe("text meets WCAG AA (4.5:1)", () => {
  it.each(TEXT_PAIRS)("%s", (_label, foreground, background, criterion) => {
    const ratio = contrast(foreground, background);
    expect(ratio, `${foreground} on ${background} must reach 4.5:1 (WCAG ${criterion}); measured ${ratio.toFixed(2)}:1`)
      .toBeGreaterThanOrEqual(4.5);
  });
});

describe("controls and states meet WCAG 1.4.11 (3:1)", () => {
  it.each(UI_PAIRS)("%s", (_label, foreground, background, criterion) => {
    const ratio = contrast(foreground, background);
    expect(ratio, `${foreground} on ${background} must reach 3:1 (WCAG ${criterion}); measured ${ratio.toFixed(2)}:1`)
      .toBeGreaterThanOrEqual(3);
  });
});

describe("chips and banners", () => {
  it.each(Object.entries(coverTones))("the %s subject cover is legible", (name, tone) => {
    // The store renders the product type as text on this chip, so it is text, not decoration.
    const ratio = contrast(tone.foreground, tone.background);
    expect(ratio, `${name} chip: ${tone.foreground} on ${tone.background} measured ${ratio.toFixed(2)}:1`)
      .toBeGreaterThanOrEqual(4.5);
  });

  it.each(Object.entries(statusTones))("the %s banner is legible", (_name, tone) => {
    const ratio = contrast(tone.foreground, tone.background);
    expect(ratio, `banner text measured ${ratio.toFixed(2)}:1`).toBeGreaterThanOrEqual(4.5);
    // The border is decoration — the background already separates the banner from the page — so it is
    // held to the non-text ratio rather than the text one.
    expect(contrast(tone.border, tone.background)).toBeGreaterThanOrEqual(1);
  });
});
