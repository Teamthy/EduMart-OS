import { handle, ok, readJson, assertSameOrigin, idempotencyKeyFrom, withIdempotency } from "@/lib/server/http";
import { requireUser } from "@/lib/server/auth";
import { createOrder, listOrders } from "@/lib/server/commerce";

export async function GET(req: Request) {
  return handle(req, async () => {
    const user = await requireUser();
    return ok({ orders: listOrders(user.id) });
  });
}

export async function POST(req: Request) {
  return handle(req, async () => {
    assertSameOrigin(req);
    const user = await requireUser();
    const body = await readJson<{ items?: string[] }>(req);
    const key = idempotencyKeyFrom(req);
    const result = withIdempotency({ key, scope: "order.create", userId: user.id }, () => ({
      status: 201,
      body: { order: createOrder(user.id, body.items ?? []) },
    }));
    return ok(result.body, { status: result.status });
  });
}
