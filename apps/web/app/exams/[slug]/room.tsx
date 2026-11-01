"use client";

import { useCallback, useEffect, useMemo, useState } from "react";
import { AlarmClock, CheckCircle2, Play } from "lucide-react";

import { Alert } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Progress } from "@/components/ui/progress";
import {
  LiveApiError,
  examResults,
  saveAnswer,
  startExam,
  submitAttempt,
  type AttemptView,
  type Exam,
  type ExamCandidate,
} from "@/lib/live-api";

interface Props {
  exam: Exam;
}

/**
 * The exam page for a candidate: start, answer, submit.
 *
 * Everything that decides the outcome lives on the server — the question set, the permutation, the deadline and
 * the score. This component's only jobs are to send the option somebody picked, to show the time the *server*
 * says is left, and to submit when that time runs out rather than waiting for a timer in a tab that may be
 * asleep. Auto-submitting is the same request a person would make; the server would close the attempt at the
 * deadline anyway, and doing it here means the score is shown instead of "expired" on the next visit.
 */
export function ExamRoom({ exam }: Props) {
  const [attempt, setAttempt] = useState<AttemptView | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [remaining, setRemaining] = useState<number>(exam.myAttempt?.remainingSeconds ?? 0);
  const [submitted, setSubmitted] = useState<{ raw: number; max: number; percent: number } | null>(
    exam.myAttempt?.scorePercent !== null && exam.myAttempt?.scorePercent !== undefined
      ? { raw: 0, max: 0, percent: exam.myAttempt.scorePercent }
      : null,
  );

  const inProgress = attempt?.status === "in_progress";

  const start = useCallback(async () => {
    setBusy(true);
    setError(null);
    try {
      const view = await startExam(exam.slug);
      setAttempt(view);
      setRemaining(view.remaining_seconds);
    } catch (caught) {
      setError(caught instanceof LiveApiError ? caught.message : "Could not start the exam.");
    } finally {
      setBusy(false);
    }
  }, [exam.slug]);

  const submit = useCallback(async () => {
    if (!attempt) return;
    setBusy(true);
    setError(null);
    try {
      await submitAttempt(attempt.session_id);
      // The submitted view is what carries the score; asking for it again keeps one source of truth rather than
      // guessing the result from the answers this browser holds.
      const results = await reloadAttempt(attempt.session_id);
      setAttempt(results);
      if (results.score) setSubmitted(results.score);
    } catch (caught) {
      setError(caught instanceof LiveApiError ? caught.message : "Could not submit the exam.");
    } finally {
      setBusy(false);
    }
  }, [attempt]);

  useEffect(() => {
    if (!inProgress) return;
    const timer = window.setInterval(() => {
      setRemaining((current) => Math.max(0, current - 1));
    }, 1_000);
    return () => window.clearInterval(timer);
  }, [inProgress]);

  // The deadline is the server's. When it passes, the attempt is submitted — by this call, or by the server's
  // own expiry sweep if the tab is gone.
  useEffect(() => {
    if (!inProgress || remaining > 0) return;
    void submit();
  }, [inProgress, remaining, submit]);

  async function choose(qvId: string, optionId: string) {
    if (!attempt || attempt.status !== "in_progress") return;
    setAttempt((current) =>
      current
        ? {
            ...current,
            questions: current.questions.map((question) =>
              question.qvId === qvId ? { ...question, selectedOptionId: optionId } : question,
            ),
          }
        : current,
    );
    try {
      await saveAnswer(attempt.session_id, qvId, optionId);
    } catch (caught) {
      setError(caught instanceof LiveApiError ? caught.message : "That answer did not save.");
    }
  }

  const answered = useMemo(
    () => attempt?.questions.filter((question) => question.selectedOptionId).length ?? 0,
    [attempt],
  );

  return (
    <div className="space-y-4">
      <Card>
        <CardContent className="flex flex-wrap items-center justify-between gap-3 p-4">
          <div>
            <h1 className="text-xl">{exam.title}</h1>
            <p className="text-xs text-muted-foreground">
              {exam.questionCount} questions · {exam.durationMinutes} minutes ·{" "}
              {exam.shuffled ? "your paper is in your own order" : "one shared order"}
            </p>
          </div>
          <div className="flex items-center gap-2">
            <Badge variant={exam.open ? "success" : "outline"}>{exam.open ? "open" : "closed"}</Badge>
            {inProgress || submitted ? null : (
              <Button type="button" disabled={busy || !exam.open} onClick={() => void start()}>
                <Play aria-hidden="true" className="size-4" />
                {busy ? "Starting…" : "Start exam"}
              </Button>
            )}
          </div>
        </CardContent>
      </Card>

      {error ? <Alert tone="error" title="Something went wrong">{error}</Alert> : null}

      {submitted ? (
        <Card>
          <CardHeader>
            <CardTitle className="flex items-center gap-2">
              <CheckCircle2 aria-hidden="true" className="size-5 text-emerald-600" />
              Submitted
            </CardTitle>
          </CardHeader>
          <CardContent className="space-y-2">
            {submitted.max > 0 ? (
              <p className="text-sm">
                Score: {submitted.raw}/{submitted.max} ({submitted.percent}%)
              </p>
            ) : (
              <p className="text-sm">Your score is {submitted.percent}%.</p>
            )}
            <Progress value={submitted.percent} />
            <p className="text-xs text-muted-foreground">
              The server recorded when this attempt started and when it was submitted. Your result belongs to this
              sitting, not to a screenshot of it.
            </p>
          </CardContent>
        </Card>
      ) : null}

      {attempt && attempt.status === "in_progress" ? (
        <>
          <Card>
            <CardContent className="flex flex-wrap items-center justify-between gap-3 p-4">
              <span className="flex items-center gap-2 text-sm">
                <AlarmClock aria-hidden="true" className="size-4" />
                {Math.floor(remaining / 60)}m {remaining % 60}s left, by the server's clock
              </span>
              <span className="text-xs text-muted-foreground">
                {answered}/{attempt.questions.length} answered
              </span>
              <Button type="button" size="sm" disabled={busy} onClick={() => void submit()}>
                Submit exam
              </Button>
            </CardContent>
          </Card>

          <ol className="space-y-3">
            {attempt.questions.map((question, index) => (
              <li key={question.qvId}>
                <Card>
                  <CardContent className="space-y-3 p-4">
                    <p className="font-medium">
                      {index + 1}. {question.stem}
                    </p>
                    <div className="grid gap-2 sm:grid-cols-2">
                      {question.options.map((option) => {
                        const chosen = question.selectedOptionId === option.id;
                        return (
                          <button
                            key={option.id}
                            type="button"
                            aria-pressed={chosen}
                            onClick={() => void choose(question.qvId, option.id)}
                            className={`rounded-lg border p-2 text-left text-sm transition-colors ${
                              chosen ? "border-primary bg-secondary" : "border-border hover:bg-secondary/50"
                            }`}
                          >
                            {option.label}
                          </button>
                        );
                      })}
                    </div>
                  </CardContent>
                </Card>
              </li>
            ))}
          </ol>
        </>
      ) : null}

      {!attempt && !submitted ? (
        <Card>
          <CardHeader>
            <CardTitle>Before you start</CardTitle>
          </CardHeader>
          <CardContent className="space-y-2 text-sm text-muted-foreground">
            <p>
              This sitting opens at {new Date(exam.startsAt).toLocaleString()} and closes at{" "}
              {new Date(exam.endsAt).toLocaleString()}. You get {exam.durationMinutes} minutes, or whatever is left
              of the window if that is shorter.
            </p>
            <p>You have {exam.attemptLimit} attempt. Answers save as you choose them; the exam submits itself when time runs out.</p>
            {exam.myAttempt && exam.myAttempt.status !== "in_progress" ? (
              <p className="text-foreground">You have already sat this exam, and the attempt limit is used up.</p>
            ) : null}
          </CardContent>
        </Card>
      ) : null}
    </div>
  );
}

