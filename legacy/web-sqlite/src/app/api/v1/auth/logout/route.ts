import { cookies } from "next/headers";
import { handle, ok, assertSameOrigin } from "@/lib/server/http";
import { SESSION_COOKIE, revokeSessionByToken } from "@/lib/server/auth";

export async function POST(req: Request) {
  return handle(req, async () => {
    assertSameOrigin(req);
    const store = await cookies();
    const token = store.get(SESSION_COOKIE)?.value;
    if (token) revokeSessionByToken(token);
    store.delete(SESSION_COOKIE);
    return ok({ signedOut: true });
  });
}
