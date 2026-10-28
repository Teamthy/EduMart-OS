import { palette, radii, spacing } from "@edumart/shared";

/** React Native theme derived from the same tokens the web app uses. */
export const theme = {
  colors: {
    background: palette.paper,
    surface: palette.surface,
    ink: palette.ink,
    inkMuted: palette.ink3,
    primary: palette.green700,
    primaryDark: palette.green800,
    primarySoft: palette.green100,
    border: palette.line,
    danger: palette.rose600,
    dangerSoft: palette.rose100,
    success: palette.emerald600,
    successSoft: palette.emerald100,
    warning: palette.amber500,
    warningSoft: palette.amber100,
  },
  radii,
  spacing,
  /** 44pt minimum touch target, per WCAG 2.2 target size guidance. */
  touch: 44,
} as const;
