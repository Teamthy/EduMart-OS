import { handle, ok } from "@/lib/server/http";
import { listTopics, subjectById } from "@/lib/server/content";

export async function GET(req: Request, ctx: { params: Promise<{ subjectId: string }> }) {
  return handle(req, async () => {
    const { subjectId } = await ctx.params;
    return ok({ subject: subjectById(subjectId), topics: listTopics(subjectId) });
  });
}
