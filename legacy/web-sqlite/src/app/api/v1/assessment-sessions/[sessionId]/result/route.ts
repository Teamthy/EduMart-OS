import { handle, ok, ApiError } from "@/lib/server/http";
import { requireUser } from "@/lib/server/auth";
import { getSessionRow, readSnapshot, sessionView } from "@/lib/server/assessment";
import { isResultReleased } from "@/lib/domain/exam";

export async function GET(req: Request, ctx: { params: Promise<{ sessionId: string }> }) {
  return handle(req, async () => {
    const user = await requireUser();
    const { sessionId } = await ctx.params;
    const session = getSessionRow(sessionId);
    if (!session || session.user_id !== user.id) throw new ApiError("NOT_FOUND", "That result could not be found.");
    if (session.status === "in_progress") throw new ApiError("CONFLICT", "Submit the attempt to see your result.");

    const definition = session.definition_id
      ? (await import("@/lib/server/content")).definitionById(session.definition_id)
      : null;
    const released = isResultReleased(
      { status: session.status, releaseAt: session.release_at },
      (definition?.result_release as "immediate" | "on_close") ?? "immediate",
      definition?.availability_end ?? null,
    );
    if (!released) {
      return ok({ result: { available: false, reason: "Results are released when the assessment window closes." } });
    }
    const snapshot = readSnapshot(session);
    const responses = sessionView(session, { includeAnswers: true });
    return ok({
      result: {
        available: true,
        policy_version: snapshot.policy.version,
        ...sessionView(session, { includeAnswers: true }),
        responses,
        topics: snapshot.questions.map((q) => ({ topicName: q.topicName, topicId: q.topicId })),
      },
    });
  });
}
