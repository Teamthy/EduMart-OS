import { cookies } from "next/headers";
import { q } from "@/lib/server/db";
import { ApiError, handle, ok, readJson, rateLimit, clientIp, assertSameOrigin } from "@/lib/server/http";
import { verifyPassword, normalizeEmail, createSession, sessionCookie, rolesFor } from "@/lib/server/auth";
import { track } from "@/lib/server/audit";

export async function POST(req: Request) {
  return handle(req, async () => {
    assertSameOrigin(req);
    const body = await readJson<{ email?: string; password?: string }>(req);
    const email = normalizeEmail(body.email ?? "");
    const password = body.password ?? "";

    // Rate limit by IP and by account so a stolen email list cannot be sprayed.
    const ipLimit = rateLimit(`login:ip:${clientIp(req)}`, 20, 60_000);
    const acctLimit = rateLimit(`login:acct:${email}`, 8, 60_000);
    if (!ipLimit.allowed || !acctLimit.allowed) {
      throw new ApiError("RATE_LIMITED", `Too many attempts. Try again in ${Math.max(ipLimit.retryAfter, acctLimit.retryAfter)} seconds.`);
    }

    const user = q.get<any>("SELECT * FROM users WHERE email_normalized = ?", email);
    const generic = new ApiError("UNAUTHENTICATED", "Email or password is incorrect.");
    if (!user) throw generic;
    if (!verifyPassword(password, user.password_hash)) throw generic;
    if (user.status !== "active") throw new ApiError("FORBIDDEN", "This account is not active. Contact support.");

    const { token, expiresAt } = createSession(user.id, req.headers.get("user-agent"));
    (await cookies()).set(sessionCookie(token, Math.floor((new Date(expiresAt).getTime() - Date.now()) / 1000)));

    const roles = rolesFor(user.id);
    track({ userId: user.id, eventType: "session_started", entityType: "user", entityId: user.id });

    return ok({
      user: { id: user.id, email: user.email_normalized, displayName: user.display_name, roles },
      next: roles.includes("platform_admin") || roles.includes("content_editor")
        ? "/admin"
        : roles.includes("teacher") || roles.includes("org_admin")
          ? "/teacher"
          : user.exam_pathway_id
            ? "/dashboard"
            : "/onboarding",
    });
  });
}
