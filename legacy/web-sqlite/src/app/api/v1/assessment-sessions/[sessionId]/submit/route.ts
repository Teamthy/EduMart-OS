import { handle, ok, assertSameOrigin, idempotencyKeyFrom } from "@/lib/server/http";
import { requireUser } from "@/lib/server/auth";
import { finalizeSession, getSessionRow, sessionView } from "@/lib/server/assessment";
import { ApiError } from "@/lib/server/http";

/**
 * Final submission is a server-side state transition. Duplicate submissions are safe:
 * the second call returns the already-finalized attempt rather than re-scoring it.
 */
export async function POST(req: Request, ctx: { params: Promise<{ sessionId: string }> }) {
  return handle(req, async () => {
    assertSameOrigin(req);
    const user = await requireUser();
    const { sessionId } = await ctx.params;
    idempotencyKeyFrom(req);

    const session = getSessionRow(sessionId);
    if (!session || session.user_id !== user.id) throw new ApiError("NOT_FOUND", "That attempt could not be found.");

    const finalized = finalizeSession(session, "submitted");
    return ok({ session: sessionView(finalized, { includeAnswers: true }), already_finalized: session.status !== "in_progress" });
  });
}
