import Link from "next/link";
import { Shell } from "@/components/Shell";
import { requirePageUser, relativeTime, daysUntil, scoreTone } from "@/lib/server/guards";
import { learningSummary } from "@/lib/server/insights";
import { listClasses } from "@/lib/server/classes";
import { listSubjects, listPathways } from "@/lib/server/content";
import { q } from "@/lib/server/db";

export const dynamic = "force-dynamic";

export default async function DashboardPage() {
  const user = await requirePageUser("/dashboard");
  const summary = learningSummary(user.id);
  const classes = listClasses(user.id);
  const pathways = listPathways();
  const currentPathway = pathways.find((p) => p.id === user.examPathwayId) ?? pathways[0];
  const subjects = listSubjects(currentPathway?.id).slice(0, 6);
  const targetIn = daysUntil(user.targetDate);

  const assignments = q.all<any>(
    `SELECT d.id, d.title, d.duration_seconds, d.availability_end, c.name AS class_name,
            (SELECT COUNT(*) FROM assessment_sessions s WHERE s.definition_id = d.id AND s.user_id = ?) AS my_attempts,
            d.attempt_limit
       FROM assessment_definitions d
       JOIN class_memberships cm ON cm.class_id = d.class_id AND cm.user_id = ? AND cm.status = 'active'
       LEFT JOIN classes c ON c.id = d.class_id
      WHERE d.status = 'published'
        AND (d.availability_end IS NULL OR d.availability_end > ?)
      ORDER BY d.availability_end ASC LIMIT 4`,
    user.id,
    user.id,
    new Date().toISOString(),
  );

  const firstName = user.displayName.split(" ")[0];
  const weakest = summary.topics.filter((t) => t.accuracy < 60).slice(0, 3);

  return (
    <Shell
      title={`Welcome back, ${firstName}`}
      subtitle={currentPathway ? `${currentPathway.name} preparation` : "Set your exam pathway to get started"}
      activePath="/dashboard"
      action={<Link className="btn sm" href="/practice">Start practice</Link>}
    >
      {summary.active ? (
        <div className="banner warn">
          <span className="ico">⏱</span>
          <div style={{ flex: 1 }}>
            <strong>You have an attempt in progress.</strong>{" "}
            {summary.active.title ?? "Practice session"} — the server deadline is still running.
          </div>
          <Link className="btn sm" href={`/attempt/${summary.active.id}`}>Resume</Link>
        </div>
      ) : null}

      {targetIn !== null ? (
        <div className="grid-4 metrics">
          <div className="metric">
            <span className="label">Days to exam</span>
            <span className="value">{Math.max(targetIn, 0)}</span>
            <span className="delta">{user.targetDate ? new Date(user.targetDate).toLocaleDateString("en-NG", { day: "numeric", month: "long" }) : ""}</span>
          </div>
          <div className="metric">
            <span className="label">Attempts completed</span>
            <span className="value">{summary.attempts}</span>
            <span className="delta">{summary.questionsAnswered} answers recorded</span>
          </div>
          <div className="metric">
            <span className="label">Accuracy</span>
            <span className="value">{summary.accuracy}<small>%</small></span>
            <span className="delta">Average score {summary.averagePercent}%</span>
          </div>
          <div className="metric">
            <span className="label">Learning points</span>
            <span className="value">{summary.points}</span>
            <span className="delta">🔥 {summary.streak.current}-day streak</span>
          </div>
        </div>
      ) : (
        <div className="grid-4 metrics">
          <div className="metric">
            <span className="label">Attempts</span>
            <span className="value">{summary.attempts}</span>
          </div>
          <div className="metric">
            <span className="label">Accuracy</span>
            <span className="value">{summary.accuracy}<small>%</small></span>
          </div>
          <div className="metric">
            <span className="label">Streak</span>
            <span className="value">{summary.streak.current}<small>d</small></span>
          </div>
          <div className="metric">
            <span className="label">Points</span>
            <span className="value">{summary.points}</span>
          </div>
        </div>
      )}

      <div className="split" style={{ gridTemplateColumns: "1.5fr 1fr" }}>
        <section className="card">
          <div className="card-head">
            <div>
              <div className="card-title">Pick up where you left off</div>
              <div className="card-sub">Subjects in {currentPathway?.name}</div>
            </div>
            <Link className="btn ghost sm" href="/practice">Configure</Link>
          </div>
          <div className="grid-3" style={{ gap: 10 }}>
            {subjects.map((subject) => (
              <Link key={subject.id} href={`/practice?subject=${subject.id}`} className="card tight interactive subject-card" style={{ gap: 8 }}>
                <span className="subject-icon" aria-hidden>{subject.icon}</span>
                <div>
                  <div className="strong small">{subject.name}</div>
                  <div className="tiny muted">{subject.question_count} questions</div>
                </div>
              </Link>
            ))}
          </div>
        </section>

        <section className="card">
          <div className="card-head">
            <div className="card-title">Focus next</div>
            <Link className="btn ghost sm" href="/progress">All insights</Link>
          </div>
          {weakest.length === 0 ? (
            <div className="empty" style={{ padding: "26px 14px" }}>
              <span className="glyph">◔</span>
              <div className="small">Complete an attempt to see topic-level strengths.</div>
            </div>
          ) : (
            <div className="stack-sm">
              {weakest.map((topic) => (
                <div key={topic.topic_id} className="stack-sm">
                  <div className="row-between">
                    <span className="small strong">{topic.topic_name}</span>
                    <span className={`badge ${scoreTone(topic.accuracy)}`}>{topic.accuracy}%</span>
                  </div>
                  <div className="progress"><span style={{ width: `${Math.max(topic.accuracy, 4)}%` }} /></div>
                  <div className="tiny muted">{topic.subject_name} · {topic.correct}/{topic.attempts} correct</div>
                </div>
              ))}
            </div>
          )}
        </section>
      </div>

      {assignments.length > 0 ? (
        <section className="card">
          <div className="card-head">
            <div>
              <div className="card-title">Class assignments</div>
              <div className="card-sub">Set by your teachers</div>
            </div>
            <Link className="btn ghost sm" href="/classes">My classes</Link>
          </div>
          <div className="stack-sm">
            {assignments.map((a) => {
              const used = Number(a.my_attempts);
              const left = Number(a.attempt_limit) - used;
              return (
                <div key={a.id} className="row-between wrap" style={{ borderTop: "1px solid var(--line)", paddingTop: 12 }}>
                  <div>
                    <div className="strong small">{a.title}</div>
                    <div className="tiny muted">
                      {a.class_name} · {Math.round(a.duration_seconds / 60)} min · {left > 0 ? `${left} attempt${left === 1 ? "" : "s"} left` : "no attempts left"}
                    </div>
                  </div>
                  <div className="row" style={{ gap: 8 }}>
                    <span className="badge neutral">
                      {a.availability_end
                        ? `Closes ${new Date(a.availability_end).toLocaleDateString("en-NG", { day: "numeric", month: "short" })}`
                        : "Open"}
                    </span>
                    <Link className="btn sm" href={`/exam/${a.id}`} aria-disabled={left <= 0}>
                      {used > 0 ? "Try again" : "Start"}
                    </Link>
                  </div>
                </div>
              );
            })}
          </div>
        </section>
      ) : null}

      <div className="split">
        <section className="card">
          <div className="card-head">
            <div className="card-title">Recent attempts</div>
            <Link className="btn ghost sm" href="/progress">View all</Link>
          </div>
          {summary.recent.length === 0 ? (
            <div className="empty">
              <span className="glyph">✎</span>
              <div className="small strong">No attempts yet</div>
              <div className="tiny">Start a short practice session to see your first result.</div>
              <Link className="btn sm" href="/practice">Start practice</Link>
            </div>
          ) : (
            <div>
              {summary.recent.map((attempt) => (
                <div key={attempt.id} className="attempt-row">
                  <div>
                    <div className="strong small">{attempt.title}</div>
                    <div className="tiny muted">
                      {attempt.mode === "practice" ? "Practice" : attempt.mode === "mock" ? "Mock exam" : "Class assessment"} ·{" "}
                      {relativeTime(attempt.submitted_at ?? attempt.started_at)}
                    </div>
                  </div>
                  <span className={`badge ${scoreTone(attempt.score_percent)}`}>
                    {attempt.score_percent ?? 0}% · {attempt.score_raw ?? 0}/{attempt.score_max ?? 0}
                  </span>
                  <Link className="btn secondary sm" href={`/result/${attempt.id}`}>Review</Link>
                </div>
              ))}
            </div>
          )}
        </section>

        <section className="card">
          <div className="card-head">
            <div className="card-title">Your classes</div>
            <Link className="btn ghost sm" href="/classes">Manage</Link>
          </div>
          {classes.length === 0 ? (
            <div className="empty" style={{ padding: "26px 14px" }}>
              <span className="glyph">👥</span>
              <div className="small">Join a class with the code your teacher shared.</div>
              <Link className="btn sm" href="/classes">Join a class</Link>
            </div>
          ) : (
            <div className="stack-sm">
              {classes.map((cls) => (
                <Link key={cls.id} href={`/classes/${cls.id}`} className="row-between" style={{ padding: "10px 0", borderTop: "1px solid var(--line)" }}>
                  <span>
                    <span className="strong small" style={{ display: "block" }}>{cls.name}</span>
                    <span className="tiny muted">{cls.organization_name ?? "Personal class"} · {cls.student_count} learners</span>
                  </span>
                  <span className="badge neutral">{cls.role}</span>
                </Link>
              ))}
            </div>
          )}
          <div className="divider" style={{ margin: "12px 0" }} />
          <Link className="btn secondary block sm" href="/progress">See your readiness</Link>
        </section>
      </div>
    </Shell>
  );
}
