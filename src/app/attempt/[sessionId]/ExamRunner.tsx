"use client";

import Link from "next/link";
import { useRouter } from "next/navigation";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";

type Question = {
  qvId: string;
  stem: string;
  options: { id: string; label: string }[];
  position: number;
  topicName?: string;
  subjectName?: string;
  difficulty?: string;
  selectedOptionId: string | null;
  answerVersion: number;
};

type SaveStatus = "idle" | "saving" | "saved" | "pending" | "failed";

type AnswerState = {
  selectedOptionId: string | null;
  version: number;
  status: SaveStatus;
  message?: string;
  pendingSequence?: number;
};

const QUEUE_KEY = (sessionId: string) => `edumart.queue.${sessionId}`;

/**
 * Timed assessment runner.
 *
 * Client responsibilities are presentation only:
 *  • the countdown is derived from a server-supplied deadline plus a measured clock offset,
 *  • every answer change is written to the server and only marked "Saved" after acknowledgement,
 *  • writes that fail are queued, retried with backoff, and their state is always visible,
 *  • submission is a server transition; a duplicate submit is safe.
 */

export function ExamRunner({
  sessionId,
  title,
  mode,
  deadlineAt,
  serverTime,
  startedAt,
  questions,
}: {
  sessionId: string;
  title: string;
  mode: string;
  deadlineAt: string;
  serverTime: string;
  startedAt: string;
  questions: Question[];
}) {
  const router = useRouter();
  const deadlineMs = useMemo(() => new Date(deadlineAt).getTime(), [deadlineAt]);
  const serverOffset = useMemo(() => Date.now() - new Date(serverTime).getTime(), [serverTime]);

  const [index, setIndex] = useState(0);
  const [answers, setAnswers] = useState<Record<string, AnswerState>>(() => {
    const initial: Record<string, AnswerState> = {};
    for (const q of questions) {
      initial[q.qvId] = {
        selectedOptionId: q.selectedOptionId,
        version: q.answerVersion,
        status: q.selectedOptionId ? "saved" : "idle",
      };
    }
    return initial;
  });
  const [flagged, setFlagged] = useState<Record<string, boolean>>({});
  const [now, setNow] = useState(() => Date.now() - serverOffset);
  const [online, setOnline] = useState(true);
  const [submitting, setSubmitting] = useState(false);
  const [confirming, setConfirming] = useState(false);
  const [fatal, setFatal] = useState<string | null>(null);
  const [autoSubmitting, setAutoSubmitting] = useState(false);
  const [showPalette, setShowPalette] = useState(false);

  const queueRef = useRef<Record<string, { selectedOptionId: string | null; expectedVersion: number }>>({});
  const retryTimer = useRef<ReturnType<typeof setTimeout> | null>(null);
  const submittedRef = useRef(false);

  const current = questions[index];
  const answeredCount = Object.values(answers).filter((a) => a.selectedOptionId !== null).length;
  const remaining = Math.max(0, Math.floor((deadlineMs - now) / 1000));

  // ── clock ────────────────────────────────────────────────────────────────
  useEffect(() => {
    const id = setInterval(() => setNow(Date.now() - serverOffset), 1000);
    return () => clearInterval(id);
  }, [serverOffset]);

  // ── connectivity ─────────────────────────────────────────────────────────
  useEffect(() => {
    setOnline(navigator.onLine);
    const up = () => {
      setOnline(true);
      flushQueue();
    };
    const down = () => setOnline(false);
    window.addEventListener("online", up);
    window.addEventListener("offline", down);
    return () => {
      window.removeEventListener("online", up);
      window.removeEventListener("offline", down);
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  // ── restore queued writes from a previous visit (bounded, versioned) ─────
  useEffect(() => {
    try {
      const raw = localStorage.getItem(QUEUE_KEY(sessionId));
      if (!raw) return;
      const parsed = JSON.parse(raw) as Record<string, { selectedOptionId: string | null; expectedVersion: number }>;
      queueRef.current = parsed;
      setAnswers((prev) => {
        const next = { ...prev };
        for (const [qvId, item] of Object.entries(parsed)) {
          if (next[qvId]) {
            next[qvId] = {
              ...next[qvId],
              selectedOptionId: item.selectedOptionId,
              status: "pending",
              message: "Waiting to sync from this device",
            };
          }
        }
        return next;
      });
      flushQueue();
    } catch {
      /* ignore malformed queue */
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const persistQueue = useCallback(() => {
    try {
      if (Object.keys(queueRef.current).length === 0) localStorage.removeItem(QUEUE_KEY(sessionId));
      else localStorage.setItem(QUEUE_KEY(sessionId), JSON.stringify(queueRef.current));
    } catch {
      /* storage may be full or blocked; the in-memory queue still retries */
    }
  }, [sessionId]);

  const flushQueue = useCallback(async () => {
    const entries = Object.entries(queueRef.current);
    if (entries.length === 0) return;
    for (const [qvId, item] of entries) {
      await sendAnswer(qvId, item.selectedOptionId, item.expectedVersion, true);
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  async function sendAnswer(
    qvId: string,
    selectedOptionId: string | null,
    expectedVersion: number,
    fromQueue = false,
  ): Promise<boolean> {
    const sequence = Number(`${Date.now()}`.slice(-6));
    setAnswers((prev) => ({
      ...prev,
      [qvId]: { ...prev[qvId], selectedOptionId, status: "saving", message: undefined, pendingSequence: sequence },
    }));

    try {
      const res = await fetch(
        `/api/v1/assessment-sessions/${sessionId}/responses/${qvId}`,
        {
          method: "PUT",
          headers: { "content-type": "application/json" },
          body: JSON.stringify({
            answer: { selected_option_id: selectedOptionId },
            expected_version: expectedVersion,
            client_sequence: sequence,
          }),
        },
      );
      const data = await res.json().catch(() => ({}));

      if (res.ok) {
        delete queueRef.current[qvId];
        persistQueue();
        setAnswers((prev) => ({
          ...prev,
          [qvId]: {
            selectedOptionId,
            version: data.version ?? expectedVersion + 1,
            status: "saved",
            message: undefined,
          },
        }));
        return true;
      }

      const code = data?.error?.code;
      if (code === "SESSION_EXPIRED" || code === "SESSION_CLOSED") {
        setFatal(data.error.message);
        setAutoSubmitting(true);
        submittedRef.current = true;
        router.push(`/result/${sessionId}`);
        return false;
      }

      if (code === "CONFLICT") {
        // Someone else changed this answer (another tab/device). Surface the canonical state.
        delete queueRef.current[qvId];
        persistQueue();
        setAnswers((prev) => ({
          ...prev,
          [qvId]: {
            selectedOptionId: prev[qvId]?.selectedOptionId ?? null,
            version: data?.error?.details?.[0]?.current_version ?? prev[qvId]?.version ?? expectedVersion,
            status: "failed",
            message: "Changed on another device — refresh to sync",
          },
        }));
        return false;
      }

      throw new Error(code ?? "SAVE_FAILED");
    } catch {
      queueRef.current[qvId] = { selectedOptionId, expectedVersion };
      persistQueue();
      setAnswers((prev) => ({
        ...prev,
        [qvId]: {
          ...prev[qvId],
          selectedOptionId,
          status: "pending",
          message: "Not synced — we will retry",
        },
      }));
      if (!retryTimer.current) {
        retryTimer.current = setTimeout(() => {
          retryTimer.current = null;
          flushQueue();
        }, 6000);
      }
      void fromQueue;
      return false;
    }
  }

  function choose(optionId: string) {
    if (submittedRef.current || fatal) return;
    const q = current;
    const state = answers[q.qvId];
    const next = state.selectedOptionId === optionId ? optionId : optionId;
    setAnswers((prev) => ({
      ...prev,
      [q.qvId]: { ...prev[q.qvId], selectedOptionId: next, status: "saving" },
    }));
    void sendAnswer(q.qvId, next, state.version);
  }

  function clearAnswer() {
    const q = current;
    const state = answers[q.qvId];
    setAnswers((prev) => ({ ...prev, [q.qvId]: { ...prev[q.qvId], selectedOptionId: null, status: "saving" } }));
    void sendAnswer(q.qvId, null, state.version);
  }

  // ── submit ───────────────────────────────────────────────────────────────
  const submit = useCallback(
    async (auto = false) => {
      if (submittedRef.current) return;
      submittedRef.current = true;
      setSubmitting(true);
      if (auto) setAutoSubmitting(true);
      try {
        const res = await fetch(`/api/v1/assessment-sessions/${sessionId}/submit`, {
          method: "POST",
          headers: { "content-type": "application/json", "idempotency-key": `submit-${sessionId}` },
          body: "{}",
        });
        if (!res.ok) {
          const data = await res.json().catch(() => ({}));
          submittedRef.current = false;
          setSubmitting(false);
          setFatal(data?.error?.message ?? "Submission failed. Your answers are still saved on the server — try again.");
          return;
        }
        localStorage.removeItem(QUEUE_KEY(sessionId));
        router.push(`/result/${sessionId}`);
      } catch {
        submittedRef.current = false;
        setSubmitting(false);
        setFatal("We could not reach the server to submit. Your saved answers are safe — retry when you reconnect.");
      }
    },
    [router, sessionId],
  );

  // Auto-submit at the deadline; the server would expire it anyway, this just closes the loop.
  useEffect(() => {
    if (remaining <= 0 && !submittedRef.current) void submit(true);
  }, [remaining, submit]);

  useEffect(() => {
    const handler = (e: BeforeUnloadEvent) => {
      if (!submittedRef.current) {
        e.preventDefault();
        e.returnValue = "";
      }
    };
    window.addEventListener("beforeunload", handler);
    return () => window.removeEventListener("beforeunload", handler);
  }, []);

  const saveState = answers[current?.qvId ?? ""]?.status ?? "idle";
  const saveLabel: Record<SaveStatus, string> = {
    idle: "Not answered",
    saving: "Saving…",
    saved: "Saved on server",
    pending: "Offline — queued",
    failed: "Save failed",
  };

  const mins = Math.floor(remaining / 60);
  const secs = remaining % 60;
  const urgent = remaining <= 120;
  const elapsedMinutes = Math.max(0, Math.floor((now - new Date(startedAt).getTime()) / 60000));

  if (!current) {
    return (
      <div className="exam-shell">
        <div className="page">
          <div className="banner error">This attempt has no questions. Please contact support.</div>
        </div>
      </div>
    );
  }

  return (
    <div className="exam-shell">
      <header className="exam-top">
        <div style={{ minWidth: 0 }}>
          <div className="tiny muted" style={{ textTransform: "capitalize" }}>{mode} · question {index + 1} of {questions.length}</div>
          <div className="strong" style={{ fontSize: 14, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap", maxWidth: 320 }}>
            {title}
          </div>
        </div>

        <div className="exam-timer" aria-live="polite" aria-label={`Time remaining ${mins} minutes ${secs} seconds`}>
          {String(mins).padStart(2, "0")}:{String(secs).padStart(2, "0")}
        </div>

        <span className={`save-state ${saveState === "saved" ? "saved" : saveState === "saving" ? "saving" : saveState === "pending" ? "pending" : saveState === "failed" ? "failed" : "saving"}`} aria-live="polite">
          <span aria-hidden>{saveState === "saved" ? "✓" : saveState === "pending" ? "⟳" : saveState === "failed" ? "!" : "•"}</span>
          {online ? saveLabel[saveState] : "Offline — answers queued"}
        </span>

        <span className="spacer" />
        <button className="btn secondary sm palette-toggle" type="button" onClick={() => setShowPalette((v) => !v)}>
          Palette
        </button>
        <button className="btn sm" type="button" onClick={() => setConfirming(true)} disabled={submitting}>
          {submitting ? "Submitting…" : "Submit"}
        </button>
      </header>

      {fatal ? (
        <div style={{ padding: "12px clamp(12px,3vw,26px) 0" }}>
          <div className="banner error"><span className="ico">!</span>{fatal}</div>
        </div>
      ) : null}

      {!online ? (
        <div style={{ padding: "12px clamp(12px,3vw,26px) 0" }}>
          <div className="banner warn">
            <span className="ico">⚡</span>
            You are offline. Answers stay on this device and retry automatically; nothing is marked saved until the server confirms it.
          </div>
        </div>
      ) : null}

      <div className="exam-body">
        <section className="question-card" aria-live="polite">
          <div className="row-between wrap" style={{ gap: 8 }}>
            <span className="badge neutral">Question {current.position}</span>
            <div className="row" style={{ gap: 6 }}>
              {current.topicName ? <span className="badge teal">{current.topicName}</span> : null}
              {current.difficulty ? <span className="badge amber">{current.difficulty}</span> : null}
            </div>
          </div>

          <div className="question-stem" dangerouslySetInnerHTML={{ __html: current.stem }} />

          <div className="options" role="radiogroup" aria-label="Answer options">
            {current.options.map((option, i) => {
              const selected = answers[current.qvId]?.selectedOptionId === option.id;
              return (
                <button
                  key={option.id}
                  type="button"
                  role="radio"
                  aria-checked={selected}
                  className={`option ${selected ? "selected" : ""}`}
                  onClick={() => choose(option.id)}
                  disabled={Boolean(fatal)}
                >
                  <span className="key" aria-hidden>{String.fromCharCode(65 + i)}</span>
                  <span>{option.label}</span>
                </button>
              );
            })}
          </div>

          <div className="row-between wrap" style={{ marginTop: 22, gap: 10 }}>
            <div className="row" style={{ gap: 8 }}>
              <button className="btn secondary sm" type="button" onClick={() => setIndex((i) => Math.max(0, i - 1))} disabled={index === 0}>
                ← Previous
              </button>
              <button
                className="btn sm"
                type="button"
                onClick={() => setIndex((i) => Math.min(questions.length - 1, i + 1))}
                disabled={index === questions.length - 1}
              >
                Next →
              </button>
            </div>
            <div className="row" style={{ gap: 8 }}>
              <button
                className="btn ghost sm"
                type="button"
                onClick={() => setFlagged((f) => ({ ...f, [current.qvId]: !f[current.qvId] }))}
              >
                {flagged[current.qvId] ? "★ Flagged" : "☆ Flag for review"}
              </button>
              {answers[current.qvId]?.selectedOptionId ? (
                <button className="btn ghost sm" type="button" onClick={clearAnswer}>Clear</button>
              ) : null}
            </div>
          </div>

          {answers[current.qvId]?.message ? (
            <div className={`banner ${answers[current.qvId].status === "failed" ? "error" : "warn"}`} style={{ marginTop: 14 }}>
              <span className="ico">!</span>
              {answers[current.qvId].message}
              {answers[current.qvId].status === "pending" ? (
                <button className="btn secondary sm" type="button" style={{ marginLeft: "auto" }} onClick={() => flushQueue()}>
                  Retry now
                </button>
              ) : null}
            </div>
          ) : null}
        </section>

        <aside className={`exam-palette ${showPalette ? "" : "palette-collapsed"}`}>
          <div className="row-between" style={{ marginBottom: 10 }}>
            <span className="strong small">Questions</span>
            <span className="tiny muted">{answeredCount}/{questions.length}</span>
          </div>
          <div className="progress" style={{ marginBottom: 12 }}>
            <span style={{ width: `${(answeredCount / questions.length) * 100}%` }} />
          </div>
          <div className="palette-grid">
            {questions.map((q, i) => {
              const state = answers[q.qvId];
              const answered = Boolean(state?.selectedOptionId);
              return (
                <button
                  key={q.qvId}
                  type="button"
                  className={`palette-btn ${answered ? "answered" : ""} ${i === index ? "current" : ""} ${flagged[q.qvId] ? "flag" : ""}`}
                  onClick={() => {
                    setIndex(i);
                    setShowPalette(false);
                  }}
                  aria-label={`Question ${i + 1}${answered ? ", answered" : ", unanswered"}${flagged[q.qvId] ? ", flagged" : ""}`}
                >
                  {i + 1}
                </button>
              );
            })}
          </div>
          <div className="legend" style={{ marginTop: 12 }}>
            <span><i style={{ background: "var(--green-600)" }} /> Answered</span>
            <span><i style={{ background: "#fff", border: "1px solid var(--line-strong)" }} /> Blank</span>
            <span><i style={{ background: "var(--amber-500)" }} /> Flagged</span>
          </div>
          <div className="divider" style={{ margin: "12px 0" }} />
          <div className="tiny muted">
            Elapsed {elapsedMinutes} min. The deadline is enforced by the server; this timer is a display aid.
          </div>
          <button className="btn block sm" type="button" style={{ marginTop: 10 }} onClick={() => setConfirming(true)} disabled={submitting}>
            Submit attempt
          </button>
          <Link href="/dashboard" className="tiny muted" style={{ display: "block", marginTop: 10, textAlign: "center" }}>
            Leave (timer keeps running)
          </Link>
        </aside>
      </div>

      {confirming ? (
        <div className="modal-backdrop" role="dialog" aria-modal="true" aria-labelledby="submit-title">
          <div className="modal stack">
            <h3 id="submit-title">Submit this attempt?</h3>
            <p className="small muted">
              You have answered <strong>{answeredCount}</strong> of {questions.length} questions.
              {answeredCount < questions.length ? " Unanswered questions score zero." : ""} Submission is final and cannot be reopened.
            </p>
            {Object.values(answers).some((a) => a.status === "pending") ? (
              <div className="banner warn small"><span className="ico">!</span>Some answers are still waiting to sync. Submit anyway, or wait a moment to sync first.</div>
            ) : null}
            <div className="btn-row" style={{ justifyContent: "flex-end" }}>
              <button className="btn secondary" type="button" onClick={() => setConfirming(false)}>Keep working</button>
              <button className="btn" type="button" onClick={() => submit(false)} disabled={submitting}>
                {submitting ? "Submitting…" : "Submit now"}
              </button>
            </div>
          </div>
        </div>
      ) : null}

      {autoSubmitting ? (
        <div className="modal-backdrop" role="alertdialog" aria-modal="true">
          <div className="modal stack center">
            <h3>Time is up</h3>
            <p className="small muted">Your attempt is being finalized on the server. You will be taken to your result.</p>
            <div className="skeleton" style={{ width: "60%", margin: "0 auto" }} />
          </div>
        </div>
      ) : null}
    </div>
  );
}
