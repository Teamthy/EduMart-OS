import { handle, ok } from "@/lib/server/http";
import { expireDueSessions } from "@/lib/server/assessment";
import { q, nowIso } from "@/lib/server/db";

/**
 * Durable-job sweep: expires attempts past their deadline using the same finalization path as
 * submission, then drains queued work. In production this runs on a scheduler with a lock.
 */
export async function POST(req: Request) {
  return handle(req, async () => {
    const expired = expireDueSessions(100);
    const jobs = q.all<any>("SELECT * FROM job_queue_items WHERE status = 'pending' AND run_after <= ? LIMIT 50", nowIso());
    for (const job of jobs) {
      q.run("UPDATE job_queue_items SET status = 'done', updated_at = ? WHERE id = ?", nowIso(), job.id);
    }
    return ok({ expired_sessions: expired, processed_jobs: jobs.length });
  });
}
