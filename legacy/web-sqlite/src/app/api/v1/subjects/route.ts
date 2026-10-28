import { handle, ok } from "@/lib/server/http";
import { listSubjects } from "@/lib/server/content";

export async function GET(req: Request) {
  return handle(req, async () => {
    const url = new URL(req.url);
    return ok({ subjects: listSubjects(url.searchParams.get("pathwayId") ?? undefined) });
  });
}
