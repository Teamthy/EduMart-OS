import { handle, ok } from "@/lib/server/http";
import { listPathways, listSubjects } from "@/lib/server/content";

export async function GET(req: Request) {
  return handle(req, async () => {
    const pathways = listPathways().map((p) => ({
      ...p,
      subjects: listSubjects(p.id).length,
    }));
    return ok({ pathways });
  });
}
