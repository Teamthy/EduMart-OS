import Link from "next/link";
import { notFound } from "next/navigation";
import { Shell } from "@/components/Shell";
import { requirePageUser } from "@/lib/server/guards";
import { definitionById } from "@/lib/server/content";
import { q } from "@/lib/server/db";
import { classForUser } from "@/lib/server/classes";
import { StartAssessment } from "./StartAssessment";

export const dynamic = "force-dynamic";

export default async function AssessmentIntroPage({ params }: { params: Promise<{ definitionId: string }> }) {
  const user = await requirePageUser("/exam");
  const { definitionId } = await params;
  const definition = definitionById(definitionId);
  if (!definition || definition.status !== "published") notFound();

  if (definition.class_id && !classForUser(user.id, definition.class_id)) notFound();

  const used = q.get<any>(
    "SELECT COUNT(*) AS n FROM assessment_sessions WHERE user_id = ? AND definition_id = ?",
    user.id,
    definitionId,
  );
  const attemptsUsed = Number(used?.n ?? 0);
  const remaining = Math.max(Number(definition.attempt_limit) - attemptsUsed, 0);
  const now = Date.now();
  const notOpenYet = definition.availability_start && now < new Date(definition.availability_start).getTime();
  const closed = definition.availability_end && now > new Date(definition.availability_end).getTime();

  const config = JSON.parse(definition.config_json || "{}");

  return (
    <Shell
      title={definition.title}
      subtitle={definition.class_name ? `Class assessment · ${definition.class_name}` : "Public mock assessment"}
      activePath="/exam"
      action={<Link className="btn secondary sm" href="/exam">All assessments</Link>}
    >
      <div className="split" style={{ gridTemplateColumns: "1.5fr 1fr", alignItems: "start" }}>
        <div className="stack">
          <section className="card pad-lg stack">
            <div className="row wrap" style={{ gap: 8 }}>
              <span className="badge">{Math.round(definition.duration_seconds / 60)} minutes</span>
              <span className="badge neutral">{definition.question_count} questions</span>
              <span className="badge neutral">{definition.attempt_limit} attempt{Number(definition.attempt_limit) === 1 ? "" : "s"}</span>
              {definition.class_id ? <span className="badge indigo">Class only</span> : <span className="badge amber">Public mock</span>}
            </div>
            <p className="lead">
              This is a timed, server-scored assessment. Your deadline is fixed when you start and is enforced by
              the server — refreshing, closing the tab or changing your device clock will not extend it.
            </p>
            <div className="stack-sm">
              <h3 style={{ fontSize: "1rem" }}>Before you begin</h3>
              <ul className="small muted" style={{ margin: 0, paddingLeft: 18, lineHeight: 1.8 }}>
                <li>Answers save automatically; each one is confirmed by the server.</li>
                <li>If your connection drops, pending answers are queued and retried — you will see their state.</li>
                <li>You can move between questions freely{config.navigation === "linear" ? " only forward" : ""}.</li>
                <li>The attempt is submitted automatically when the timer reaches zero.</li>
                <li>Explanations and the answer key are shown after the result is released.</li>
              </ul>
            </div>
          </section>

          <section className="card">
            <div className="card-title">Scoring</div>
            <div className="tiny muted" style={{ marginTop: 4 }}>
              Policy v{JSON.parse(definition.scoring_policy_json).version} · 1 mark per correct answer · no negative marking ·
              unscored blanks are recorded as unanswered.
            </div>
          </section>
        </div>

        <aside className="stack" style={{ position: "sticky", top: 84 }}>
          <section className="card pad-lg stack">
            <div className="row-between">
              <span className="small muted">Attempts used</span>
              <span className="strong">{attemptsUsed} of {definition.attempt_limit}</span>
            </div>
            <div className="row-between">
              <span className="small muted">Availability</span>
              <span className="small strong">
                {definition.availability_start ? new Date(definition.availability_start).toLocaleDateString("en-NG") : "Open"}
                {" – "}
                {definition.availability_end ? new Date(definition.availability_end).toLocaleDateString("en-NG") : "no end"}
              </span>
            </div>
            {notOpenYet ? <div className="banner warn small"><span className="ico">⏳</span>This assessment is not open yet.</div> : null}
            {closed ? <div className="banner error small"><span className="ico">!</span>This assessment window has closed.</div> : null}
            {remaining === 0 ? <div className="banner warn small"><span className="ico">!</span>You have used all attempts.</div> : null}
            <StartAssessment
              definitionId={definition.id}
              disabled={Boolean(notOpenYet) || Boolean(closed) || remaining === 0}
              remaining={remaining}
            />
            <p className="tiny muted">
              By starting you confirm this is your own work. Assessment activity is logged for integrity review.
            </p>
          </section>
        </aside>
      </div>
    </Shell>
  );
}
