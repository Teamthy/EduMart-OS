import { handle, ok } from "@/lib/server/http";
import { requireUser } from "@/lib/server/auth";
import { listEntitlements } from "@/lib/server/commerce";

export async function GET(req: Request) {
  return handle(req, async () => {
    const user = await requireUser();
    return ok({ entitlements: listEntitlements(user.id) });
  });
}
