"use client";

import Link from "next/link";
import { useActionState, useState } from "react";
import { Loader2 } from "lucide-react";

import { daysUntil, studyLoadAdvice, type SignupDraft } from "@edumart/shared";

import { Alert } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Field } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { saveSchedule } from "@/lib/signup-actions";
import { EMPTY_STATE } from "@/lib/signup-state";

const MINUTES = [15, 30, 45, 60, 90, 120];

export function ScheduleForm({ draft }: { draft: SignupDraft }) {
  const [state, formAction, pending] = useActionState(saveSchedule, EMPTY_STATE);
  const [targetDate, setTargetDate] = useState(draft.targetDate);
  const [dailyMinutes, setDailyMinutes] = useState(draft.dailyMinutes || 30);
  const days = daysUntil(targetDate);
  const today = new Date().toISOString().slice(0, 10);

  return (
    <form action={formAction} className="space-y-6">
      {state.message ? <Alert tone="error" title="Could not save this step">{state.message}</Alert> : null}

      <Field
        id="targetDate"
        label="Exam date"
        hint={days !== null ? `${days} day${days === 1 ? "" : "s"} to go — we'll pace your plan to that date.` : undefined}
        error={state.errors.targetDate}
        required
      >
        <Input
          id="targetDate"
          name="targetDate"
          type="date"
          min={today}
          value={targetDate}
          onChange={(event) => setTargetDate(event.target.value)}
          aria-invalid={Boolean(state.errors.targetDate)}
          required
        />
      </Field>

      <fieldset className="space-y-2">
        <legend className="text-sm font-medium">Daily study time</legend>
        <input type="hidden" name="dailyMinutes" value={dailyMinutes} />
        <div className="grid grid-cols-3 gap-2">
          {MINUTES.map((minutes) => {
            const active = minutes === dailyMinutes;
            return (
              <Label
                key={minutes}
                className={
                  active
                    ? "flex min-h-11 cursor-pointer items-center justify-center rounded-lg border border-primary bg-secondary font-semibold text-secondary-foreground"
                    : "flex min-h-11 cursor-pointer items-center justify-center rounded-lg border border-border bg-card text-muted-foreground hover:border-input"
                }
              >
                <input
                  type="radio"
                  name="dailyMinutesChoice"
                  value={minutes}
                  checked={active}
                  onChange={() => setDailyMinutes(minutes)}
                  className="sr-only"
                />
                {minutes >= 60 ? `${minutes / 60}h` : `${minutes}m`}
              </Label>
            );
          })}
        </div>
        <p className="text-xs text-muted-foreground">{studyLoadAdvice(dailyMinutes)}</p>
        {state.errors.dailyMinutes ? (
          <p role="alert" className="text-sm font-medium text-destructive">
            {state.errors.dailyMinutes}
          </p>
        ) : null}
      </fieldset>

      <div className="flex items-center justify-between gap-3">
        <Button asChild variant="ghost">
          <Link href="/register/subjects">Back</Link>
        </Button>
        <Button type="submit" disabled={pending}>
          {pending ? <Loader2 aria-hidden="true" className="animate-spin" /> : null}
          {pending ? "Saving…" : "Continue"}
        </Button>
      </div>
    </form>
  );
}
