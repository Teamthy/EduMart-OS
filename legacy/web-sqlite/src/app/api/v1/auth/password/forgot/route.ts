import { randomBytes } from "node:crypto";
import { q, nowIso, id } from "@/lib/server/db";
import { handle, ok, readJson, rateLimit, clientIp, ApiError, assertSameOrigin } from "@/lib/server/http";
import { normalizeEmail, sha256 } from "@/lib/server/auth";
import { enqueue } from "@/lib/server/audit";

/**
 * Always returns 200 so the endpoint cannot be used to discover registered emails.
 * In production the token is delivered by the notification worker; it is never returned to the client.
 */
export async function POST(req: Request) {
  return handle(req, async () => {
    assertSameOrigin(req);
    if (!rateLimit(`forgot:${clientIp(req)}`, 5, 60_000).allowed) {
      throw new ApiError("RATE_LIMITED", "Too many reset requests. Please wait a minute.");
    }
    const body = await readJson<{ email?: string }>(req);
    const email = normalizeEmail(body.email ?? "");
    const user = q.get<any>("SELECT id FROM users WHERE email_normalized = ?", email);
    if (user) {
      const token = randomBytes(32).toString("base64url");
      q.run(
        "INSERT INTO password_reset_tokens (id, user_id, created_at, expires_at, used_at) VALUES (?,?,?,?,NULL)",
        sha256(token),
        user.id,
        nowIso(),
        new Date(Date.now() + 30 * 60_000).toISOString(),
      );
      enqueue("email.password_reset", { userId: user.id, token }, 0);
    }
    return ok({ sent: true, message: "If that email is registered, a reset link is on its way." });
  });
}
