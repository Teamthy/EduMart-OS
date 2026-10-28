import Link from "next/link";
import { notFound } from "next/navigation";
import { Shell } from "@/components/Shell";
import { requirePageRole, relativeTime } from "@/lib/server/guards";
import { classForUser, requireClassTeacher, classMembers } from "@/lib/server/classes";
import { classReport } from "@/lib/server/insights";
import { listSubjects, listTopics } from "@/lib/server/content";
import { AssessmentBuilder } from "./AssessmentBuilder";
import { InvitePanel } from "./InvitePanel";

export const dynamic = "force-dynamic";

export default async function ManageClassPage({ params }: { params: Promise<{ classId: string }> }) {
  const user = await requirePageRole(["teacher", "org_admin"], "/teacher");
  const { classId } = await params;
  const scope = classForUser(user.id, classId);
  if (!scope) notFound();
  requireClassTeacher(user, classId);

  const report = classReport(classId);
  const { members } = classMembers(user, classId);
  const subjects = listSubjects();

  // Topic list for the default subject, so the builder can offer focused scopes.
  const subjectId = scope.subject_id ?? subjects[0]?.id;
  const topics = subjectId ? listTopics(subjectId) : [];

  return (
    <Shell
      title={scope.name}
      subtitle="Manage class, learners and assessments"
      activePath="/teacher"
      action={<div className="btn-row"><Link className="btn secondary sm" href={`/classes/${classId}`}>Learner view</Link><Link className="btn sm" href="/teacher">Overview</Link></div>}
    >
      <div className="grid-4 metrics">
        <div className="metric"><span className="label">Learners</span><span className="value">{members.filter((m) => m.role === "student").length}</span></div>
        <div className="metric"><span className="label">Assessments</span><span className="value">{report.assessments.length}</span></div>
        <div className="metric"><span className="label">Submissions</span><span className="value">{report.assessments.reduce((s, a) => s + Number(a.submissions), 0)}</span></div>
        <div className="metric"><span className="label">Join code</span><span className="value mono" style={{ fontSize: 19 }}>{scope.join_code}</span></div>
      </div>

      <div className="split" style={{ gridTemplateColumns: "1.35fr 1fr", alignItems: "start" }}>
        <div className="stack">
          <section className="card">
            <div className="card-head">
              <div>
                <div className="card-title">Schedule an assessment</div>
                <div className="card-sub">Built from approved, published questions only</div>
              </div>
            </div>
            <AssessmentBuilder classId={classId} subjects={subjects} initialSubjectId={subjectId} initialTopics={topics} />
          </section>

          <section className="card">
            <div className="card-head">
              <div className="card-title">Assessments</div>
              <span className="badge neutral">{report.assessments.length}</span>
            </div>
            {report.assessments.length === 0 ? (
              <div className="empty" style={{ padding: 24 }}><span className="small">None scheduled yet.</span></div>
            ) : (
              <div className="table-wrap">
                <table className="data">
                  <thead>
                    <tr><th>Title</th><th>Questions</th><th>Window opens</th><th>Closes</th><th>Submissions</th><th>Average</th></tr>
                  </thead>
                  <tbody>
                    {report.assessments.map((a) => (
                      <tr key={a.id}>
                        <td className="strong">{a.title}</td>
                        <td className="small">{a.question_count}</td>
                        <td className="small muted">{a.availability_start ? new Date(a.availability_start).toLocaleDateString("en-NG") : "—"}</td>
                        <td className="small muted">{a.availability_end ? new Date(a.availability_end).toLocaleDateString("en-NG") : "Open"}</td>
                        <td className="small">{a.submissions}</td>
                        <td className="small">{Math.round(Number(a.average))}%</td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            )}
          </section>

          <section className="card">
            <div className="card-head">
              <div className="card-title">Learners</div>
              <span className="badge neutral">{members.filter((m) => m.role === "student").length}</span>
            </div>
            <div className="table-wrap">
              <table className="data">
                <thead><tr><th>Name</th><th>Email</th><th>Attempts</th><th>Average</th><th>Joined</th></tr></thead>
                <tbody>
                  {members.filter((m) => m.role === "student").map((m) => (
                    <tr key={m.id}>
                      <td className="strong">{m.display_name}</td>
                      <td className="small muted">{m.email_normalized}</td>
                      <td className="small">{m.attempts}</td>
                      <td className="small">{Math.round(Number(m.average))}%</td>
                      <td className="small muted">{relativeTime(m.joined_at)}</td>
                    </tr>
                  ))}
                  {members.filter((m) => m.role === "student").length === 0 ? (
                    <tr><td colSpan={5} className="small muted">No learners yet.</td></tr>
                  ) : null}
                </tbody>
              </table>
            </div>
          </section>
        </div>

        <aside className="stack" style={{ position: "sticky", top: 84 }}>
          <section className="card">
            <div className="card-title">Invite learners</div>
            <p className="tiny muted" style={{ margin: "4px 0 12px" }}>
              Links are expiring, limited-use and revocable. Tokens are stored hashed.
            </p>
            <InvitePanel classId={classId} joinCode={scope.join_code} />
          </section>
          <section className="card">
            <div className="card-title">Class data boundary</div>
            <p className="tiny muted" style={{ marginTop: 4 }}>
              Every query for this page is scoped to this class on the server. A teacher outside this class
              receives a 404 rather than a filtered list, and learners never see other learners&apos; scores.
            </p>
          </section>
        </aside>
      </div>
    </Shell>
  );
}
