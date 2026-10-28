"use client";

import { useRouter } from "next/navigation";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import {
  AlertCircle,
  Check,
  ChevronLeft,
  ChevronRight,
  Clock,
  CloudOff,
  Loader2,
  Send,
} from "lucide-react";

import { ApiError, formatCountdown, type AssessmentSessionView, type QuestionView } from "@edumart/shared";

import { Alert } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent } from "@/components/ui/card";
import { api } from "@/lib/browser-api";
import { cn } from "@/lib/utils";

type SaveStatus = "idle" | "pending" | "saved" | "failed";

interface AnswerState {
  optionId: string | null;
  version: number;
  status: SaveStatus;
  sequence: number;
  error?: string;
}

/**
 * The attempt runner.
 *
 * The client never owns the clock, the score or the answer key. It owns exactly three things:
 * which option the learner has chosen *right now* (optimistic), whether that choice has been
 * confirmed by the server, and a retry queue for writes that have not.
 *
 * A write is retried with the same `clientSequence`, which makes it idempotent server-side: a
 * response lost in transit is replayed, not duplicated, and never overwrites a newer answer from
 * another device.
 */
export function AttemptRunner({ session }: { session: AssessmentSessionView }) {
  const router = useRouter();

  const initialAnswers = useMemo(() => {
    const map: Record<string, AnswerState> = {};
    for (const question of session.questions) {
      map[question.qvId] = {
        optionId: question.selectedOptionId,
        version: question.answerVersion,
        status: "idle",
        sequence: 0,
      };
    }
    return map;
  }, [session.questions]);

  const [answers, setAnswers] = useState<Record<string, AnswerState>>(initialAnswers);
  const [currentIndex, setCurrentIndex] = useState(0);
  const [remaining, setRemaining] = useState(session.remaining_seconds);
  const [submitting, setSubmitting] = useState(false);
  const [submitError, setSubmitError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const [offline, setOffline] = useState(false);

  // Deadline maths uses the server's `remaining_seconds`, then elapses locally. The client clock is
  // never trusted for anything that affects scoring.
  const deadlineRef = useRef(Date.now() + session.remaining_seconds * 1000);
  const inFlight = useRef<Set<string>>(new Set());
  // A ref mirrors the latest answers so `save` can read the confirmed version without re-creating.
  const answersRef = useRef(answers);
  useEffect(() => {
    answersRef.current = answers;
  }, [answers]);

  useEffect(() => {
    const tick = () => {
      const seconds = Math.max(0, Math.round((deadlineRef.current - Date.now()) / 1000));
      setRemaining(seconds);
    };
    tick();
    const interval = setInterval(tick, 1000);
    return () => clearInterval(interval);
  }, []);

  useEffect(() => {
    const update = () => setOffline(!navigator.onLine);
    update();
    window.addEventListener("online", update);
    window.addEventListener("offline", update);
    return () => {
      window.removeEventListener("online", update);
      window.removeEventListener("offline", update);
    };
  }, []);

  const save = useCallback(
    async (questionId: string, optionId: string | null) => {
      if (inFlight.current.has(questionId)) return;
      inFlight.current.add(questionId);

      let sequence = 0;
      setAnswers((current) => {
        const previous = current[questionId];
        sequence = (previous?.sequence ?? 0) + 1;
        return {
          ...current,
          [questionId]: {
            optionId,
            version: previous?.version ?? 0,
            status: "pending",
            sequence,
          },
        };
      });

      try {
        // Reading the state inside the updater is not reliable for the request payload, so the
        // expected version is taken from the last confirmed value held in this closure's snapshot.
        const expectedVersion = answersRef.current[questionId]?.version ?? 0;
        const result = await api().saveAnswer(session.session_id, questionId, {
          selectedOptionId: optionId,
          expectedVersion,
          clientSequence: sequence,
        });

        setAnswers((current) => ({
          ...current,
          [questionId]: {
            optionId,
            version: result.version,
            status: "saved",
            sequence,
          },
        }));
      } catch (error) {
        if (error instanceof ApiError && error.code === "CONFLICT") {
          // Another device wrote first. Surface it rather than silently overwriting.
          setNotice("This answer changed on another device. Reloading the latest saved value.");
          try {
            const fresh = await api().session(session.session_id);
            setAnswers((current) => ({
              ...current,
              [questionId]: {
                optionId: fresh.session.questions.find((q) => q.qvId === questionId)?.selectedOptionId ?? null,
                version: fresh.session.questions.find((q) => q.qvId === questionId)?.answerVersion ?? 0,
                status: "saved",
                sequence,
              },
            }));
          } catch {
            setAnswers((current) => ({
              ...current,
              [questionId]: { ...current[questionId]!, status: "failed", error: "Reload to see the latest value." },
            }));
          }
        } else {
          const message =
            error instanceof ApiError && error.code === "SESSION_EXPIRED"
              ? "The deadline passed — your saved answers were submitted."
              : error instanceof ApiError && error.code === "NETWORK_ERROR"
                ? "No connection. We'll retry when you're back online."
                : error instanceof ApiError
                  ? error.message
                  : "That answer didn't save.";
          setAnswers((current) => ({
            ...current,
            [questionId]: { ...current[questionId]!, status: "failed", error: message },
          }));
        }
      } finally {
        inFlight.current.delete(questionId);
      }
    },
    [session.session_id],
  );

  // Retry failed writes when connectivity returns.
  useEffect(() => {
    if (!offline && Object.values(answers).some((answer) => answer.status === "failed")) {
      for (const question of session.questions) {
        const answer = answers[question.qvId];
        if (answer?.status === "failed" && answer.optionId) {
          void save(question.qvId, answer.optionId);
        }
      }
    }
  }, [offline, answers, save, session.questions]);

  const questions = session.questions;
  const current = questions[currentIndex];
  const answeredCount = Object.values(answers).filter((answer) => answer.optionId !== null).length;
  const pendingCount = Object.values(answers).filter((answer) => answer.status === "pending").length;
  const failedCount = Object.values(answers).filter((answer) => answer.status === "failed").length;

  const choose = (question: QuestionView, optionId: string) => {
    setNotice(null);
    setAnswers((currentAnswers) => ({
      ...currentAnswers,
      [question.qvId]: { ...currentAnswers[question.qvId]!, optionId, status: "pending" },
    }));
    void save(question.qvId, optionId);
  };

  const submit = useCallback(async () => {
    setSubmitting(true);
    setSubmitError(null);
    try {
      await api().submitSession(session.session_id);
      router.replace(`/results/${session.session_id}`);
      router.refresh();
    } catch (error) {
      setSubmitError(error instanceof ApiError ? error.message : "We couldn't submit. Please try again.");
      setSubmitting(false);
    }
  }, [router, session.session_id]);

  // When the server deadline has passed the attempt must close even if the learner walked away.
  useEffect(() => {
    if (remaining === 0 && !submitting) {
      void submit();
    }
  }, [remaining, submitting, submit]);

  if (!current) {
    return <Alert tone="error" title="This attempt has no questions">Please start a new practice set.</Alert>;
  }

  const answer = answers[current.qvId]!;
  const isLast = currentIndex === questions.length - 1;
  const lowTime = remaining <= 60;

  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <div>
          <p className="text-xs uppercase tracking-wide text-muted-foreground">
            Question {currentIndex + 1} of {questions.length}
          </p>
          <h1 className="text-xl">{session.mode === "practice" ? "Practice set" : "Timed assessment"}</h1>
        </div>
        <div
          className={cn(
            "flex items-center gap-2 rounded-lg border px-3 py-2 font-mono text-sm",
            lowTime ? "border-destructive bg-[#fdeeee] text-destructive" : "border-border bg-card",
          )}
          role="timer"
          aria-live={lowTime ? "assertive" : "off"}
        >
          <Clock aria-hidden="true" className="size-4" />
          <span aria-label={`Time remaining ${formatCountdown(remaining)}`}>{formatCountdown(remaining)}</span>
        </div>
      </div>

      {notice ? <Alert tone="warning">{notice}</Alert> : null}
      {offline ? (
        <Alert tone="offline" title="Offline">
          New answers stay on this device and retry automatically. Anything already marked Saved is on the server.
        </Alert>
      ) : null}

      <div className="grid gap-4 lg:grid-cols-[1fr_240px]">
        <Card>
          <CardContent className="space-y-5 p-5">
            <fieldset>
              <legend className="sr-only">Question {currentIndex + 1}</legend>
              <p className="mb-4 text-base leading-relaxed">{current.stem}</p>

              <div className="space-y-2">
                {current.options.map((option) => {
                  const selected = answer.optionId === option.id;
                  return (
                    <label
                      key={option.id}
                      className={cn(
                        "flex min-h-12 cursor-pointer items-center gap-3 rounded-lg border p-3 text-sm transition-colors",
                        selected ? "border-primary bg-secondary" : "border-border hover:border-input",
                      )}
                    >
                      <input
                        type="radio"
                        name={`question-${current.qvId}`}
                        value={option.id}
                        checked={selected}
                        onChange={() => choose(current, option.id)}
                        className="size-4 accent-[#0d5c46]"
                      />
                      <span>{option.label}</span>
                    </label>
                  );
                })}
              </div>
            </fieldset>

            <div className="flex items-center justify-between gap-3 pt-2">
              <Button
                type="button"
                variant="outline"
                onClick={() => setCurrentIndex((index) => Math.max(0, index - 1))}
                disabled={currentIndex === 0}
              >
                <ChevronLeft aria-hidden="true" />
                Previous
              </Button>

              {isLast ? (
                <Button type="button" onClick={() => void submit()} disabled={submitting}>
                  {submitting ? <Loader2 aria-hidden="true" className="animate-spin" /> : <Send aria-hidden="true" />}
                  {submitting ? "Submitting…" : "Finish and submit"}
                </Button>
              ) : (
                <Button type="button" onClick={() => setCurrentIndex((index) => Math.min(questions.length - 1, index + 1))}>
                  Next
                  <ChevronRight aria-hidden="true" />
                </Button>
              )}
            </div>
          </CardContent>
        </Card>

        <Card className="h-fit">
          <CardContent className="space-y-3 p-4">
            <div className="flex items-center justify-between text-sm">
              <span className="font-semibold">Progress</span>
              <span className="text-muted-foreground">
                {answeredCount}/{questions.length}
              </span>
            </div>

            <div className="grid grid-cols-5 gap-1.5" role="list" aria-label="Question navigation">
              {questions.map((question, index) => {
                const state = answers[question.qvId];
                const answered = state?.optionId !== null && state?.optionId !== undefined;
                const status = state?.status ?? "idle";
                return (
                  <button
                    key={question.qvId}
                    type="button"
                    role="listitem"
                    onClick={() => setCurrentIndex(index)}
                    aria-current={index === currentIndex ? "true" : undefined}
                    aria-label={`Question ${index + 1}${answered ? ", answered" : ", not answered"}${
                      status === "failed" ? ", not saved" : status === "pending" ? ", saving" : ""
                    }`}
                    className={cn(
                      "grid size-9 place-items-center rounded-md border text-xs font-semibold",
                      index === currentIndex ? "border-primary ring-2 ring-primary/30" : "border-border",
                      answered ? "bg-primary text-primary-foreground" : "bg-card text-muted-foreground",
                      status === "failed" && "border-destructive text-destructive",
                    )}
                  >
                    {status === "pending" ? (
                      <Loader2 aria-hidden="true" className="size-3 animate-spin" />
                    ) : status === "failed" ? (
                      <AlertCircle aria-hidden="true" className="size-3" />
                    ) : (
                      index + 1
                    )}
                  </button>
                );
              })}
            </div>

            {/* Save state is announced politely: it is informative, not urgent. */}
            <div aria-live="polite" className="space-y-2 text-xs">
              {failedCount > 0 ? (
                <p className="flex items-center gap-1.5 font-semibold text-destructive">
                  <CloudOff aria-hidden="true" className="size-3.5" />
                  {failedCount} answer{failedCount === 1 ? "" : "s"} not saved — tap the question to retry.
                </p>
              ) : pendingCount > 0 ? (
                <p className="flex items-center gap-1.5 text-muted-foreground">
                  <Loader2 aria-hidden="true" className="size-3.5 animate-spin" />
                  Saving {pendingCount} answer{pendingCount === 1 ? "" : "s"}…
                </p>
              ) : answeredCount > 0 ? (
                <p className="flex items-center gap-1.5 font-semibold text-success">
                  <Check aria-hidden="true" className="size-3.5" />
                  All answers saved on the server
                </p>
              ) : (
                <p className="text-muted-foreground">Answers save as soon as you choose them.</p>
              )}
              <p className="text-muted-foreground">
                The server owns the deadline. Submitting with unanswered questions is allowed — you
                are scored only on what you answered.
              </p>
            </div>

            {submitError ? <Alert tone="error">{submitError}</Alert> : null}

            <Button className="w-full" onClick={() => void submit()} disabled={submitting || pendingCount > 0} variant="secondary">
              {submitting ? <Loader2 aria-hidden="true" className="animate-spin" /> : null}
              Submit attempt
            </Button>
            {pendingCount > 0 ? (
              <p className="text-center text-xs text-muted-foreground">Waiting for saves to finish…</p>
            ) : null}
          </CardContent>
        </Card>
      </div>

      {failedCount > 0 ? <Badge variant="destructive">{failedCount} unsaved</Badge> : null}
    </div>
  );
}
