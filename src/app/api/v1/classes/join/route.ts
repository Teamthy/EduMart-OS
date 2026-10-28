import { handle, ok, readJson, assertSameOrigin } from "@/lib/server/http";
import { requireUser } from "@/lib/server/auth";
import { joinClassByCode } from "@/lib/server/classes";

export async function POST(req: Request) {
  return handle(req, async () => {
    assertSameOrigin(req);
    const user = await requireUser();
    const body = await readJson<{ code?: string }>(req);
    return ok({ joined: joinClassByCode(user, body.code ?? "") });
  });
}
