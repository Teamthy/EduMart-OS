import { handle, ok } from "@/lib/server/http";
import { requireUser, requireRole } from "@/lib/server/auth";
import { q } from "@/lib/server/db";

/** Operations console data. Restricted to content editors, finance and platform admins. */
export async function GET(req: Request) {
  return handle(req, async () => {
    const user = await requireUser();
    requireRole(user, "content_editor", "platform_admin", "finance", "support");

    const reviewQueue = q.all<any>(
      `SELECT qv.id, qv.stem, qv.review_state, qv.created_at, qv.source_name, qv.license_basis,
              s.name AS subject_name, t.name AS topic_name, u.display_name AS reviewer
         FROM question_versions qv
         JOIN questions q ON q.id = qv.question_id
         JOIN subjects s ON s.id = q.subject_id
         LEFT JOIN topics t ON t.id = q.topic_id
         LEFT JOIN users u ON u.id = qv.reviewer_id
        WHERE qv.review_state IN ('in_review','draft')
        ORDER BY qv.created_at ASC LIMIT 25`,
    );
    const reports = q.all<any>(
      `SELECT cr.id, cr.reason, cr.details, cr.status, cr.created_at, qv.stem, u.display_name AS reporter
         FROM content_reports cr JOIN question_versions qv ON qv.id = cr.question_version_id
         JOIN users u ON u.id = cr.reported_by
        WHERE cr.status = 'open' ORDER BY cr.created_at DESC`,
    );
    const orders = q.all<any>(
      `SELECT o.id, o.reference, o.status, o.total_minor, o.currency, o.created_at, u.display_name, u.email_normalized
         FROM orders o JOIN users u ON u.id = o.user_id ORDER BY o.created_at DESC LIMIT 20`,
    );
    const reconciliation = q.get<any>(
      `SELECT COUNT(*) AS mismatches FROM payment_attempts pa JOIN orders o ON o.id = pa.order_id
        WHERE pa.status = 'succeeded' AND o.status NOT IN ('paid','fulfilled','refunded')`,
    );
    const support = q.all<any>("SELECT * FROM support_cases ORDER BY created_at DESC LIMIT 10");
    const auditTrail = q.all<any>(
      `SELECT a.id, a.action, a.resource_type, a.resource_id, a.reason, a.occurred_at, u.display_name AS actor
         FROM audit_logs a LEFT JOIN users u ON u.id = a.actor_user_id ORDER BY a.occurred_at DESC LIMIT 25`,
    );
    const counts = q.get<any>(
      `SELECT (SELECT COUNT(*) FROM users) AS users,
              (SELECT COUNT(*) FROM questions WHERE status = 'published') AS questions,
              (SELECT COUNT(*) FROM assessment_sessions) AS attempts,
              (SELECT COUNT(*) FROM job_queue_items WHERE status = 'pending') AS queued_jobs`,
    );

    return ok({ reviewQueue, reports, orders, reconciliation, support, auditTrail, counts });
  });
}
