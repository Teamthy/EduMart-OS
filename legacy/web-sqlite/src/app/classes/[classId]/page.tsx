import Link from "next/link";
import { notFound } from "next/navigation";
import { Shell } from "@/components/Shell";
import { requirePageUser, relativeTime } from "@/lib/server/guards";
import { classForUser, classMembers } from "@/lib/server/classes";
import { classReport } from "@/lib/server/insights";

export const dynamic = "force-dynamic";

export default async function ClassDetailPage({ params }: { params: Promise<{ classId: string }> }) {
  const user = await requirePageUser("/classes");
  const { classId } = await params;
  const scope = classForUser(user.id, classId);
  if (!scope) notFound();

  const { members } = classMembers(user, classId);
  const isTeacher = scope.role === "teacher" || scope.owner_user_id === user.id || user.roles.includes("platform_admin");
  const report = isTeacher ? classReport(classId) : null;

  return (
    <Shell
      title={scope.name}
      subtitle={isTeacher ? "Teacher view" : "Class"}
      activePath="/classes"
      action={
        <div className="btn-row">
          {isTeacher ? <Link className="btn sm" href={`/teacher/classes/${classId}`}>Manage</Link> : null}
          <Link className="btn secondary sm" href="/classes">All classes</Link>
        </div>
      }
    >
      <div className="grid-4 metrics">
        <div className="metric">
          <span className="label">Learners</span>
          <span className="value">{members.filter((m) => m.role === "student").length}</span>
        </div>
        <div className="metric">
          <span className="label">Assessments</span>
          <span className="value">{report?.assessments.length ?? "—"}</span>
        </div>
        <div className="metric">
          <span className="label">Class average</span>
          <span className="value">
            {report && report.assessments.length > 0
              ? `${Math.round(report.assessments.reduce((s, a) => s + Number(a.average), 0) / report.assessments.filter((a) => Number(a.submissions) > 0).length || 0)}%`
              : "—"}
          </span>
        </div>
        <div className="metric">
          <span className="label">Join code</span>
          <span className="value mono" style={{ fontSize: 20 }}>{scope.join_code}</span>
        </div>
      </div>

      {isTeacher && report ? (
        <section className="stack">
          <div className="split">
            <div className="card">
              <div className="card-head">
                <div>
                  <div className="card-title">Assessments</div>
                  <div className="card-sub">Scheduled and published for this class</div>
                </div>
              </div>
              {report.assessments.length === 0 ? (
                <div className="empty" style={{ padding: 22 }}>
                  <span className="small">No assessments yet.</span>
                  <Link className="btn sm" href={`/teacher/classes/${classId}`}>Create one</Link>
                </div>
              ) : (
                <div className="stack-sm">
                  {report.assessments.map((a) => (
                    <div key={a.id} className="row-between wrap" style={{ borderTop: "1px solid var(--line)", paddingTop: 10 }}>
                      <div>
                        <div className="strong small">{a.title}</div>
                        <div className="tiny muted">
                          {a.question_count} questions · {Math.round(a.duration_seconds / 60)} min · {a.submissions} submissions
                        </div>
                      </div>
                      <span className={`badge ${Number(a.average) >= 70 ? "" : Number(a.average) >= 45 ? "amber" : "rose"}`}>
                        avg {Math.round(Number(a.average))}%
                      </span>
                    </div>
                  ))}
                </div>
              )}
            </div>

            <div className="card">
              <div className="card-head">
                <div className="card-title">Learner performance</div>
                <span className="badge neutral">Class-scoped</span>
              </div>
              <div className="stack-sm">
                {report.members.map((m) => (
                  <div key={m.id} className="row-between" style={{ borderTop: "1px solid var(--line)", paddingTop: 8 }}>
                    <div>
                      <div className="small strong">{m.display_name}</div>
                      <div className="tiny muted">{m.attempts} attempts · joined {relativeTime(m.joined_at)}</div>
                    </div>
                    <span className="badge neutral">{Math.round(Number(m.average))}%</span>
                  </div>
                ))}
                {report.members.length === 0 ? <div className="tiny muted">No learners yet — share the join code.</div> : null}
              </div>
            </div>
          </div>
        </section>
      ) : (
        <section className="card">
          <div className="card-head">
            <div className="card-title">Your class assessments</div>
            <Link className="btn ghost sm" href="/exam">Open assessments</Link>
          </div>
          <p className="small muted">
            Assessments scheduled for this class appear under Assessments. You can see your own results and
            the class list, but not other learners&apos; scores.
          </p>
        </section>
      )}

      <section className="card">
        <div className="card-head">
          <div className="card-title">Members</div>
          <span className="badge neutral">{members.length}</span>
        </div>
        <div className="table-wrap">
          <table className="data">
            <thead>
              <tr>
                <th>Name</th>
                <th>Role</th>
                <th>Joined</th>
                {isTeacher ? <th>Attempts</th> : null}
                {isTeacher ? <th>Average</th> : null}
              </tr>
            </thead>
            <tbody>
              {members.map((m) => (
                <tr key={m.id}>
                  <td><span className="strong">{m.display_name}</span>{isTeacher ? <div className="tiny muted">{m.email_normalized}</div> : null}</td>
                  <td><span className={`badge ${m.role === "teacher" ? "indigo" : "neutral"}`} style={{ textTransform: "capitalize" }}>{m.role}</span></td>
                  <td className="small muted">{relativeTime(m.joined_at)}</td>
                  {isTeacher ? <td className="small">{m.attempts}</td> : null}
                  {isTeacher ? <td className="small">{Math.round(Number(m.average))}%</td> : null}
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </section>
    </Shell>
  );
}
