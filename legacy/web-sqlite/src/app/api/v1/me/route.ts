import { q, nowIso } from "@/lib/server/db";
import { ApiError, handle, ok, readJson, assertSameOrigin } from "@/lib/server/http";
import { requireUser } from "@/lib/server/auth";
import { ensureReferralCode } from "@/lib/server/rewards";

export async function GET(req: Request) {
  return handle(req, async () => {
    const user = await requireUser();
    const referral = ensureReferralCode(user.id, user.displayName);
    return ok({ user, referral_code: referral.code });
  });
}

export async function PATCH(req: Request) {
  return handle(req, async () => {
    assertSameOrigin(req);
    const user = await requireUser();
    const body = await readJson<{ displayName?: string; examPathwayId?: string | null; targetDate?: string | null; timezone?: string }>(req);

    const updates: string[] = [];
    const params: unknown[] = [];
    if (body.displayName !== undefined) {
      if (body.displayName.trim().length < 2) throw new ApiError("VALIDATION_ERROR", "Enter your name.");
      updates.push("display_name = ?");
      params.push(body.displayName.trim());
    }
    if (body.examPathwayId !== undefined) {
      updates.push("exam_pathway_id = ?");
      params.push(body.examPathwayId);
    }
    if (body.targetDate !== undefined) {
      updates.push("target_date = ?");
      params.push(body.targetDate);
    }
    if (body.timezone !== undefined) {
      updates.push("timezone = ?");
      params.push(body.timezone);
    }
    if (updates.length === 0) return ok({ user });
    params.push(nowIso(), user.id);
    q.run(`UPDATE users SET ${updates.join(", ")}, updated_at = ? WHERE id = ?`, ...params);
    const updated = await requireUser();
    return ok({ user: updated });
  });
}
