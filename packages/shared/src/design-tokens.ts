/**
 * Design tokens shared by the web app (Tailwind theme + shadcn CSS variables) and the mobile app
 * (React Native theme object). Values come from the original Edumart design system; they are the
 * single source of truth for colour, radius and spacing in both shells.
 */

export const palette = {
  green900: "#06301f",
  green800: "#0a4630",
  green700: "#0d5c46",
  green600: "#137a5c",
  green500: "#1a9a73",
  green100: "#dff0e7",
  green50: "#eff7f2",
  ink: "#12181d",
  ink2: "#3a444d",
  // ink3 was #667079, which measured 4.39:1 on the muted panel — a near miss that only bites the
  // moment muted text lands on a muted background. Darkened to clear 4.5:1 on every surface we use.
  ink3: "#5b666e",
  ink4: "#8d969e",
  paper: "#f8f7f4",
  surface: "#ffffff",
  line: "#e4e1da",
  // lineStrong is the *only* boundary of a text field, a checkbox and a radio. At #d3cfc6 it measured
  // 1.55:1 against the card: WCAG 1.4.11 wants 3:1 for the visual information that identifies a control.
  lineStrong: "#8f8672",
  // amber500 carries warning icons *and* text on the amber cover chip, so it has to clear 4.5:1 on
  // #fdf0d4 rather than the 3:1 an icon alone would need. #c98508 measured 2.72:1 there.
  amber500: "#936106",
  amber100: "#fdf0d4",
  rose600: "#b0303f",
  rose100: "#fbe4e4",
  indigo600: "#3b4ec4",
  indigo100: "#e5e8fb",
  teal600: "#0e747a", // 4.24:1 on its own chip → 4.66:1
  teal100: "#d9f0f1",
  violet600: "#6b3fa0",
  violet100: "#ece3f8",
  emerald600: "#0e764c", // 4.46:1 on its own chip → 4.77:1
  emerald100: "#dbf1e3",
} as const;

export const radii = { sm: 8, md: 12, lg: 18, xl: 26 } as const;

export const spacing = { xs: 4, sm: 8, md: 12, lg: 16, xl: 24, xxl: 32 } as const;

export const typography = {
  sans: 'ui-sans-serif, -apple-system, "Segoe UI", Roboto, "Helvetica Neue", Arial, sans-serif',
  serif: '"Iowan Old Style", "Palatino Linotype", Palatino, Georgia, "Times New Roman", serif',
  mono: 'ui-monospace, SFMono-Regular, "SF Mono", Menlo, Consolas, monospace',
} as const;

/** Subject cover tones, keyed by the tone name stored on a product. */
export const coverTones: Record<string, { background: string; foreground: string }> = {
  indigo: { background: palette.indigo100, foreground: palette.indigo600 },
  violet: { background: palette.violet100, foreground: palette.violet600 },
  teal: { background: palette.teal100, foreground: palette.teal600 },
  emerald: { background: palette.emerald100, foreground: palette.emerald600 },
  amber: { background: palette.amber100, foreground: palette.amber500 },
  rose: { background: palette.rose100, foreground: palette.rose600 },
};

/**
 * Status banner tones, shared so the same five colours are used by the web alerts and the mobile shell.
 *
 * They live here rather than inline in the component because a colour literal buried in JSX is invisible
 * to the contrast check, and the check is the only thing standing between a tasteful amber and an
 * unreadable one.
 */
export const statusTones = {
  info: { background: "#eef3fb", border: "#c9d6ef", foreground: "#243b63" },
  success: { background: "#eaf7ef", border: "#bfe3cd", foreground: "#0b5837" },
  warning: { background: "#fdf6e6", border: "#f0dcae", foreground: "#7a5200" },
  error: { background: "#fdeeee", border: "#efc4c4", foreground: "#8f2431" },
  offline: { background: "#f4f1ea", border: "#d9d4c9", foreground: "#4c4a44" },
} as const;

/** Minimum touch target, per WCAG 2.2 target size (minimum) 24px and platform guidance of 44px. */
export const minTouchTarget = 44;

export const focusRing = {
  web: "outline: 3px solid #1a9a73; outline-offset: 2px;",
  mobile: { borderWidth: 2, borderColor: palette.green500 },
} as const;
