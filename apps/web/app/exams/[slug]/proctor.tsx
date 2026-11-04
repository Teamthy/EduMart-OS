"use client";

import { useCallback, useEffect, useRef, useState } from "react";
import { AlertTriangle, Eye, Megaphone, ShieldOff, ShieldCheck, Timer, Users } from "lucide-react";

import { Alert } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Progress } from "@/components/ui/progress";
import {
  LiveApiError,
  proctorDesk,
  reopenAttempt,
  sendProctorWarning,
  voidAttempt,
  type Exam,
  type ProctorCandidate,
  type ProctorDesk,
  type ProctorWarning,
} from "@/lib/live-api";

/** How often the desk re-reads the sitting. Three seconds is inside the time it takes to read a row. */
const POLL_MS = 3_000;

interface Props {
  exam: Exam;
}

/**
 * The proctor's desk: every attempt in the sitting, while it is open.
 *
 * Three things a proctor can do, and the component is arranged around them rather than around the data: send a
 * warning (whose first two kinds are composed by the server from what it already recorded), annul an attempt
 * with a reason, and undo that while the sitting is still open. Everything else is evidence — progress,
 * attention signals, when the server last heard from somebody — and the person in front of the screen decides
 * what it means.
 *
 * It polls. A socket would be a second transport to authenticate and reconnect for a view that changes a few
 * times a minute; when the poll fails, the last good reading stays on screen with a note rather than blanking
 * the console mid-sitting.
 */
export function ExamProctorDesk({ exam }: Props) {
  const [desk, setDesk] = useState<ProctorDesk | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [stale, setStale] = useState(false);
  const [busy, setBusy] = useState<string | null>(null);
  const [open, setOpen] = useState<{ sessionId: string; mode: "warn" | "void" } | null>(null);
  const [kind, setKind] = useState<ProctorWarning["kind"]>("attention");
  const [note, setNote] = useState("");
  const [reason, setReason] = useState("");
  const [flash, setFlash] = useState<string | null>(null);
  const mounted = useRef(true);

  useEffect(() => {
    mounted.current = true;
    return () => {
      mounted.current = false;
    };
  }, []);

  const refresh = useCallback(async () => {
    try {
      const next = await proctorDesk(exam.id);
      if (!mounted.current) return;
      setDesk(next);
      setStale(false);
      setError(null);
    } catch (caught) {
      if (!mounted.current) return;
      // Keep what is on screen. A console that goes blank when one poll fails is a console that loses the
      // sitting exactly when the network is the thing going wrong.
      setStale(true);
      setError(caught instanceof LiveApiError ? caught.message : "Could not read the sitting.");
    }
  }, [exam.id]);

  useEffect(() => {
    void refresh();
    const timer = window.setInterval(() => void refresh(), POLL_MS);
    return () => window.clearInterval(timer);
  }, [refresh]);

  const act = useCallback(
    async (key: string, run: () => Promise<void>, done: string) => {
      setBusy(key);
      setError(null);
      try {
        await run();
        await refresh();
        if (mounted.current) setFlash(done);
      } catch (caught) {
        setError(caught instanceof LiveApiError ? caught.message : "That did not go through.");
      } finally {
        if (mounted.current) setBusy(null);
      }
    },
    [refresh],
  );

  if (!desk) {
    return (
      <Card>
        <CardHeader>
          <CardTitle>The proctor&apos;s desk</CardTitle>
        </CardHeader>
        <CardContent className="space-y-2">
          {error ? <Alert tone="error">{error}</Alert> : null}
          <p className="text-sm text-muted-foreground">Reading the sitting…</p>
        </CardContent>
      </Card>
    );
  }

  const { sitting } = desk;

  return (
    <div className="space-y-4">
      <Card>
        <CardHeader className="flex flex-row flex-wrap items-center justify-between gap-2">
          <CardTitle className="flex items-center gap-2">
            <Eye aria-hidden="true" className="size-5" />
            Proctoring {sitting.title}
          </CardTitle>
          <div className="flex flex-wrap items-center gap-2 text-xs text-muted-foreground">
            <Badge variant={sitting.open ? "success" : "outline"}>{sitting.open ? "open" : "closed"}</Badge>
            <span className="flex items-center gap-1">
              <Timer aria-hidden="true" className="size-3" />
              {formatCountdown(sitting.secondsUntilClose)} left in the window
            </span>
            <span className="flex items-center gap-1">
              <Users aria-hidden="true" className="size-3" />
              {sitting.inProgress} in progress · {sitting.submitted} finished · {sitting.voided} annulled
            </span>
            {sitting.flagged > 0 ? (
              <Badge variant="warning">
                <AlertTriangle aria-hidden="true" className="mr-1 size-3" />
                {sitting.flagged} with signals
              </Badge>
            ) : null}
            {sitting.unreadWarnings > 0 ? (
              <Badge variant="outline">{sitting.unreadWarnings} warning(s) unread</Badge>
            ) : null}
          </div>
        </CardHeader>
        <CardContent className="space-y-3">
          {error ? (
            <Alert tone="warning" title={stale ? "Showing the last reading" : "Something went wrong"}>
              {error}
            </Alert>
          ) : null}
          {flash ? <Alert tone="success">{flash}</Alert> : null}
          {desk.candidates.length === 0 ? (
            <p className="text-sm text-muted-foreground">Nobody has started yet.</p>
          ) : null}
          <ul className="space-y-2">
            {desk.candidates.map((candidate) => (
              <CandidateRow
                key={candidate.sessionId}
                candidate={candidate}
                open={open?.sessionId === candidate.sessionId ? open.mode : null}
                busy={busy}
                kind={kind}
                note={note}
                reason={reason}
                onKind={setKind}
                onNote={setNote}
                onReason={setReason}
                onToggle={(mode) => {
                  setFlash(null);
                  setOpen(open?.sessionId === candidate.sessionId && open.mode === mode ? null : { sessionId: candidate.sessionId, mode });
                  setKind("attention");
                  setNote("");
                  setReason("");
                }}
                onWarn={() =>
                  void act(
                    `warn:${candidate.sessionId}`,
                    async () => {
                      await sendProctorWarning(exam.id, {
                        sessionIds: [candidate.sessionId],
                        kind,
                        ...(kind === "notice" ? { note } : {}),
                      });
                      setOpen(null);
                    },
                    `Warning sent to ${candidate.displayName}.`,
                  )
                }
                onVoid={() =>
                  void act(
                    `void:${candidate.sessionId}`,
                    async () => {
                      await voidAttempt(exam.id, candidate.sessionId, reason);
                      setOpen(null);
                    },
                    `${candidate.displayName}'s attempt was annulled.`,
                  )
                }
                onReopen={() =>
                  void act(
                    `reopen:${candidate.sessionId}`,
                    async () => {
                      await reopenAttempt(exam.id, candidate.sessionId, "reopened from the proctor desk");
                    },
                    `${candidate.displayName}'s attempt is live again.`,
                  )
                }
              />
            ))}
          </ul>
        </CardContent>
      </Card>

      {desk.actions.length > 0 ? (
        <Card>
          <CardHeader>
            <CardTitle className="text-base">Interventions</CardTitle>
          </CardHeader>
          <CardContent>
            <ul className="space-y-1 text-xs text-muted-foreground">
              {desk.actions.map((action) => (
                <li key={action.id}>
                  <span className="text-foreground">{action.actorName}</span> {verbFor(action.action)} ·{" "}
                  {new Date(action.occurredAt).toLocaleTimeString()}
                  {action.reason ? ` · ${action.reason}` : ""}
                </li>
              ))}
            </ul>
          </CardContent>
        </Card>
      ) : null}
    </div>
  );
}

