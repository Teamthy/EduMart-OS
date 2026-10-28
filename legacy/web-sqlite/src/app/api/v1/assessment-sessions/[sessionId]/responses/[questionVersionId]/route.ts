import { handle, ok, readJson, assertSameOrigin, ApiError } from "@/lib/server/http";
import { requireUser } from "@/lib/server/auth";
import { saveResponse } from "@/lib/server/assessment";

type Body = { answer?: { selected_option_id?: string | null }; selectedOptionId?: string | null; expected_version?: number; expectedVersion?: number; client_sequence?: number; clientSequence?: number };

/**
 * PUT /v1/assessment-sessions/{id}/responses/{questionVersionId}
 * Idempotent upsert with optimistic versioning. Returns the canonical stored version so the UI
 * can display "Saved on server" only after acknowledgement.
 */
export async function PUT(req: Request, ctx: { params: Promise<{ sessionId: string; questionVersionId: string }> }) {
  return handle(req, async () => {
    assertSameOrigin(req);
    const user = await requireUser();
    const { sessionId, questionVersionId } = await ctx.params;
    const body = await readJson<Body>(req);

    const selected = body.answer?.selected_option_id !== undefined
      ? body.answer.selected_option_id
      : body.selectedOptionId !== undefined
        ? body.selectedOptionId
        : null;

    if (selected !== null && typeof selected !== "string") {
      throw new ApiError("VALIDATION_ERROR", "selected_option_id must be a string or null.");
    }

    return ok(
      saveResponse(user.id, sessionId, questionVersionId, {
        selectedOptionId: selected,
        expectedVersion: body.expected_version ?? body.expectedVersion,
        clientSequence: body.client_sequence ?? body.clientSequence,
      }),
    );
  });
}
