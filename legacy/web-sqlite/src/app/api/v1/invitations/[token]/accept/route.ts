import { handle, ok, assertSameOrigin } from "@/lib/server/http";
import { requireUser } from "@/lib/server/auth";
import { acceptInvitation } from "@/lib/server/classes";

export async function POST(req: Request, ctx: { params: Promise<{ token: string }> }) {
  return handle(req, async () => {
    assertSameOrigin(req);
    const user = await requireUser();
    const { token } = await ctx.params;
    return ok({ accepted: acceptInvitation(user, token) });
  });
}
