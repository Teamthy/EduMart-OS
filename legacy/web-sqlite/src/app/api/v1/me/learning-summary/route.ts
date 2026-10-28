import { handle, ok } from "@/lib/server/http";
import { requireUser } from "@/lib/server/auth";
import { learningSummary } from "@/lib/server/insights";

export async function GET(req: Request) {
  return handle(req, async () => {
    const user = await requireUser();
    return ok({ summary: learningSummary(user.id) });
  });
}
