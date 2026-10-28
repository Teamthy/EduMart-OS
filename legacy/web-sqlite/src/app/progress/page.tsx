import Link from "next/link";
import { Shell } from "@/components/Shell";
import { requirePageUser, scoreTone, relativeTime } from "@/lib/server/guards";
import { learningSummary, recentAttempts, topicStrength } from "@/lib/server/insights";

export const dynamic = "force-dynamic";

export default async function ProgressPage() {
  const user = await requirePageUser("/progress");
  const summary = learningSummary(user.id);
  const attempts = recentAttempts(user.id, 20);
  const topics = topicStrength(user.id, 12);

  // Score trend, oldest → newest for the sparkline.
  const trend = [...attempts].reverse().filter((a) => a.score_percent !== null).slice(-12);
  const maxTrend = 100;

  return (
    <Shell
      title="Progress"
      subtitle="Everything here is derived from your completed attempts."
      activePath="/progress"
      action={<Link className="btn sm" href="/practice">Practise a weak topic</Link>}
    >
      <div className="grid-4 metrics">
        <div className="metric">
          <span className="label">Average score</span>
          <span className="value">{summary.averagePercent}<small>%</small></span>
          <span className="delta">Best {summary.bestPercent}%</span>
        </div>
        <div className="metric">
          <span className="label">Accuracy</span>
          <span className="value">{summary.accuracy}<small>%</small></span>
          <span className="delta">{summary.questionsAnswered} answered</span>
        </div>
        <div className="metric">
          <span className="label">Current streak</span>
          <span className="value">{summary.streak.current}<small> days</small></span>
          <span className="delta">Longest {summary.streak.longest}</span>
        </div>
        <div className="metric">
          <span className="label">Learning points</span>
          <span className="value">{summary.points}</span>
          <span className="delta">{summary.attempts} attempts</span>
        </div>
      </div>

      <section className="card">
        <div className="card-head">
          <div>
            <div className="card-title">Score trend</div>
            <div className="card-sub">Last {trend.length} completed attempts</div>
          </div>
        </div>
        {trend.length === 0 ? (
          <div className="empty">
            <span className="glyph">◔</span>
            <div className="strong">No trend yet</div>
            <div className="small">Complete two or more attempts to see how you are improving.</div>
          </div>
        ) : (
          <div className="row" style={{ alignItems: "flex-end", gap: 10, height: 160, padding: "10px 0" }}>
            {trend.map((attempt) => {
              const pct = attempt.score_percent ?? 0;
              return (
                <div key={attempt.id} style={{ flex: 1, display: "flex", flexDirection: "column", alignItems: "center", gap: 6 }}>
                  <span className="tiny muted">{pct}%</span>
                  <div
                    title={`${attempt.title} — ${pct}%`}
                    style={{
                      width: "100%",
                      maxWidth: 44,
                      height: `${Math.max((pct / maxTrend) * 110, 6)}px`,
                      borderRadius: "7px 7px 3px 3px",
                      background: pct >= 70 ? "var(--green-600)" : pct >= 45 ? "var(--amber-500)" : "var(--rose-600)",
                    }}
                  />
                  <span className="tiny muted" style={{ fontSize: 10 }}>{new Date(attempt.submitted_at ?? attempt.started_at).toLocaleDateString("en-NG", { day: "numeric", month: "short" })}</span>
                </div>
              );
            })}
          </div>
        )}
      </section>

      <div className="split">
        <section className="card">
          <div className="card-head">
            <div>
              <div className="card-title">Topic strengths & weaknesses</div>
              <div className="card-sub">Ordered by accuracy, weakest first</div>
            </div>
          </div>
          {topics.length === 0 ? (
            <div className="empty" style={{ padding: 26 }}><span className="small">Answer questions to build topic analytics.</span></div>
          ) : (
            <div className="stack-sm">
              {topics.map((topic) => (
                <div key={topic.topic_id} className="bar-row">
                  <span className="small strong" style={{ overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }} title={topic.topic_name}>
                    {topic.topic_name}
                  </span>
                  <span className={`bar`}>
                    <i style={{ width: `${Math.max(topic.accuracy, 3)}%` }} className={topic.accuracy >= 70 ? "" : topic.accuracy >= 45 ? "mid" : "low"} />
                  </span>
                  <span className="tiny muted" style={{ textAlign: "right" }}>{topic.accuracy}%</span>
                </div>
              ))}
            </div>
          )}
        </section>

        <section className="card">
          <div className="card-head">
            <div className="card-title">Attempt history</div>
            <span className="badge neutral">{attempts.length}</span>
          </div>
          {attempts.length === 0 ? (
            <div className="empty" style={{ padding: 26 }}>
              <span className="glyph">✎</span>
              <div className="small">No attempts recorded yet.</div>
            </div>
          ) : (
            <div>
              {attempts.map((attempt) => (
                <div key={attempt.id} className="attempt-row">
                  <div>
                    <div className="strong small">{attempt.title}</div>
                    <div className="tiny muted">
                      {relativeTime(attempt.submitted_at ?? attempt.started_at)} · {attempt.status}
                    </div>
                  </div>
                  <span className={`badge ${scoreTone(attempt.score_percent)}`}>{attempt.score_percent ?? 0}%</span>
                  <Link className="btn ghost sm" href={`/result/${attempt.id}`}>Open</Link>
                </div>
              ))}
            </div>
          )}
        </section>
      </div>
    </Shell>
  );
}
