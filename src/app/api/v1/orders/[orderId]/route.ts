import { handle, ok, ApiError } from "@/lib/server/http";
import { requireUser } from "@/lib/server/auth";
import { getOrder } from "@/lib/server/commerce";

export async function GET(req: Request, ctx: { params: Promise<{ orderId: string }> }) {
  return handle(req, async () => {
    const user = await requireUser();
    const { orderId } = await ctx.params;
    const order = getOrder(user.id, orderId);
    if (!order) throw new ApiError("NOT_FOUND", "That order could not be found.");
    return ok({ order });
  });
}
