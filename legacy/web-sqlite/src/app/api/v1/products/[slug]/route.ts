import { handle, ok, ApiError } from "@/lib/server/http";
import { productBySlug, entitlementFor } from "@/lib/server/commerce";
import { getCurrentUser } from "@/lib/server/auth";

export async function GET(req: Request, ctx: { params: Promise<{ slug: string }> }) {
  return handle(req, async () => {
    const { slug } = await ctx.params;
    const product = productBySlug(slug);
    if (!product || product.status !== "published") throw new ApiError("NOT_FOUND", "That publication could not be found.");
    const user = await getCurrentUser();
    return ok({ product, owned: user ? Boolean(entitlementFor(user.id, product.id)) : false });
  });
}
