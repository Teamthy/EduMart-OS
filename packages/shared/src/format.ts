/** Formatting helpers shared by web and mobile so both shells print identical values. */

/**
 * Money is always integer minor units (kobo for NGN) and is never a float from the server.
 * Currencies without a minor unit are listed explicitly; everything else divides by 100.
 */
const ZERO_DECIMAL_CURRENCIES = ["JPY", "KRW", "VND", "CLP"];

export function formatMoney(minor: number, currency = "NGN"): string {
  const zeroDecimal = ZERO_DECIMAL_CURRENCIES.includes(currency);
  const value = zeroDecimal ? minor : minor / 100;
  const formatted = new Intl.NumberFormat("en-NG", {
    style: "currency",
    currency,
    minimumFractionDigits: 0,
    maximumFractionDigits: zeroDecimal ? 0 : 2,
  }).format(value);
  // en-NG renders NGN as "NGN"; the product shows ₦ for readability.
  return currency === "NGN" ? formatted.replace("NGN", "₦").replace(/\s+/g, "") : formatted;
}

export function formatPercent(value: number | null | undefined): string {
  if (value === null || value === undefined) return "—";
  return `${Math.round(value)}%`;
}

/** Countdown from whole seconds; display-only, the server owns the real deadline. */
export function formatCountdown(totalSeconds: number): string {
  const safe = Math.max(0, Math.floor(totalSeconds));
  const hours = Math.floor(safe / 3600);
  const minutes = Math.floor((safe % 3600) / 60);
  const seconds = safe % 60;
  const pad = (n: number) => String(n).padStart(2, "0");
  return hours > 0 ? `${hours}:${pad(minutes)}:${pad(seconds)}` : `${pad(minutes)}:${pad(seconds)}`;
}

export function formatRelativeDate(iso: string, now = new Date()): string {
  const then = new Date(iso);
  const diffMs = now.getTime() - then.getTime();
  const minutes = Math.round(diffMs / 60000);
  if (minutes < 1) return "just now";
  if (minutes < 60) return `${minutes}m ago`;
  const hours = Math.round(minutes / 60);
  if (hours < 24) return `${hours}h ago`;
  const days = Math.round(hours / 24);
  if (days < 7) return `${days}d ago`;
  return then.toLocaleDateString("en-NG", { day: "numeric", month: "short", year: "numeric" });
}

/** Date + time, used where the exact moment matters (submission time). */
export function formatDateTimeSafe(iso: string | null): string {
  if (!iso) return "—";
  return new Date(iso).toLocaleString("en-NG", {
    day: "numeric",
    month: "short",
    year: "numeric",
    hour: "2-digit",
    minute: "2-digit",
  });
}

export function formatDate(iso: string | null): string {
  if (!iso) return "—";
  return new Date(iso).toLocaleDateString("en-NG", { day: "numeric", month: "long", year: "numeric" });
}

/** Days between today and a target date, clamped at zero. */
export function daysUntil(isoDate: string | null): number | null {
  if (!isoDate) return null;
  const target = new Date(`${isoDate}T00:00:00Z`).getTime();
  const today = new Date();
  const start = Date.UTC(today.getUTCFullYear(), today.getUTCMonth(), today.getUTCDate());
  return Math.max(0, Math.round((target - start) / 86_400_000));
}

/** Initials for avatars, tolerant of single names. */
export function initials(name: string): string {
  const parts = name.trim().split(/\s+/).filter(Boolean);
  if (parts.length === 0) return "?";
  if (parts.length === 1) return parts[0]!.slice(0, 2).toUpperCase();
  return `${parts[0]![0]}${parts[parts.length - 1]![0]}`.toUpperCase();
}
