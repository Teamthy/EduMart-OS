import Link from "next/link";
import { Shell } from "@/components/Shell";
import { requirePageUser } from "@/lib/server/guards";
import { q } from "@/lib/server/db";

export const dynamic = "force-dynamic";

export default async function SupportPage() {
  const user = await requirePageUser("/support");
  const cases = q.all<any>(
    "SELECT id, category, status, priority, subject, created_at FROM support_cases WHERE opened_by = ? ORDER BY created_at DESC",
    user.id,
  );

  return (
    <Shell title="Help & support" subtitle="Payment, access, assessment and privacy requests." activePath="/account">
      <div className="split" style={{ gridTemplateColumns: "1.3fr 0.7fr" }}>
        <section className="card">
          <div className="card-head">
            <div className="card-title">Your cases</div>
            <Link className="btn sm" href="/account">Open a new case</Link>
          </div>
          {cases.length === 0 ? (
            <div className="empty" style={{ padding: 26 }}>
              <span className="glyph">✉</span>
              <div className="small">No cases yet. Open one from Settings if something looks wrong.</div>
            </div>
          ) : (
            <div className="table-wrap">
              <table className="data">
                <thead><tr><th>Subject</th><th>Category</th><th>Priority</th><th>Status</th><th>Opened</th></tr></thead>
                <tbody>
                  {cases.map((c) => (
                    <tr key={c.id}>
                      <td className="strong">{c.subject}</td>
                      <td className="small" style={{ textTransform: "capitalize" }}>{c.category.replace("_", " ")}</td>
                      <td className="small" style={{ textTransform: "capitalize" }}>{c.priority}</td>
                      <td><span className="badge neutral" style={{ textTransform: "capitalize" }}>{c.status}</span></td>
                      <td className="small muted">{new Date(c.created_at).toLocaleDateString("en-NG")}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
        </section>
        <aside className="card stack">
          <div className="card-title">Response commitments</div>
          <ul className="small muted" style={{ paddingLeft: 16, lineHeight: 1.9, margin: 0 }}>
            <li>Exam access or submission failure: urgent, same day.</li>
            <li>Payment or entitlement mismatch: high, one business day.</li>
            <li>Content correction: reviewed within three days.</li>
            <li>Data/privacy requests: acknowledged within 72 hours.</li>
          </ul>
          <div className="banner info small">
            <span className="ico">ℹ</span>
            For assessment disputes include the subject and approximate start time so we can locate the attempt.
          </div>
        </aside>
      </div>
    </Shell>
  );
}
