import { handle, ok } from "@/lib/server/http";
import { requireUser } from "@/lib/server/auth";
import { getSessionRow, sessionView } from "@/lib/server/assessment";
import { ApiError } from "@/lib/server/http";

export async function GET(req: Request, ctx: { params: Promise<{ sessionId: string }> }) {
  return handle(req, async () => {
    const user = await requireUser();
    const { sessionId } = await ctx.params;
    const session = getSessionRow(sessionId);
    if (!session || session.user_id !== user.id) throw new ApiError("NOT_FOUND", "That attempt could not be found.");
    return ok({ session: sessionView(session, { includeAnswers: true }) });
  });
}
