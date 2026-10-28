import { handle, ok, readJson, assertSameOrigin, idempotencyKeyFrom, withIdempotency } from "@/lib/server/http";
import { requireUser } from "@/lib/server/auth";
import { startDefinitionSession, sessionView } from "@/lib/server/assessment";

export async function POST(req: Request, ctx: { params: Promise<{ definitionId: string }> }) {
  return handle(req, async () => {
    assertSameOrigin(req);
    const user = await requireUser();
    const { definitionId } = await ctx.params;
    await readJson<{ client_context?: unknown }>(req).catch(() => ({}));
    const key = idempotencyKeyFrom(req);

    const result = withIdempotency({ key, scope: `assessment.start:${definitionId}`, userId: user.id }, () => {
      const session = startDefinitionSession(user, definitionId, null);
      return { status: 201, body: { session: sessionView(session, { includeAnswers: false }) } };
    });
    return ok(result.body, { status: result.status, headers: result.replayed ? { "idempotent-replay": "true" } : undefined });
  });
}