function verbFor(action: "warn" | "void" | "reopen") {
  if (action === "void") return "annulled an attempt";
  if (action === "reopen") return "reopened an attempt";
  return "warned";
}

interface RowProps {
  candidate: ProctorCandidate;
  open: "warn" | "void" | null;
  busy: string | null;
  kind: ProctorWarning["kind"];
  note: string;
  reason: string;
  onKind: (kind: ProctorWarning["kind"]) => void;
  onNote: (note: string) => void;
  onReason: (reason: string) => void;
  onToggle: (mode: "warn" | "void") => void;
  onWarn: () => void;
  onVoid: () => void;
  onReopen: () => void;
}

function CandidateRow(props: RowProps) {
  const { candidate } = props;
  const live = candidate.status === "in_progress";
  const progress = candidate.questions > 0 ? Math.round((candidate.answered / candidate.questions) * 100) : 0;
  const signals = candidate.integrity?.signals ?? 0;

  return (
    <li className="rounded-lg border p-3">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <div className="min-w-0">
          <p className="truncate text-sm font-medium">
            {candidate.displayName}{" "}
            <span className="text-xs font-normal text-muted-foreground">
              {statusLabel(candidate)}
            </span>
          </p>
          <p className="text-xs text-muted-foreground">
            {candidate.answered}/{candidate.questions} answered
            {live ? ` · ${formatCountdown(candidate.remainingSeconds)} left on their clock` : ""}
            {candidate.lastSeenAt ? ` · last heard ${new Date(candidate.lastSeenAt).toLocaleTimeString()}` : ""}
          </p>
        </div>
        <div className="flex flex-wrap items-center gap-2">
          {signals > 0 ? (
            <Badge variant="warning">
              {signals} signal{signals === 1 ? "" : "s"}
              {candidate.integrity && candidate.integrity.focusLost > 0
                ? ` · left ${candidate.integrity.focusLost}x`
                : ""}
            </Badge>
          ) : null}
          {candidate.warnings > 0 ? (
            <Badge variant={candidate.unreadWarnings > 0 ? "warning" : "outline"}>
              {candidate.warnings} warning{candidate.warnings === 1 ? "" : "s"}
              {candidate.unreadWarnings > 0 ? ` · ${candidate.unreadWarnings} unread` : " · read"}
            </Badge>
          ) : null}
          {live ? (
            <>
              <Button type="button" size="sm" variant="outline" onClick={() => props.onToggle("warn")}>
                <Megaphone aria-hidden="true" className="size-3.5" />
                Warn
              </Button>
              <Button type="button" size="sm" variant="outline" onClick={() => props.onToggle("void")}>
                <ShieldOff aria-hidden="true" className="size-3.5" />
                Annul
              </Button>
            </>
          ) : null}
          {candidate.status === "voided" ? (
            <Button type="button" size="sm" variant="outline" onClick={props.onReopen}>
              <ShieldCheck aria-hidden="true" className="size-3.5" />
              Reopen
            </Button>
          ) : null}
        </div>
      </div>

      <Progress value={progress} className="mt-2 h-1.5" />

      {candidate.status === "voided" && candidate.voidReason ? (
        <p className="mt-2 text-xs text-muted-foreground">
          Annulled{candidate.voidedBy ? ` by ${candidate.voidedBy}` : ""}: {candidate.voidReason}
        </p>
      ) : null}

      {candidate.lastWarning ? (
        <p className="mt-2 text-xs text-muted-foreground">
          Last warning ({candidate.lastWarning.kind}): {candidate.lastWarning.note}
          {candidate.lastWarning.acknowledgedAt ? " · read" : " · not read"}
        </p>
      ) : null}

      {props.open === "warn" ? (
        <div className="mt-3 space-y-2 rounded-md bg-secondary/40 p-3">
          <div className="flex flex-wrap gap-2">
            {(["attention", "time", "notice"] as const).map((option) => (
              <Button
                key={option}
                type="button"
                size="sm"
                variant={props.kind === option ? "default" : "outline"}
                onClick={() => props.onKind(option)}
              >
                {option === "attention" ? "Attention" : option === "time" ? "Time left" : "My message"}
              </Button>
            ))}
          </div>
          <p className="text-xs text-muted-foreground">
            {props.kind === "attention"
              ? "The server writes this one, quoting the attempt's own signal count."
              : props.kind === "time"
                ? "The server writes this one, quoting that attempt's remaining seconds."
                : "Your own words, 3–250 characters. The candidate reads exactly this."}
          </p>
          {props.kind === "notice" ? (
            <Input
              value={props.note}
              onChange={(event) => props.onNote(event.target.value)}
              maxLength={250}
              placeholder="Keep your camera on and stay on this tab, please."
              aria-label="Warning message"
            />
          ) : null}
          <Button
            type="button"
            size="sm"
            disabled={props.busy === `warn:${candidate.sessionId}` || (props.kind === "notice" && props.note.trim().length < 3)}
            onClick={props.onWarn}
          >
            Send warning
          </Button>
        </div>
      ) : null}

      {props.open === "void" ? (
        <div className="mt-3 space-y-2 rounded-md bg-secondary/40 p-3">
          <p className="text-xs text-muted-foreground">
            Annulling ends the attempt now. The engine then refuses answers and submission, the attempt stays in
            the report as annulled, and your name and reason are recorded with it.
          </p>
          <Input
            value={props.reason}
            onChange={(event) => props.onReason(event.target.value)}
            maxLength={250}
            placeholder="What did you see? (required, 3–250 characters)"
            aria-label="Reason"
          />
          <Button
            type="button"
            size="sm"
            variant="destructive"
            disabled={props.busy === `void:${candidate.sessionId}` || props.reason.trim().length < 3}
            onClick={props.onVoid}
          >
            Annul this attempt
          </Button>
        </div>
      ) : null}
    </li>
  );
}

function statusLabel(candidate: ProctorCandidate) {
  switch (candidate.status) {
    case "in_progress":
      return "sitting";
    case "voided":
      return "annulled";
    case "submitted":
      return "handed in";
    case "expired":
      return "out of time";
    default:
      return candidate.status;
  }
}

function formatCountdown(seconds: number) {
  if (seconds <= 0) return "0m";
  const minutes = Math.floor(seconds / 60);
  if (minutes < 1) return `${seconds}s`;
  return `${minutes}m`;
}
