import Link from "next/link";
import { Shell } from "@/components/Shell";
import { requirePageUser } from "@/lib/server/guards";
import { listMockDefinitions } from "@/lib/server/content";
import { q } from "@/lib/server/db";
import { listClasses } from "@/lib/server/classes";

export const dynamic = "force-dynamic";

export default async function ExamListPage() {
  const user = await requirePageUser("/exam");
  const mocks = listMockDefinitions();
  const classes = listClasses(user.id);
  const classIds = classes.map((c) => c.id);

  const classAssessments = classIds.length
    ? q.all<any>(
        `SELECT d.id, d.title, d.duration_seconds, d.availability_start, d.availability_end, d.attempt_limit,
                c.name AS class_name,
                (SELECT COUNT(*) FROM assessment_items ai WHERE ai.definition_id = d.id) AS question_count,
                (SELECT COUNT(*) FROM assessment_sessions s WHERE s.definition_id = d.id AND s.user_id = ?) AS my_attempts
           FROM assessment_definitions d JOIN classes c ON c.id = d.class_id
          WHERE d.status = 'published' AND d.class_id IN (${classIds.map(() => "?").join(",")})
          ORDER BY d.availability_end ASC`,
        user.id,
        ...classIds,
      )
    : [];

  return (
    <Shell
      title="Assessments"
      subtitle="Timed, server-authoritative mock exams and class assessments."
      activePath="/exam"
      action={<Link className="btn secondary sm" href="/practice">Untimed practice</Link>}
    >
      <div className="banner info">
        <span className="ico">⏱</span>
        <span>
          Once an assessment starts, the server owns the clock. Your answers save automatically and are
          confirmed by the server — changing your device time will not extend the deadline.
        </span>
      </div>

      <section className="stack">
        <h2 style={{ fontSize: "1.25rem" }}>Public mock exams</h2>
        {mocks.length === 0 ? (
          <div className="empty">
            <span className="glyph">⏱</span>
            <div className="strong">No mock exams published yet</div>
            <div className="small">Check back soon, or start an untimed practice session.</div>
          </div>
        ) : (
          <div className="grid-3">
            {mocks.map((mock) => (
              <article key={mock.id} className="card interactive stack">
                <div className="row-between">
                  <span className="badge neutral">{Math.round(mock.duration_seconds / 60)} minutes</span>
                  <span className="tiny muted">{mock.question_count} questions</span>
                </div>
                <h3 style={{ fontSize: "1.05rem" }}>{mock.title}</h3>
                <div className="tiny muted">{mock.attempt_limit} attempts allowed · scored on submit</div>
                <Link className="btn block sm" href={`/exam/${mock.id}`}>View and start</Link>
              </article>
            ))}
          </div>
        )}
      </section>

      <section className="stack">
        <h2 style={{ fontSize: "1.25rem" }}>Class assessments</h2>
        {classAssessments.length === 0 ? (
          <div className="empty">
            <span className="glyph">👥</span>
            <div className="strong">Nothing assigned right now</div>
            <div className="small">When a teacher schedules an assessment for your class, it appears here.</div>
            <Link className="btn sm" href="/classes">View classes</Link>
          </div>
        ) : (
          <div className="table-wrap">
            <table className="data">
              <thead>
                <tr>
                  <th>Assessment</th>
                  <th>Class</th>
                  <th>Duration</th>
                  <th>Attempts</th>
                  <th>Window closes</th>
                  <th />
                </tr>
              </thead>
              <tbody>
                {classAssessments.map((a) => {
                  const left = Number(a.attempt_limit) - Number(a.my_attempts);
                  return (
                    <tr key={a.id}>
                      <td><span className="strong">{a.title}</span><div className="tiny muted">{a.question_count} questions</div></td>
                      <td className="small">{a.class_name}</td>
                      <td className="small">{Math.round(a.duration_seconds / 60)} min</td>
                      <td className="small">{left > 0 ? `${left} left` : "Used"}</td>
                      <td className="small">{a.availability_end ? new Date(a.availability_end).toLocaleDateString("en-NG") : "Open"}</td>
                      <td style={{ textAlign: "right" }}>
                        <Link className="btn sm" href={`/exam/${a.id}`} aria-disabled={left <= 0}>Open</Link>
                      </td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          </div>
        )}
      </section>
    </Shell>
  );
}
