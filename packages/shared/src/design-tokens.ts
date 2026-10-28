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
  ink3: "#667079",
  ink4: "#8d969e",
  paper: "#f8f7f4",
  surface: "#ffffff",
  line: "#e4e1da",
  lineStrong: "#d3cfc6",
  amber500: "#c98508",
  amber100: "#fdf0d4",
  rose600: "#b0303f",
  rose100: "#fbe4e4",
  indigo600: "#3b4ec4",
  indigo100: "#e5e8fb",
  teal600: "#0f7b81",
  teal100: "#d9f0f1",
  violet600: "#6b3fa0",
  violet100: "#ece3f8",
  emerald600: "#0f7b4f",
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

/** Minimum touch target, per WCAG 2.2 target size (minimum) 24px and platform guidance of 44px. */
export const minTouchTarget = 44;

export const focusRing = {
  web: "outline: 3px solid #1a9a73; outline-offset: 2px;",
  mobile: { borderWidth: 2, borderColor: palette.green500 },
} as const;
