import Link from "next/link";
import { Shell } from "@/components/Shell";
import { requirePageRole, relativeTime } from "@/lib/server/guards";
import { listClasses } from "@/lib/server/classes";
import { q } from "@/lib/server/db";
import { listPathways, listSubjects } from "@/lib/server/content";
import { CreateClassForm } from "./CreateClassForm";

export const dynamic = "force-dynamic";

export default async function TeacherPage() {
  const user = await requirePageRole(["teacher", "org_admin"], "/teacher");
  const classes = listClasses(user.id);
  const pathways = listPathways();
  const subjects = listSubjects();

  const pending = q.all<any>(
    `SELECT d.id, d.title, d.availability_end, c.name AS class_name,
            (SELECT COUNT(*) FROM assessment_sessions s WHERE s.definition_id = d.id AND s.status != 'in_progress') AS submissions
       FROM assessment_definitions d JOIN classes c ON c.id = d.class_id
       JOIN class_memberships cm ON cm.class_id = c.id AND cm.user_id = ? AND cm.role = 'teacher'
      WHERE d.status = 'published' ORDER BY d.created_at DESC LIMIT 6`,
    user.id,
  );

  const learners = q.get<any>(
    `SELECT COUNT(DISTINCT cm.user_id) AS n
       FROM class_memberships cm JOIN classes c ON c.id = cm.class_id
      WHERE c.owner_user_id = ? AND cm.role = 'student'`,
    user.id,
  );

  return (
    <Shell
      title={`Teacher overview`}
      subtitle={`${user.displayName} · ${classes.length} class${classes.length === 1 ? "" : "es"}`}
      activePath="/teacher"
      action={<Link className="btn sm" href="/exam">Assessment library</Link>}
    >
      <div className="grid-4 metrics">
        <div className="metric">
          <span className="label">Classes</span>
          <span className="value">{classes.length}</span>
        </div>
        <div className="metric">
          <span className="label">Learners</span>
          <span className="value">{Number(learners?.n ?? 0)}</span>
        </div>
        <div className="metric">
          <span className="label">Assessments</span>
          <span className="value">{pending.length}</span>
        </div>
        <div className="metric">
          <span className="label">Role</span>
          <span className="value" style={{ fontSize: 18, paddingTop: 8 }}>{user.roles.includes("org_admin") ? "Org admin" : "Teacher"}</span>
        </div>
      </div>

      <div className="split" style={{ gridTemplateColumns: "1.5fr 1fr", alignItems: "start" }}>
        <section className="stack">
          <div className="card">
            <div className="card-head">
              <div>
                <div className="card-title">My classes</div>
                <div className="card-sub">Only classes you teach are listed</div>
              </div>
            </div>
            {classes.length === 0 ? (
              <div className="empty">
                <span className="glyph">👥</span>
                <div className="strong">Create your first class</div>
                <div className="small">You will get a join code and an invitation link you can share.</div>
              </div>
            ) : (
              <div className="stack-sm">
                {classes.map((cls) => (
                  <div key={cls.id} className="row-between wrap" style={{ borderTop: "1px solid var(--line)", paddingTop: 12 }}>
                    <div>
                      <Link href={`/teacher/classes/${cls.id}`} className="strong small">{cls.name}</Link>
                      <div className="tiny muted">
                        {cls.student_count} learners · {cls.assessment_count} assessments · code <span className="mono">{cls.join_code}</span>
                      </div>
                    </div>
                    <div className="row" style={{ gap: 8 }}>
                      <Link className="btn secondary sm" href={`/classes/${cls.id}`}>View</Link>
                      <Link className="btn sm" href={`/teacher/classes/${cls.id}`}>Manage</Link>
                    </div>
                  </div>
                ))}
              </div>
            )}
          </div>

          <div className="card">
            <div className="card-head">
              <div className="card-title">Published assessments</div>
            </div>
            {pending.length === 0 ? (
              <div className="tiny muted">Nothing published yet. Open a class to schedule one.</div>
            ) : (
              <div className="stack-sm">
                {pending.map((a) => (
                  <div key={a.id} className="row-between wrap" style={{ borderTop: "1px solid var(--line)", paddingTop: 10 }}>
                    <div>
                      <div className="strong small">{a.title}</div>
                      <div className="tiny muted">
                        {a.class_name} · {a.submissions} submissions
                        {a.availability_end ? ` · closes ${new Date(a.availability_end).toLocaleDateString("en-NG")}` : ""}
                      </div>
                    </div>
                    <span className="badge">{a.submissions} done</span>
                  </div>
                ))}
              </div>
            )}
          </div>
        </section>

        <aside className="stack aside-sticky" style={{ position: "sticky", top: 84 }}>
          <section className="card">
            <div className="card-title">Create a class</div>
            <p className="tiny muted" style={{ margin: "4px 0 12px" }}>
              Classes you create are owned by you. Learners join with a code or an expiring invitation link.
            </p>
            <CreateClassForm subjects={subjects} />
          </section>
          <section className="card">
            <div className="card-title">Assessment policy</div>
            <ul className="tiny muted" style={{ paddingLeft: 16, lineHeight: 1.8, margin: "6px 0 0" }}>
              <li>Assessments draw only from approved, published questions.</li>
              <li>Each attempt is server-timed and immutable once submitted.</li>
              <li>You can withhold results until the window closes.</li>
              <li>Question keys are never sent to learners before release.</li>
            </ul>
          </section>
        </aside>
      </div>
    </Shell>
  );
}
