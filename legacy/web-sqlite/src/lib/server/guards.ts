import { redirect } from "next/navigation";
import { getCurrentUser, type AuthUser } from "./auth";

/** Page-level guard. Redirects rather than rendering a permission-denied page for anonymous users. */
export async function requirePageUser(next?: string): Promise<AuthUser> {
  const user = await getCurrentUser();
  if (!user) redirect(`/login${next ? `?next=${encodeURIComponent(next)}` : ""}`);
  return user;
}

export async function requirePageRole(roles: string[], next?: string): Promise<AuthUser> {
  const user = await requirePageUser(next);
  if (user.roles.includes("platform_admin")) return user;
  if (!roles.some((role) => user.roles.includes(role))) redirect("/dashboard?denied=1");
  return user;
}

export function homeFor(user: AuthUser): string {
  if (user.roles.includes("platform_admin") || user.roles.includes("content_editor")) return "/admin";
  if (user.roles.includes("teacher") || user.roles.includes("org_admin")) return "/teacher";
  return "/dashboard";
}

export function naira(minor: number): string {
  return `₦${(minor / 100).toLocaleString("en-NG")}`;
}

export function relativeTime(iso: string | null | undefined): string {
  if (!iso) return "—";
  const diff = Date.now() - new Date(iso).getTime();
  const mins = Math.round(diff / 60000);
  if (Math.abs(mins) < 1) return "just now";
  if (mins < 60) return `${mins} min ago`;
  const hours = Math.round(mins / 60);
  if (hours < 24) return `${hours}h ago`;
  const days = Math.round(hours / 24);
  if (days < 7) return `${days}d ago`;
  return new Date(iso).toLocaleDateString("en-NG", { day: "numeric", month: "short", year: "numeric" });
}

export function daysUntil(iso: string | null | undefined): number | null {
  if (!iso) return null;
  return Math.ceil((new Date(iso).getTime() - Date.now()) / 86400000);
}

export function scoreTone(percent: number | null | undefined): string {
  if (percent === null || percent === undefined) return "neutral";
  if (percent >= 70) return "";
  if (percent >= 45) return "amber";
  return "rose";
}
