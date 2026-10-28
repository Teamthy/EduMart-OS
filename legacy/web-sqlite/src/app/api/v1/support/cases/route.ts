import { handle, ok, readJson, assertSameOrigin } from "@/lib/server/http";
import { requireUser } from "@/lib/server/auth";
import { q, id, nowIso } from "@/lib/server/db";
import { audit } from "@/lib/server/audit";

export async function GET(req: Request) {
  return handle(req, async () => {
    const user = await requireUser();
    const cases = q.all<any>(
      "SELECT id, category, priority, status, subject, created_at FROM support_cases WHERE opened_by = ? ORDER BY created_at DESC",
      user.id,
    );
    return ok({ cases });
  });
}

export async function POST(req: Request) {
  return handle(req, async () => {
    assertSameOrigin(req);
    const user = await requireUser();
    const body = await readJson<{ category?: string; subject?: string; body?: string; priority?: string }>(req);
    const categories = ["access", "payment", "content", "technical", "exam_dispute", "privacy"];
    if (!categories.includes(body.category ?? "")) throw new Error("invalid");
    const caseId = id();
    q.run(
      `INSERT INTO support_cases (id, opened_by, category, priority, status, subject, body, assigned_to, created_at, updated_at)
       VALUES (?,?,?,?, 'open', ?,?, NULL, ?, ?)`,
      caseId,
      user.id,
      body.category,
      ["low", "normal", "high", "urgent"].includes(body.priority ?? "") ? body.priority : "normal",
      (body.subject ?? "").slice(0, 140),
      (body.body ?? "").slice(0, 2000),
      nowIso(),
      nowIso(),
    );
    audit({ actorUserId: user.id, action: "support.case_opened", resourceType: "support_case", resourceId: caseId });
    return ok({ case_id: caseId, message: "Your case is with our support team." }, { status: 201 });
  });
}
