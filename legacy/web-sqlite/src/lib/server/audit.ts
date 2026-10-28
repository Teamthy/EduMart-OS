import { q, nowIso, id } from "./db";

/** Append-only audit trail for privileged or state-changing operations. */
export function audit(opts: {
  actorUserId?: string | null;
  action: string;
  resourceType: string;
  resourceId?: string;
  reason?: string;
  requestId?: string;
  metadata?: Record<string, unknown>;
}) {
  q.run(
    `INSERT INTO audit_logs (id, actor_user_id, action, resource_type, resource_id, reason, request_id, metadata_json, occurred_at)
     VALUES (?,?,?,?,?,?,?,?,?)`,
    id(),
    opts.actorUserId ?? null,
    opts.action,
    opts.resourceType,
    opts.resourceId ?? "",
    opts.reason ?? "",
    opts.requestId ?? "",
    JSON.stringify(opts.metadata ?? {}),
    nowIso(),
  );
}

export function enqueue(kind: string, payload: Record<string, unknown>, runAfterMs = 0) {
  q.run(
    `INSERT INTO job_queue_items (id, kind, payload_json, status, attempts, last_error, run_after, created_at, updated_at)
     VALUES (?,?,?,'pending',0,'',?,?,?)`,
    id(),
    kind,
    JSON.stringify(payload),
    new Date(Date.now() + runAfterMs).toISOString(),
    nowIso(),
    nowIso(),
  );
}

export function track(event: {
  userId: string;
  eventType: string;
  entityType?: string;
  entityId?: string;
  metadata?: Record<string, unknown>;
}) {
  q.run(
    "INSERT INTO learning_events (id, user_id, event_type, entity_type, entity_id, metadata_json, occurred_at) VALUES (?,?,?,?,?,?,?)",
    id(),
    event.userId,
    event.eventType,
    event.entityType ?? "",
    event.entityId ?? "",
    JSON.stringify(event.metadata ?? {}),
    nowIso(),
  );
}
