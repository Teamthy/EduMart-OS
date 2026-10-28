import { handle, ok, readJson, assertSameOrigin } from "@/lib/server/http";
import { requireUser } from "@/lib/server/auth";
import { createInvitation } from "@/lib/server/classes";

export async function POST(req: Request, ctx: { params: Promise<{ classId: string }> }) {
  return handle(req, async () => {
    assertSameOrigin(req);
    const user = await requireUser();
    const { classId } = await ctx.params;
    const body = await readJson<{ email?: string; role?: "student" | "teacher"; maxUses?: number }>(req);
    const invitation = createInvitation(user, { classId, email: body.email, role: body.role, maxUses: body.maxUses });
    return ok({ invitation }, { status: 201 });
  });
}