/** The host's view: who started, when they submitted, and what the server scored. */
export function ExamResults({ exam }: Props) {
  const [candidates, setCandidates] = useState<ExamCandidate[] | null>(null);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    void (async () => {
      try {
        const { candidates: rows } = await examResults(exam.id);
        setCandidates(rows);
      } catch (caught) {
        setError(caught instanceof LiveApiError ? caught.message : "Could not load the sitting.");
      }
    })();
  }, [exam.id]);

  return (
    <Card>
      <CardHeader>
        <CardTitle>The sitting</CardTitle>
      </CardHeader>
      <CardContent className="space-y-2">
        {error ? <Alert tone="error">{error}</Alert> : null}
        {candidates === null && !error ? <p className="text-sm text-muted-foreground">Loading…</p> : null}
        {candidates && candidates.length === 0 ? (
          <p className="text-sm text-muted-foreground">Nobody has started yet.</p>
        ) : null}
        {candidates && candidates.length > 0 ? (
          <ul className="space-y-1 text-sm">
            {candidates.map((candidate) => (
              <li key={candidate.userId} className="flex flex-wrap items-center justify-between gap-2">
                <span className="truncate">{candidate.displayName}</span>
                <span className="text-xs text-muted-foreground">
                  {candidate.status}
                  {candidate.scoreMax ? ` · ${candidate.scoreRaw}/${candidate.scoreMax}` : ""}
                  {candidate.submittedAt
                    ? ` · submitted ${new Date(candidate.submittedAt).toLocaleTimeString()}`
                    : candidate.remainingSeconds > 0
                      ? ` · ${Math.ceil(candidate.remainingSeconds / 60)} min left`
                      : ""}
                  {candidate.integrity && candidate.integrity.signals > 0 ? (
                    // A clean score with six lost-tab events beside it is a different result from a clean score
                    // with none, and the host is the person who should decide what that means.
                    <span className="ml-2 rounded bg-amber-100 px-1.5 py-0.5 text-amber-900">
                      {candidate.integrity.signals} signal{candidate.integrity.signals === 1 ? "" : "s"}
                      {candidate.integrity.focusLost > 0 ? ` · left ${candidate.integrity.focusLost}x` : ""}
                    </span>
                  ) : null}
                </span>
              </li>
            ))}
          </ul>
        ) : null}
      </CardContent>
    </Card>
  );
}

/**
 * Re-reads one attempt through the assessment endpoint, which is the one place a score comes from.
 *
 * It is a small local function rather than a method on the exam client because the exam page needs exactly this
 * shape: the shared client's assessment methods serve the attempt screen, and duplicating them would give two
 * places to keep the envelope right.
 */
async function reloadAttempt(sessionId: string): Promise<AttemptView> {
  const response = await fetch(`/v1/assessment-sessions/${encodeURIComponent(sessionId)}`, {
    headers: { Accept: "application/json" },
    credentials: "include",
    cache: "no-store",
  });
  const payload = (await response.json()) as {
    session?: AttemptView;
    error?: { message?: string };
  };
  if (!response.ok || !payload.session) {
    throw new LiveApiError({
      code: "ERROR",
      message: payload.error?.message ?? "Could not read the attempt.",
      status: response.status,
    });
  }
  return payload.session;
}
