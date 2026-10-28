import { handle, ok, readJson, assertSameOrigin, ApiError } from "@/lib/server/http";
import { requireUser, requireRole } from "@/lib/server/auth";
import { refundOrder } from "@/lib/server/commerce";
import { reverseCommission } from "@/lib/server/rewards";
import { q } from "@/lib/server/db";

export async function POST(req: Request, ctx: { params: Promise<{ orderId: string }> }) {
  return handle(req, async () => {
    assertSameOrigin(req);
    const user = await requireUser();
    requireRole(user, "platform_admin", "finance");
    const { orderId } = await ctx.params;
    const body = await readJson<{ reason?: string }>(req);
    if ((body.reason ?? "").trim().length < 5) throw new ApiError("VALIDATION_ERROR", "A refund reason is required.");

    const order = q.get<any>("SELECT user_id FROM orders WHERE id = ?", orderId);
    if (!order) throw new ApiError("NOT_FOUND", "Order not found.");
    const refunded = refundOrder(orderId, user.id, body.reason!);
    // A refund reverses any commission accrued on that purchase via a compensating entry.
    reverseCommission(orderId, body.reason!, user.id);
    return ok({ order: refunded });
  });
}
