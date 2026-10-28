import type { Metadata } from "next";
import Link from "next/link";
import { notFound, redirect } from "next/navigation";
import { CheckCircle2, EyeOff, RotateCcw, XCircle } from "lucide-react";

import { formatDateTimeSafe, formatPercent } from "@edumart/shared";

import { Alert } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Progress } from "@/components/ui/progress";
import { ServerApiError, serverFetch } from "@/lib/server-api";
import { requireUser } from "@/lib/session";
import type { AssessmentSessionView } from "@edumart/shared";

export const metadata: Metadata = { title: "Result" };
export const dynamic = "force-dynamic";

export default async function ResultsPage({ params }: { params: Promise<{ id: string }> }) {
  await requireUser();
  const { id } = await params;

  let session: AssessmentSessionView;
  try {
    ({ session } = await serverFetch<{ session: AssessmentSessionView }>(`/v1/assessment-sessions/${id}`));
  } catch (error) {
    if (error instanceof ServerApiError && error.isNotFound) notFound();
    if (error instanceof ServerApiError && error.isForbidden) redirect("/permission-denied");
    return (
      <Alert tone="error" title="We couldn't load this result">
        {error instanceof ServerApiError ? error.message : "Please try again."}
      </Alert>
    );
  }

  if (session.status === "in_progress") redirect(`/attempt/${session.session_id}`);

  const answers = new Map((session.review ?? []).map((item) => [item.qvId, item]));
  const statusLabel =
    session.status === "expired" ? "Closed at the deadline" : session.status === "cancelled" ? "Cancelled" : "Submitted";

  return (
    <div className="space-y-6">
      <nav aria-label="Breadcrumb" className="text-sm text-muted-foreground">
        <Link href="/progress" className="hover:underline">
          Progress
        </Link>{" "}
        / <span aria-current="page">Result</span>
      </nav>

      <header className="space-y-1">
        <h1 className="text-2xl">{session.mode === "practice" ? "Practice result" : "Assessment result"}</h1>
        <p className="text-sm text-muted-foreground">
          {statusLabel}
          {session.submitted_at ? ` · ${formatDateTimeSafe(session.submitted_at)}` : ""} · Attempt #{session.attempt_no}
        </p>
      </header>

      {session.score ? (
        <Card>
          <CardContent className="space-y-3 p-5">
            <div className="flex flex-wrap items-end justify-between gap-3">
              <div>
                <p className="text-xs uppercase tracking-wide text-muted-foreground">Score</p>
                <p className="font-serif text-4xl">{formatPercent(session.score.percent)}</p>
              </div>
              <p className="text-sm text-muted-foreground">
                {session.score.raw} of {session.score.max} marks · {session.question_count} questions
              </p>
            </div>
            <Progress
              value={session.score.percent}
              aria-label={`Score ${session.score.percent} percent`}
              className="h-3"
            />
            <p className="text-xs text-muted-foreground">
              Scored on the server with a versioned policy, so this result can be reproduced exactly.
            </p>
          </CardContent>
        </Card>
      ) : (
        <Alert tone="warning" title="No score recorded">
          This attempt closed before any answers were saved.
        </Alert>
      )}

      {!session.result_released ? (
        <Alert tone="info" title="Answers not released yet">
          This assessment releases answers later. Your score is final; the per-question review will
          appear here automatically once released.
        </Alert>
      ) : null}

      {session.result_released && session.review && session.review.length > 0 ? (
        <section aria-labelledby="review" className="space-y-3">
          <h2 id="review" className="font-serif text-lg">
            Review
          </h2>
          <ol className="space-y-3">
            {session.questions.map((question) => {
              const key = answers.get(question.qvId);
              const correct = key ? question.selectedOptionId === key.answerOptionId : null;
              return (
                <li key={question.qvId}>
                  <Card>
                    <CardHeader className="flex-row items-start justify-between gap-3 pb-2">
                      <CardTitle className="text-base font-semibold">
                        Q{question.position}. {question.stem}
                      </CardTitle>
                      {correct === true ? (
                        <Badge variant="success">
                          <CheckCircle2 aria-hidden="true" className="size-3" /> Correct
                        </Badge>
                      ) : correct === false ? (
                        <Badge variant="destructive">
                          <XCircle aria-hidden="true" className="size-3" /> Incorrect
                        </Badge>
                      ) : (
                        <Badge variant="outline">Unanswered</Badge>
                      )}
                    </CardHeader>
                    <CardContent className="space-y-2">
                      <ul className="space-y-1 text-sm">
                        {question.options.map((option) => {
                          const isAnswer = key?.answerOptionId === option.id;
                          const isChoice = question.selectedOptionId === option.id;
                          return (
                            <li
                              key={option.id}
                              className={
                                isAnswer
                                  ? "rounded-md border border-[#bfe3cd] bg-[#eaf7ef] px-3 py-2"
                                  : isChoice
                                    ? "rounded-md border border-[#efc4c4] bg-[#fdeeee] px-3 py-2"
                                    : "px-3 py-2 text-muted-foreground"
                              }
                            >
                              {option.label}
                              {isAnswer ? <span className="ml-2 text-xs font-semibold">Correct answer</span> : null}
                              {isChoice && !isAnswer ? <span className="ml-2 text-xs font-semibold">Your answer</span> : null}
                            </li>
                          );
                        })}
                      </ul>
                      {key?.explanation ? (
                        <p className="rounded-md bg-muted px-3 py-2 text-sm">
                          <span className="font-semibold">Why: </span>
                          {key.explanation}
                        </p>
                      ) : null}
                      {key?.topicName ? (
                        <p className="text-xs text-muted-foreground">Topic: {key.topicName}</p>
                      ) : null}
                    </CardContent>
                  </Card>
                </li>
              );
            })}
          </ol>
        </section>
      ) : session.result_released ? (
        <p className="flex items-center gap-2 text-sm text-muted-foreground">
          <EyeOff aria-hidden="true" className="size-4" />
          No review items were returned for this attempt.
        </p>
      ) : null}

      <div className="flex flex-wrap gap-2">
        <Button asChild>
          <Link href="/practice">
            <RotateCcw aria-hidden="true" /> Practise again
          </Link>
        </Button>
        <Button asChild variant="outline">
          <Link href="/progress">See your progress</Link>
        </Button>
      </div>
    </div>
  );
}
