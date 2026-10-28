import { handle, ok, readJson, assertSameOrigin, ApiError } from "@/lib/server/http";
import { requireUser, requireRole } from "@/lib/server/auth";
import { q, nowIso } from "@/lib/server/db";
import { audit } from "@/lib/server/audit";

/**
 * Moderation decision. Publishing requires provenance metadata; high-risk actions require a reason.
 * Every decision is audited with actor, time and reason.
 */
export async function POST(req: Request, ctx: { params: Promise<{ questionVersionId: string }> }) {
  return handle(req, async () => {
    assertSameOrigin(req);
    const user = await requireUser();
    requireRole(user, "content_editor", "platform_admin");
    const { questionVersionId } = await ctx.params;
    const body = await readJson<{ decision?: string; reason?: string }>(req);
    const decision = body.decision ?? "";
    if (!["approve", "reject", "withdraw"].includes(decision)) {
      throw new ApiError("VALIDATION_ERROR", "Decision must be approve, reject or withdraw.");
    }
    if (decision !== "approve" && (body.reason ?? "").trim().length < 5) {
      throw new ApiError("VALIDATION_ERROR", "Give a short reason (at least 5 characters) for this decision.");
    }

    const version = q.get<any>(
      `SELECT qv.*, q.status AS question_status FROM question_versions qv JOIN questions q ON q.id = qv.question_id WHERE qv.id = ?`,
      questionVersionId,
    );
    if (!version) throw new ApiError("NOT_FOUND", "That question version could not be found.");

    if (decision === "approve") {
      const missing: string[] = [];
      if (!version.source_name) missing.push("source");
      if (!version.license_basis) missing.push("licence basis");
      if (!version.explanation) missing.push("explanation");
      if (missing.length) {
        throw new ApiError("CONFLICT", `Cannot publish without ${missing.join(", ")}.`, missing);
      }
      q.run(
        "UPDATE question_versions SET review_state = 'approved', reviewer_id = ?, published_at = ?, created_at = created_at WHERE id = ?",
        user.id,
        nowIso(),
        version.id,
      );
      q.run("UPDATE questions SET status = 'published' WHERE id = ?", version.question_id);
    } else if (decision === "reject") {
      q.run("UPDATE question_versions SET review_state = 'rejected', reviewer_id = ? WHERE id = ?", user.id, version.id);
      q.run("UPDATE questions SET status = 'draft' WHERE id = ?", version.question_id);
    } else {
      q.run("UPDATE question_versions SET review_state = 'withdrawn', reviewer_id = ? WHERE id = ?", user.id, version.id);
      q.run("UPDATE questions SET status = 'withdrawn' WHERE id = ?", version.question_id);
    }

    audit({
      actorUserId: user.id,
      action: `question.${decision}`,
      resourceType: "question_version",
      resourceId: version.id,
      reason: body.reason ?? "Editorial review",
    });
    return ok({ decided: decision });
  });
}
