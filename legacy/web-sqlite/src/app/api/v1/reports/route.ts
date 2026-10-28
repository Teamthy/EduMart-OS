import { handle, ok, readJson, assertSameOrigin, rateLimit, ApiError } from "@/lib/server/http";
import { requireUser } from "@/lib/server/auth";
import { q, id, nowIso } from "@/lib/server/db";
import { audit } from "@/lib/server/audit";

/** Learner content report → moderation queue. */
export async function POST(req: Request) {
  return handle(req, async () => {
    assertSameOrigin(req);
    const user = await requireUser();
    if (!rateLimit(`report:${user.id}`, 10, 60 * 60_000).allowed) {
      throw new ApiError("RATE_LIMITED", "You have submitted several reports recently. Please try again later.");
    }
    const body = await readJson<{ questionVersionId?: string; reason?: string; details?: string }>(req);
    const version = q.get<any>("SELECT id FROM question_versions WHERE id = ?", body.questionVersionId ?? "");
    if (!version) throw new ApiError("NOT_FOUND", "That question could not be found.");
    const reasons = ["incorrect_answer", "unclear_explanation", "typo", "duplicate", "rights_issue", "other"];
    if (!reasons.includes(body.reason ?? "")) throw new ApiError("VALIDATION_ERROR", "Choose a reason for the report.");

    q.run(
      "INSERT INTO content_reports (id, question_version_id, reported_by, reason, details, status, created_at) VALUES (?,?,?,?,?, 'open', ?)",
      id(),
      version.id,
      user.id,
      body.reason,
      (body.details ?? "").slice(0, 1000),
      nowIso(),
    );
    audit({ actorUserId: user.id, action: "content.reported", resourceType: "question_version", resourceId: version.id, reason: body.reason ?? "" });
    return ok({ reported: true, message: "Thank you — our content team will review this question." }, { status: 201 });
  });
}
