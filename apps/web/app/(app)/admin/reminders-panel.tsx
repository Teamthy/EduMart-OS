"use client";

import { useRouter } from "next/navigation";
import { useState } from "react";
import { Loader2, Send } from "lucide-react";

import { ApiError, formatRelativeDate, type Reminder, type ReminderStats } from "@edumart/shared";

import { Alert } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { EmptyState } from "@/components/ui/empty-state";
import { api } from "@/lib/browser-api";

const STATUS_TONE: Record<Reminder["status"], "success" | "warning" | "outline"> = {
  sent: "success",
  pending: "outline",
  claimed: "warning",
  skipped: "outline",
  cancelled: "outline",
};

/**
 * What the system has promised to send, and what it actually did.
 *
 * The sweep button exists because "was this learner told?" should be answerable now rather than at the
 * next scheduler tick. It sends real mail, so the API restricts it to platform administrators and audits
 * every run — the button reflects that rather than hiding the restriction.
 */
export function RemindersPanel({ reminders, stats }: { reminders: Reminder[]; stats: ReminderStats }) {
  const router = useRouter();
  const [working, setWorking] = useState(false);
  const [result, setResult] = useState<string | null>(null);
  const [error, setError] = useState<ApiError | null>(null);

  async function sweep() {
    setWorking(true);
    setError(null);
    setResult(null);
    try {
      const outcome = await api().sweepReminders();
      setResult(
        `Planned ${outcome.planned}, sent ${outcome.sent}, skipped ${outcome.skipped}.`,
      );
      router.refresh();
    } catch (caught) {
      setError(caught instanceof ApiError ? caught : null);
    } finally {
      setWorking(false);
    }
  }

  return (
    <Card>
      <CardHeader className="flex-row items-start justify-between gap-3">
        <div>
          <CardTitle>Reminders</CardTitle>
          <p className="text-sm text-muted-foreground">
            {stats.pending ?? 0} queued · {stats.sent ?? 0} sent · {stats.skipped ?? 0} withdrawn
            {stats.claimed ? ` · ${stats.claimed} in flight` : ""}
          </p>
        </div>
        <Button type="button" variant="secondary" size="sm" onClick={sweep} disabled={working}>
          {working ? (
            <Loader2 className="mr-2 size-4 animate-spin" aria-hidden />
          ) : (
            <Send className="mr-2 size-4" aria-hidden />
          )}
          Sweep now
        </Button>
      </CardHeader>
      <CardContent className="space-y-3">
        {error ? (
          <Alert tone="error" title="The sweep did not run">
            {error.message}
          </Alert>
        ) : null}
        {result ? (
          <Alert tone="success" title="Sweep complete">
            {result}
          </Alert>
        ) : null}

        {reminders.length === 0 ? (
          <EmptyState
            title="Nothing scheduled"
            description="Reminders are created when a learner sets a study target or has an assessment closing soon."
          />
        ) : (
          <ul className="divide-y divide-border">
            {reminders.map((reminder) => (
              <li key={reminder.id} className="space-y-1 py-2.5 first:pt-0 last:pb-0">
                <div className="flex flex-wrap items-center gap-2">
                  <Badge variant={STATUS_TONE[reminder.status]}>{reminder.status}</Badge>
                  <span className="text-sm font-medium">{reminder.subject}</span>
                  <span className="text-xs text-muted-foreground">
                    {reminder.kind.replace("_", " ")} · due {formatRelativeDate(reminder.dueAt)}
                  </span>
                </div>
                <p className="text-xs text-muted-foreground">{reminder.body}</p>
                {reminder.note ? (
                  <p className="text-xs text-muted-foreground">
                    Withdrawn: {reminder.note}. Nothing was sent.
                  </p>
                ) : null}
              </li>
            ))}
          </ul>
        )}
      </CardContent>
    </Card>
  );
}
