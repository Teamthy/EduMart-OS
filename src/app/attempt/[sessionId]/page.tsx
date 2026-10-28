import { notFound, redirect } from "next/navigation";
import { requirePageUser } from "@/lib/server/guards";
import { getSessionRow, sessionView } from "@/lib/server/assessment";
import { definitionById } from "@/lib/server/content";
import { ExamRunner } from "./ExamRunner";

export const dynamic = "force-dynamic";

export default async function AttemptPage({ params }: { params: Promise<{ sessionId: string }> }) {
  const user = await requirePageUser();
  const { sessionId } = await params;
  const session = getSessionRow(sessionId);
  if (!session || session.user_id !== user.id) notFound();
  if (session.status !== "in_progress") redirect(`/result/${sessionId}`);

  const view = sessionView(session, { includeAnswers: false });
  const definition = session.definition_id ? definitionById(session.definition_id) : null;

  return (
    <ExamRunner
      sessionId={session.id}
      title={definition?.title ?? (session.mode === "practice" ? "Practice session" : "Mock assessment")}
      mode={session.mode}
      deadlineAt={session.deadline_at}
      serverTime={view.server_time}
      startedAt={session.started_at}
      questions={view.questions}
    />
  );
}
