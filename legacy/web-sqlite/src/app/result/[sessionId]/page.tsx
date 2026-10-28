import Link from "next/link";
import { notFound, redirect } from "next/navigation";
import { Shell } from "@/components/Shell";
import { requirePageUser, scoreTone, relativeTime } from "@/lib/server/guards";
import { getSessionRow, readSnapshot, sessionView } from "@/lib/server/assessment";
import { definitionById } from "@/lib/server/content";
import { isResultReleased } from "@/lib/domain/exam";
import { ReportQuestion } from "./ReportQuestion";

export const dynamic = "force-dynamic";

export default async function ResultPage({ params }: { params: Promise<{ sessionId: string }> }) {
  const user = await requirePageUser();
  const { sessionId } = await params;
  const session = getSessionRow(sessionId);
  if (!session || session.user_id !== user.id) notFound();
  if (session.status === "in_progress") redirect(`/attempt/${sessionId}`);

  const definition = session.definition_id ? definitionById(session.definition_id) : null;
  const released = isResultReleased(
    { status: session.status, releaseAt: session.release_at },
    (definition?.result_release as "immediate" | "on_close") ?? "immediate",
    definition?.availability_end ?? null,
  );

  const view = sessionView(session, { includeAnswers: true });
  const snapshot = readSnapshot(session);
  const review = "review" in view ? (view.review as any[]) : [];
  const questions = view.questions;

  const correct = review.filter((r) => {
    const q = questions.find((qq) => qq.qvId === r.qvId);
    return q?.selectedOptionId && q.selectedOptionId === r.answerOptionId;
  }).length;
  const unanswered = questions.filter((q) => !q.selectedOptionId).length;
  const incorrect = questions.length - correct - unanswered;

  const topicMap = new Map<string, { name: string; total: number; right: number }>();
  for (const r of review) {
    const q = questions.find((qq) => qq.qvId === r.qvId);
    if (!r.topicName) continue;
    const entry = topicMap.get(r.topicName) ?? { name: r.topicName, total: 0, right: 0 };
    entry.total += 1;
    if (q?.selectedOptionId === r.answerOptionId) entry.right += 1;
    topicMap.set(r.topicName, entry);
  }
  const topics = [...topicMap.values()].sort((a, b) => a.right / a.total - b.right / b.total);

  const percent = session.score_percent ?? 0;
  const durationMin = session.submitted_at
    ? Math.max(1, Math.round((new Date(session.submitted_at).getTime() - new Date(session.started_at).getTime()) / 60000))
    : null;

  return (
    <Shell
      title="Result"
      subtitle={definition?.title ?? (session.mode === "practice" ? "Practice session" : "Assessment")}
      activePath={session.mode === "practice" ? "/practice" : "/exam"}
      action={
        <div className="btn-row">
          <Link className="btn secondary sm" href="/progress">Progress</Link>
          <Link className="btn sm" href={session.mode === "practice" ? "/practice" : "/exam"}>
            {session.mode === "practice" ? "New practice" : "More assessments"}
          </Link>
        </div>
      }
    >
      {!released ? (
        <div className="banner info">
          <span className="ico">⏳</span>
          Your score is recorded. Explanations and the answer key are released when the assessment window closes.
        </div>
      ) : null}

      <section className="card pad-lg">
        <div className="row wrap" style={{ gap: 24, alignItems: "center" }}>
          <div className="score-ring" style={{ ["--pct" as any]: percent }}>
            <span className="val">{percent}<small>%</small></span>
          </div>
          <div className="stack" style={{ gap: 6, flex: 1, minWidth: 220 }}>
            <div className="row wrap" style={{ gap: 8 }}>
              <span className={`badge ${scoreTone(percent)}`}>{session.score_raw} of {session.score_max} marks</span>
              <span className="badge neutral" style={{ textTransform: "capitalize" }}>{session.status}</span>
              {durationMin ? <span className="badge neutral">{durationMin} min used</span> : null}
            </div>
            <h2 style={{ fontSize: "1.5rem" }}>
              {percent >= 70 ? "Strong performance" : percent >= 45 ? "Solid, with clear gaps" : "This is your starting point"}
            </h2>
            <p className="small muted">
              Scored with policy v{snapshot.policy.version}. {correct} correct · {incorrect} incorrect · {unanswered} unanswered.
              Submitted {relativeTime(session.submitted_at)}.
            </p>
          </div>
          <div className="stack-sm" style={{ minWidth: 200 }}>
            <div className="row-between small"><span className="muted">Correct</span><span className="strong">{correct}</span></div>
            <div className="row-between small"><span className="muted">Incorrect</span><span className="strong">{incorrect}</span></div>
            <div className="row-between small"><span className="muted">Unanswered</span><span className="strong">{unanswered}</span></div>
            <div className="row-between small"><span className="muted">Questions</span><span className="strong">{questions.length}</span></div>
          </div>
        </div>
      </section>

      <div className="split" style={{ gridTemplateColumns: "1.15fr 1fr" }}>
        <section className="card">
          <div className="card-head">
            <div>
              <div className="card-title">Topic breakdown</div>
              <div className="card-sub">Weakest topics first — use these for your next session</div>
            </div>
          </div>
          {topics.length === 0 ? (
            <div className="empty" style={{ padding: 22 }}><span className="small">No topic data for this attempt.</span></div>
          ) : (
            <div className="stack-sm">
              {topics.map((topic) => {
                const pct = Math.round((topic.right / topic.total) * 100);
                return (
                  <div key={topic.name} className="stack-sm">
                    <div className="row-between small">
                      <span className="strong">{topic.name}</span>
                      <span className="tiny muted">{topic.right}/{topic.total} · {pct}%</span>
                    </div>
                    <div className="progress">
                      <span style={{ width: `${Math.max(pct, 3)}%`, background: pct >= 70 ? "var(--green-600)" : pct >= 45 ? "var(--amber-500)" : "var(--rose-600)" }} />
                    </div>
                  </div>
                );
              })}
            </div>
          )}
          <div className="divider" style={{ margin: "16px 0" }} />
          <Link className="btn secondary sm block" href={`/practice?subject=${snapshot.questions[0]?.subjectId ?? ""}`}>
            Practise your weakest topic
          </Link>
        </section>

        <section className="card">
          <div className="card-head">
            <div className="card-title">What to do next</div>
          </div>
          <div className="stack-sm">
            {topics.slice(0, 2).map((topic) => (
              <div key={topic.name} className="banner info small">
                <span className="ico">→</span>
                Revise <strong>{topic.name}</strong> — you scored {Math.round((topic.right / topic.total) * 100)}% here.
              </div>
            ))}
            <div className="banner small">
              <span className="ico">✦</span>
              You earned learning points for completing this attempt. See your balance in Rewards.
            </div>
            <Link className="btn sm block" href="/exam">Sit a full timed mock</Link>
            <Link className="btn secondary sm block" href="/progress">Track your readiness</Link>
          </div>
        </section>
      </div>

      {released ? (
        <section className="card">
          <div className="card-head">
            <div>
              <div className="card-title">Answer review</div>
              <div className="card-sub">Explanations follow the published question version used in this attempt</div>
            </div>
          </div>
          <div>
            {questions.map((q, i) => {
              const detail = review.find((r) => r.qvId === q.qvId);
              if (!detail) return null;
              const isCorrect = q.selectedOptionId === detail.answerOptionId;
              return (
                <article key={q.qvId} className="review-item">
                  <div className="row-between wrap" style={{ gap: 8 }}>
                    <span className="eyebrow">Question {i + 1}{detail.topicName ? ` · ${detail.topicName}` : ""}</span>
                    <span className={`badge ${q.selectedOptionId ? (isCorrect ? "" : "rose") : "amber"}`}>
                      {q.selectedOptionId ? (isCorrect ? "Correct" : "Incorrect") : "Not answered"}
                    </span>
                  </div>
                  <div className="question-stem" style={{ fontSize: 15, margin: "8px 0 12px" }} dangerouslySetInnerHTML={{ __html: q.stem }} />
                  <div className="options">
                    {q.options.map((option) => {
                      const isAnswer = option.id === detail.answerOptionId;
                      const isChosen = option.id === q.selectedOptionId;
                      const cls = isAnswer ? "correct" : isChosen ? "wrong" : "";
                      return (
                        <div key={option.id} className={`option ${cls}`} aria-disabled>
                          <span className="key" aria-hidden>{isAnswer ? "✓" : isChosen ? "✕" : ""}</span>
                          <span>{option.label}</span>
                          {isChosen ? <span className="badge neutral" style={{ marginLeft: "auto" }}>Your answer</span> : null}
                        </div>
                      );
                    })}
                  </div>
                  <div className="review-answer">
                    <div className="row" style={{ gap: 6, marginBottom: 4 }}>
                      <span className={isCorrect ? "ok" : "no"}>{isCorrect ? "Correct" : "Explanation"}</span>
                    </div>
                    {detail.explanation}
                  </div>
                  <div style={{ marginTop: 8 }}>
                    <ReportQuestion questionVersionId={q.qvId} />
                  </div>
                </article>
              );
            })}
          </div>
        </section>
      ) : null}
    </Shell>
  );
}
