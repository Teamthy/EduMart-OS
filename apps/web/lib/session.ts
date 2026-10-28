import { cache } from "react";
import { redirect } from "next/navigation";

import type { User } from "@edumart/shared";

import { ServerApiError, serverFetch } from "./server-api";

/**
 * Current principal, memoised per request so a page and its children share one lookup.
 * Returns null when signed out; callers decide whether that is a redirect or an empty state.
 */
export const getCurrentUser = cache(async (): Promise<User | null> => {
  try {
    const { user } = await serverFetch<{ user: User }>("/v1/me");
    return user;
  } catch (error) {
    if (error instanceof ServerApiError && (error.isUnauthenticated || error.isForbidden)) return null;
    throw error;
  }
});

/** Require a session, redirecting to sign-in with a return path when absent. */
export async function requireUser(returnTo = "/dashboard"): Promise<User> {
  const user = await getCurrentUser();
  if (!user) redirect(`/login?next=${encodeURIComponent(returnTo)}`);
  return user;
}

/** Require one of the roles, otherwise show the permission-denied screen. */
export async function requireRole(roles: string[], returnTo = "/dashboard"): Promise<User> {
  const user = await requireUser(returnTo);
  const allowed = user.roles.some((role) => role === "platform_admin" || roles.includes(role));
  if (!allowed) redirect("/permission-denied");
  return user;
}

export function isStaff(user: User | null): boolean {
  if (!user) return false;
  return user.roles.some((role) =>
    ["teacher", "org_admin", "content_editor", "platform_admin", "support", "finance"].includes(role),
  );
}
