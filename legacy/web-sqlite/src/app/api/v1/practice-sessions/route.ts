import { handle, ok, readJson, assertSameOrigin, idempotencyKeyFrom, withIdempotency } from "@/lib/server/http";
import { requireUser } from "@/lib/server/auth";
import { startPracticeSession, sessionView } from "@/lib/server/assessment";

type Body = { subjectId?: string; topicIds?: string[]; count?: number; difficulty?: string };

export async function POST(req: Request) {
  return handle(req, async () => {
    assertSameOrigin(req);
    const user = await requireUser();
    const body = await readJson<Body>(req);
    const key = idempotencyKeyFrom(req);

    const result = withIdempotency({ key, scope: "practice.start", userId: user.id }, () => {
      const session = startPracticeSession(user.id, {
        subjectId: body.subjectId!,
        topicIds: body.topicIds,
        count: body.count,
        difficulty: body.difficulty,
        idempotencyKey: null,
      });
      return { status: 201, body: { session: sessionView(session, { includeAnswers: false }) } };
    });

    return ok(result.body, { status: result.status, headers: result.replayed ? { "idempotent-replay": "true" } : undefined });
  });
}
