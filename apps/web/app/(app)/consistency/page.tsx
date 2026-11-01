import type { Metadata } from "next";
import { Flame, Sparkles, Trophy } from "lucide-react";

import { Alert } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Progress } from "@/components/ui/progress";
import { ServerApiError, serverFetch } from "@/lib/server-api";
import { requireUser } from "@/lib/session";
import type { Consistency } from "@/lib/live-api";

export const metadata: Metadata = { title: "Consistency" };
export const dynamic = "force-dynamic";

/**
 * Consistency.
 *
 * The numbers on this page are derived, not stored: they come from attempts started, live classes attended and
 * credits earned, so a streak cannot drift away from what actually happened. Days are UTC days — the payload
 * says so, and this page repeats it rather than pretending a "day" is obvious.
 */
export default async function ConsistencyPage() {
  await requireUser("/consistency");

  let consistency: Consistency;
  try {
    ({ consistency } = await serverFetch<{ consistency: Consistency }>("/v1/consistency?days=30"));
  } catch (error) {
    if (!(error instanceof ServerApiError)) throw error;
    return <Alert tone="error" title="We couldn't load your consistency">{error.message}</Alert>;
  }

  const percentOfWindow = Math.round((consistency.activeDays / consistency.windowDays) * 100);

  return (
    <div className="space-y-6">
      <header className="space-y-1">
        <h1 className="text-2xl">Consistency</h1>
        <p className="text-sm text-muted-foreground">
          Every day you practised, sat a sitting or joined a live class. Opening the app does not count — that
          would measure notifications, not learning.
        </p>
      </header>

      <div className="grid gap-3 sm:grid-cols-3">
        <Card>
          <CardContent className="space-y-1 p-4">
            <p className="flex items-center gap-1.5 text-xs text-muted-foreground">
              <Flame aria-hidden="true" className="size-3" />
              Current streak
            </p>
            <p className="text-3xl font-semibold">{consistency.currentStreak}</p>
            <p className="text-xs text-muted-foreground">days in a row</p>
          </CardContent>
        </Card>
        <Card>
          <CardContent className="space-y-1 p-4">
            <p className="flex items-center gap-1.5 text-xs text-muted-foreground">
              <Trophy aria-hidden="true" className="size-3" />
              Longest streak
            </p>
            <p className="text-3xl font-semibold">{consistency.longestStreak}</p>
            <p className="text-xs text-muted-foreground">your best run so far</p>
          </CardContent>
        </Card>
        <Card>
          <CardContent className="space-y-1 p-4">
            <p className="flex items-center gap-1.5 text-xs text-muted-foreground">
              <Sparkles aria-hidden="true" className="size-3" />
              Active days
            </p>
            <p className="text-3xl font-semibold">{consistency.activeDays}</p>
            <p className="text-xs text-muted-foreground">of the last {consistency.windowDays}</p>
          </CardContent>
        </Card>
      </div>

      {consistency.streakAtRisk ? (
        <Alert tone="warning" title="Your streak is still alive">
          You have not done anything today ({consistency.timeZone}). A short practice set keeps the run going.
        </Alert>
      ) : null}

      <Card>
        <CardHeader>
          <CardTitle>Last {consistency.windowDays} days</CardTitle>
        </CardHeader>
        <CardContent className="space-y-3">
          <Progress value={percentOfWindow} />
          <p className="text-xs text-muted-foreground">
            {consistency.activeDays} active of {consistency.windowDays} · days are counted in {consistency.timeZone}
          </p>
          <div className="flex flex-wrap gap-1" role="list" aria-label="Activity by day">
            {consistency.days.map((day) => (
              <span
                key={day.date}
                role="listitem"
                title={`${day.date}: ${day.active ? day.kinds.map((kind) => consistency.kindLabels[kind] ?? kind).join(", ") : "no activity"}`}
                className={`size-4 rounded-sm border ${
                  day.active ? "border-primary bg-primary" : "border-border bg-secondary"
                } ${day.isToday ? "outline outline-2 outline-offset-1 outline-primary" : ""}`}
              />
            ))}
          </div>
          <div className="flex flex-wrap gap-2">
            {Object.entries(consistency.kindLabels).map(([kind, label]) => (
              <Badge key={kind} variant="outline">
                {label}
              </Badge>
            ))}
          </div>
          <p className="text-xs text-muted-foreground">
            {consistency.todayActive
              ? "Today counts. That is what keeps the run going."
              : consistency.currentStreak > 0
                ? "Today is still open — the run is alive until the day ends."
                : "Start with a ten-question practice set to begin a run."}
          </p>
        </CardContent>
      </Card>
    </div>
  );
}
