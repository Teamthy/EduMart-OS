import { handle, ok, readJson, assertSameOrigin, idempotencyKeyFrom } from "@/lib/server/http";
import { requireUser } from "@/lib/server/auth";
import { initiatePayment, getOrder } from "@/lib/server/commerce";

export async function POST(req: Request, ctx: { params: Promise<{ orderId: string }> }) {
  return handle(req, async () => {
    assertSameOrigin(req);
    const user = await requireUser();
    const { orderId } = await ctx.params;
    await readJson<{ provider?: string }>(req).catch(() => ({}));
    idempotencyKeyFrom(req);
    const attempt = initiatePayment(user.id, orderId);
    return ok({ attempt, order: getOrder(user.id, orderId) }, { status: 201 });
  });
}
