import { handle, ok, readJson, assertSameOrigin, ApiError } from "@/lib/server/http";
import { requireUser } from "@/lib/server/auth";
import { saveResponse } from "@/lib/server/assessment";

type Body = { questionVersionId?: string; selectedOptionId?: string | null; expectedVersion?: number; clientSequence?: number };

export async function POST(req: Request, ctx: { params: Promise<{ sessionId: string }> }) {
  return handle(req, async () => {
    assertSameOrigin(req);
    const user = await requireUser();
    const { sessionId } = await ctx.params;
    const body = await readJson<Body>(req);
    if (!body.questionVersionId) throw new ApiError("VALIDATION_ERROR", "A question id is required.");

    const result = saveResponse(user.id, sessionId, body.questionVersionId, {
      selectedOptionId: body.selectedOptionId ?? null,
      expectedVersion: body.expectedVersion,
      clientSequence: body.clientSequence,
    });
    return ok(result);
  });
}
