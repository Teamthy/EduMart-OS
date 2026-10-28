import { handle, ok, ApiError } from "@/lib/server/http";
import { requireUser } from "@/lib/server/auth";
import { q } from "@/lib/server/db";
import { signPayload } from "@/lib/server/commerce";
import { audit } from "@/lib/server/audit";

/**
 * Issues a short-lived, signed access token for a private object. The file key is never
 * exposed as a public URL and the entitlement is re-checked on every request.
 */
export async function GET(req: Request, ctx: { params: Promise<{ entitlementId: string }> }) {
  return handle(req, async () => {
    const user = await requireUser();
    const { entitlementId } = await ctx.params;
    const row = q.get<any>(
      `SELECT e.*, p.title, p.file_key, p.pages FROM entitlements e JOIN products p ON p.id = e.product_id WHERE e.id = ?`,
      entitlementId,
    );
    if (!row || row.user_id !== user.id) throw new ApiError("NOT_FOUND", "That publication could not be found.");
    if (row.status !== "active") throw new ApiError("FORBIDDEN", "Your access to this publication is no longer active.");
    if (row.expires_at && new Date(row.expires_at).getTime() < Date.now()) {
      throw new ApiError("FORBIDDEN", "Your access to this publication has expired.");
    }

    const expiresAt = new Date(Date.now() + 5 * 60_000).toISOString();
    const token = signPayload(`${row.file_key}:${expiresAt}:${user.id}`);
    audit({
      actorUserId: user.id,
      action: "content.access_granted",
      resourceType: "entitlement",
      resourceId: row.id,
      metadata: { product: row.title },
    });

    return ok({
      title: row.title,
      pages: row.pages,
      signed_url: `/api/v1/entitlements/${row.id}/content/file?token=${encodeURIComponent(token)}&expires=${encodeURIComponent(expiresAt)}`,
      expires_at: expiresAt,
    });
  });
}
