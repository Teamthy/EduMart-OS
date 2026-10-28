import Link from "next/link";
import { Shell } from "@/components/Shell";
import { requirePageRole, naira, relativeTime } from "@/lib/server/guards";
import { q } from "@/lib/server/db";
import { ReviewActions } from "./ReviewActions";
import { RefundAction } from "./RefundAction";

export const dynamic = "force-dynamic";

export default async function AdminPage({ searchParams }: { searchParams: Promise<{ tab?: string }> }) {
  const user = await requirePageRole(["platform_admin", "content_editor", "finance", "support"], "/admin");
  const { tab = "content" } = await searchParams;

  const counts = q.get<any>(
    `SELECT (SELECT COUNT(*) FROM users) AS users,
            (SELECT COUNT(*) FROM questions WHERE status = 'published') AS questions,
            (SELECT COUNT(*) FROM question_versions WHERE review_state = 'in_review') AS in_review,
            (SELECT COUNT(*) FROM assessment_sessions) AS attempts,
            (SELECT COUNT(*) FROM orders WHERE status IN ('paid','fulfilled')) AS paid_orders,
            (SELECT COUNT(*) FROM content_reports WHERE status = 'open') AS open_reports,
            (SELECT COUNT(*) FROM support_cases WHERE status = 'open') AS open_cases,
            (SELECT COUNT(*) FROM job_queue_items WHERE status = 'pending') AS queued_jobs`,
  );

  const reviewQueue = q.all<any>(
    `SELECT qv.id, qv.stem, qv.review_state, qv.created_at, qv.source_name, qv.license_basis,
            s.name AS subject_name, t.name AS topic_name, u.display_name AS author
       FROM question_versions qv
       JOIN questions q ON q.id = qv.question_id
       JOIN subjects s ON s.id = q.subject_id
       LEFT JOIN topics t ON t.id = q.topic_id
       LEFT JOIN users u ON u.id = q.created_by
      WHERE qv.review_state IN ('in_review','draft')
      ORDER BY qv.created_at ASC LIMIT 20`,
  );
  const reports = q.all<any>(
    `SELECT cr.id, cr.reason, cr.details, cr.status, cr.created_at, qv.stem, u.display_name AS reporter
       FROM content_reports cr
       JOIN question_versions qv ON qv.id = cr.question_version_id
       JOIN users u ON u.id = cr.reported_by
      WHERE cr.status = 'open' ORDER BY cr.created_at DESC`,
  );
  const orders = q.all<any>(
    `SELECT o.id, o.reference, o.status, o.total_minor, o.currency, o.created_at, u.display_name, u.email_normalized
       FROM orders o JOIN users u ON u.id = o.user_id ORDER BY o.created_at DESC LIMIT 20`,
  );
  const reconciliation = q.get<any>(
    `SELECT COUNT(*) AS mismatches
       FROM payment_attempts pa JOIN orders o ON o.id = pa.order_id
      WHERE pa.status = 'succeeded' AND o.status NOT IN ('paid','fulfilled','refunded')`,
  );
  const support = q.all<any>("SELECT * FROM support_cases ORDER BY created_at DESC LIMIT 10");
  const audit = q.all<any>(
    `SELECT a.id, a.action, a.resource_type, a.resource_id, a.reason, a.occurred_at, u.display_name AS actor
       FROM audit_logs a LEFT JOIN users u ON u.id = a.actor_user_id ORDER BY a.occurred_at DESC LIMIT 30`,
  );
  const events = q.all<any>(
    `SELECT provider, event_id, processing_status, signature_valid, received_at FROM provider_events ORDER BY received_at DESC LIMIT 10`,
  );

  const tabs = [
    { key: "content", label: `Content review (${counts.in_review})` },
    { key: "reports", label: `Reports (${counts.open_reports})` },
    { key: "orders", label: "Orders & reconciliation" },
    { key: "support", label: `Support (${counts.open_cases})` },
    { key: "audit", label: "Audit log" },
  ];

  return (
    <Shell
      title="Operations console"
      subtitle="Moderation, commerce, support and audit — every privileged action is logged."
      activePath="/admin"
      wide
    >
      <div className="grid-4 metrics">
        <div className="metric"><span className="label">Users</span><span className="value">{Number(counts.users)}</span><span className="delta">{Number(counts.attempts)} attempts recorded</span></div>
        <div className="metric"><span className="label">Published questions</span><span className="value">{Number(counts.questions)}</span><span className="delta">{Number(counts.in_review)} awaiting review</span></div>
        <div className="metric"><span className="label">Paid orders</span><span className="value">{Number(counts.paid_orders)}</span><span className="delta">{Number(reconciliation?.mismatches ?? 0)} reconciliation mismatches</span></div>
        <div className="metric"><span className="label">Queue</span><span className="value">{Number(counts.queued_jobs)}</span><span className="delta">jobs pending</span></div>
      </div>

      <div className="tabs" role="tablist">
        {tabs.map((t) => (
          <Link key={t.key} href={`/admin?tab=${t.key}`} className={`tab ${tab === t.key ? "active" : ""}`} role="tab" aria-selected={tab === t.key}>
            {t.label}
          </Link>
        ))}
      </div>

      {tab === "content" ? (
        <div className="split" style={{ gridTemplateColumns: "1.35fr 0.65fr", alignItems: "start" }}>
          <section className="card">
            <div className="card-head">
              <div>
                <div className="card-title">Moderation queue</div>
                <div className="card-sub">Publishing requires source, licence basis and an explanation</div>
              </div>
              <span className="badge amber">{reviewQueue.length} pending</span>
            </div>
            {reviewQueue.length === 0 ? (
              <div className="empty" style={{ padding: 26 }}><span className="small">Queue is clear.</span></div>
            ) : (
              <div className="stack">
                {reviewQueue.map((item) => (
                  <article key={item.id} className="card flat" style={{ borderColor: "var(--line)" }}>
                    <div className="row-between wrap" style={{ gap: 8 }}>
                      <span className="eyebrow">{item.subject_name}{item.topic_name ? ` · ${item.topic_name}` : ""}</span>
                      <span className="badge amber" style={{ textTransform: "capitalize" }}>{item.review_state.replace("_", " ")}</span>
                    </div>
                    <div className="small" style={{ margin: "8px 0" }} dangerouslySetInnerHTML={{ __html: item.stem }} />
                    <dl className="kv tiny" style={{ gridTemplateColumns: "110px 1fr" }}>
                      <dt>Source</dt><dd>{item.source_name || <span style={{ color: "var(--rose-600)" }}>missing</span>}</dd>
                      <dt>Licence</dt><dd>{item.license_basis || <span style={{ color: "var(--rose-600)" }}>missing</span>}</dd>
                      <dt>Author</dt><dd>{item.author ?? "—"}</dd>
                    </dl>
                    <ReviewActions questionVersionId={item.id} />
                  </article>
                ))}
              </div>
            )}
          </section>

          <aside className="stack">
            <section className="card">
              <div className="card-title">Content policy</div>
              <ul className="tiny muted" style={{ paddingLeft: 16, lineHeight: 1.85, margin: "6px 0 0" }}>
                <li>Unapproved content can never be served to learners.</li>
                <li>Published versions are immutable; corrections create a new version.</li>
                <li>Every decision records actor, time and reason.</li>
                <li>Withdrawal preserves the history of attempts that used the item.</li>
              </ul>
            </section>
            <section className="card">
              <div className="card-title">Provider events</div>
              {events.length === 0 ? <div className="tiny muted" style={{ marginTop: 4 }}>No webhook events yet.</div> : (
                <div className="stack-sm" style={{ marginTop: 6 }}>
                  {events.map((e) => (
                    <div key={e.event_id} className="row-between tiny">
                      <span className="mono" style={{ fontSize: 10.5 }}>{e.event_id.slice(0, 14)}…</span>
                      <span className={`badge ${e.signature_valid ? "" : "rose"}`}>{e.processing_status}</span>
                    </div>
                  ))}
                </div>
              )}
            </section>
          </aside>
        </div>
      ) : null}

      {tab === "reports" ? (
        <section className="card">
          <div className="card-head">
            <div className="card-title">Learner content reports</div>
            <span className="badge amber">{reports.length} open</span>
          </div>
          {reports.length === 0 ? (
            <div className="empty" style={{ padding: 26 }}><span className="small">No open reports.</span></div>
          ) : (
            <div className="stack">
              {reports.map((report) => (
                <article key={report.id} className="card flat">
                  <div className="row-between wrap">
                    <span className="badge rose" style={{ textTransform: "capitalize" }}>{report.reason.replace("_", " ")}</span>
                    <span className="tiny muted">by {report.reporter} · {relativeTime(report.created_at)}</span>
                  </div>
                  <div className="small" style={{ margin: "8px 0" }} dangerouslySetInnerHTML={{ __html: report.stem }} />
                  {report.details ? <div className="tiny muted">&ldquo;{report.details}&rdquo;</div> : null}
                  <ReviewActions questionVersionId={report.id} reportId={report.id} mode="report" />
                </article>
              ))}
            </div>
          )}
        </section>
      ) : null}

      {tab === "orders" ? (
        <div className="stack">
          <div className="banner info">
            <span className="ico">₦</span>
            Reconciliation compares verified provider success with internal order state. Mismatches:
            <strong> {Number(reconciliation?.mismatches ?? 0)}</strong>.
          </div>
          <section className="card">
            <div className="card-title">Recent orders</div>
            <div className="table-wrap" style={{ marginTop: 10 }}>
              <table className="data">
                <thead><tr><th>Reference</th><th>Buyer</th><th>Total</th><th>Status</th><th>Created</th><th /></tr></thead>
                <tbody>
                  {orders.map((order) => (
                    <tr key={order.id}>
                      <td className="mono small">{order.reference}</td>
                      <td><span className="small strong">{order.display_name}</span><div className="tiny muted">{order.email_normalized}</div></td>
                      <td className="small">{naira(order.total_minor)}</td>
                      <td><span className={`badge ${order.status === "paid" || order.status === "fulfilled" ? "" : order.status === "refunded" ? "neutral" : "amber"}`} style={{ textTransform: "capitalize" }}>{order.status.replace("_", " ")}</span></td>
                      <td className="small muted">{relativeTime(order.created_at)}</td>
                      <td style={{ textAlign: "right" }}>
                        {order.status === "paid" || order.status === "fulfilled" ? <RefundAction orderId={order.id} /> : null}
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          </section>
        </div>
      ) : null}

      {tab === "support" ? (
        <section className="card">
          <div className="card-title">Support queue</div>
          <div className="table-wrap" style={{ marginTop: 10 }}>
            <table className="data">
              <thead><tr><th>Subject</th><th>Category</th><th>Priority</th><th>Status</th><th>Opened</th></tr></thead>
              <tbody>
                {support.map((c) => (
                  <tr key={c.id}>
                    <td className="strong small">{c.subject}</td>
                    <td className="small" style={{ textTransform: "capitalize" }}>{c.category.replace("_", " ")}</td>
                    <td><span className={`badge ${c.priority === "urgent" ? "rose" : c.priority === "high" ? "amber" : "neutral"}`} style={{ textTransform: "capitalize" }}>{c.priority}</span></td>
                    <td className="small" style={{ textTransform: "capitalize" }}>{c.status}</td>
                    <td className="small muted">{relativeTime(c.created_at)}</td>
                  </tr>
                ))}
                {support.length === 0 ? <tr><td colSpan={5} className="small muted">No cases.</td></tr> : null}
              </tbody>
            </table>
          </div>
        </section>
      ) : null}

      {tab === "audit" ? (
        <section className="card">
          <div className="card-head">
            <div>
              <div className="card-title">Audit trail</div>
              <div className="card-sub">Append-only record of privileged and state-changing actions</div>
            </div>
            <span className="badge neutral">latest 30</span>
          </div>
          <div className="table-wrap">
            <table className="data">
              <thead><tr><th>Action</th><th>Resource</th><th>Actor</th><th>Reason</th><th>When</th></tr></thead>
              <tbody>
                {audit.map((entry) => (
                  <tr key={entry.id}>
                    <td className="mono small">{entry.action}</td>
                    <td className="small muted">{entry.resource_type}{entry.resource_id ? ` · ${entry.resource_id.slice(0, 8)}` : ""}</td>
                    <td className="small">{entry.actor ?? "system"}</td>
                    <td className="small muted">{entry.reason || "—"}</td>
                    <td className="small muted">{relativeTime(entry.occurred_at)}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </section>
      ) : null}
    </Shell>
  );
}
