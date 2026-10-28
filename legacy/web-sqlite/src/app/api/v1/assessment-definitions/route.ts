import { handle, ok, readJson, assertSameOrigin } from "@/lib/server/http";
import { requireUser } from "@/lib/server/auth";
import { createAssessmentDefinition } from "@/lib/server/assessment";
import { listMockDefinitions } from "@/lib/server/content";

export async function GET(req: Request) {
  return handle(req, async () => ok({ assessments: listMockDefinitions() }));
}

/** Teacher/admin authoring: builds a published assessment from approved questions. */
export async function POST(req: Request) {
  return handle(req, async () => {
    assertSameOrigin(req);
    const user = await requireUser();
    const body = await readJson<any>(req);
    const created = createAssessmentDefinition(user, {
      title: body.title,
      classId: body.classId ?? null,
      subjectId: body.subjectId,
      topicIds: body.topicIds,
      questionCount: body.questionCount,
      durationSeconds: body.durationSeconds,
      attemptLimit: body.attemptLimit,
      availabilityStart: body.availabilityStart ?? null,
      availabilityEnd: body.availabilityEnd ?? null,
      resultRelease: body.resultRelease,
    });
    return ok({ assessment: created }, { status: 201 });
  });
}
