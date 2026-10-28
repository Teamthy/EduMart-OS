import { handle, ok, readJson, assertSameOrigin, clientIp } from "@/lib/server/http";
import { requireUser } from "@/lib/server/auth";
import { listClasses, createClass } from "@/lib/server/classes";
import { rateLimit, ApiError } from "@/lib/server/http";

export async function GET(req: Request) {
  return handle(req, async () => {
    const user = await requireUser();
    return ok({ classes: listClasses(user.id) });
  });
}

export async function POST(req: Request) {
  return handle(req, async () => {
    assertSameOrigin(req);
    const user = await requireUser();
    if (!rateLimit(`class:create:${user.id}:${clientIp(req)}`, 10, 60_000).allowed) {
      throw new ApiError("RATE_LIMITED", "You are creating classes too quickly.");
    }
    const body = await readJson<{ name?: string; organizationId?: string | null; subjectId?: string | null }>(req);
    const created = createClass(user, {
      name: body.name ?? "",
      organizationId: body.organizationId ?? null,
      subjectId: body.subjectId ?? null,
    });
    return ok({ class: created }, { status: 201 });
  });
}
