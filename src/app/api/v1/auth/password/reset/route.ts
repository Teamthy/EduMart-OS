import { q, nowIso } from "@/lib/server/db";
import { ApiError, handle, ok, readJson, assertSameOrigin } from "@/lib/server/http";
import { sha256, hashPassword } from "@/lib/server/auth";
import { audit } from "@/lib/server/audit";

/** Single-use, expiring reset token; all sessions are revoked after a reset. */
export async function POST(req: Request) {
  return handle(req, async () => {
    assertSameOrigin(req);
    const body = await readJson<{ token?: string; password?: string }>(req);
    if (!body.token) throw new ApiError("VALIDATION_ERROR", "This reset link is incomplete.");
    if ((body.password ?? "").length < 8) throw new ApiError("VALIDATION_ERROR", "Use a password of at least 8 characters.");

    const record = q.get<any>("SELECT * FROM password_reset_tokens WHERE id = ?", sha256(body.token));
    if (!record) throw new ApiError("NOT_FOUND", "This reset link is not valid.");
    if (record.used_at) throw new ApiError("CONFLICT", "This reset link has already been used.");
    if (new Date(record.expires_at).getTime() < Date.now()) throw new ApiError("CONFLICT", "This reset link has expired.");

    q.run("UPDATE users SET password_hash = ?, updated_at = ? WHERE id = ?", hashPassword(body.password!), nowIso(), record.user_id);
    q.run("UPDATE password_reset_tokens SET used_at = ? WHERE id = ?", nowIso(), record.id);
    q.run("UPDATE sessions SET revoked_at = ? WHERE user_id = ? AND revoked_at IS NULL", nowIso(), record.user_id);
    audit({ actorUserId: record.user_id, action: "account.password_reset", resourceType: "user", resourceId: record.user_id });

    return ok({ reset: true });
  });
}
