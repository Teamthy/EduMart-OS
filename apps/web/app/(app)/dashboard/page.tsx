import type { Metadata } from "next";
import Link from "next/link";
import { ArrowRight, Clock, Flame, Sparkles, Target, TrendingUp, Trophy } from "lucide-react";

import { formatPercent, formatRelativeDate, type Dashboard } from "@edumart/shared";

import { Alert } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { EmptyState } from "@/components/ui/empty-state";
import { Progress } from "@/components/ui/progress";
import { ServerApiError, serverFetch } from "@/lib/server-api";
import { requireUser } from "@/lib/session";

export const metadata: Metadata = { title: "Dashboard" };
export const dynamic = "force-dynamic";

export default async function DashboardPage() {
  const user = await requireUser("/dashboard");

  let dashboard: Dashboard;
  try {
    ({ dashboard } = await serverFetch<{ dashboard: Dashboard }>("/v1/dashboard"));
  } catch (error) {
    return (
      <Alert tone="error" title="We couldn't load your dashboard">
        {error instanceof ServerApiError ? error.message : "Please refresh to try again."}
      </Alert>
    );
  }

  return (
    <div className="space-y-6">
      <header className="space-y-1">
        <p className="text-sm text-muted-foreground">
          {dashboard.examPathway || "Your exam"} · {dashboard.targetDate ? `${dashboard.daysToExam} days to go` : "Set your exam date"}
        </p>
        <h1 className="text-2xl">Good to see you, {dashboard.displayName.split(" ")[0]}</h1>
        <p className="text-sm text-muted-foreground">{dashboard.recommendation}</p>
      </header>

      {dashboard.activeSessionId ? (
        <Card className="border-primary bg-secondary">
          <CardContent className="flex flex-wrap items-center justify-between gap-3 p-4">
            <div className="flex items-center gap-3">
              <Clock aria-hidden="true" className="size-5 text-primary" />
              <div>
                <p className="font-semibold">You have an attempt in progress</p>
                <p className="text-sm text-muted-foreground">
                  The server clock is running. Resume before the deadline closes it.
                </p>
              </div>
            </div>
            <Button asChild>
              <Link href={`/attempt/${dashboard.activeSessionId}`}>Resume attempt</Link>
            </Button>
          </CardContent>
        </Card>
      ) : null}

      <section aria-labelledby="stats" className="grid grid-cols-2 gap-3 md:grid-cols-4">
        <h2 id="stats" className="sr-only">
          Your numbers
        </h2>
        <StatCard icon={TrendingUp} label="Average score" value={formatPercent(dashboard.averagePercent)} hint={`${dashboard.attemptsCompleted} completed`} />
        <StatCard icon={Flame} label="Streak" value={`${dashboard.streakDays} day${dashboard.streakDays === 1 ? "" : "s"}`} hint="Practice daily to keep it" />
        <StatCard icon={Trophy} label="Learning points" value={String(dashboard.pointsBalance)} hint="Earned from completed work" />
        <StatCard icon={Target} label="This week" value={`${dashboard.studyMinutesThisWeek} min`} hint="Measured server-side" />
      </section>

      <div className="grid gap-4 lg:grid-cols-3">
        <Card className="lg:col-span-2">
          <CardHeader className="flex-row items-center justify-between">
            <CardTitle>Recent attempts</CardTitle>
            <Link href="/progress" className="text-sm font-semibold text-primary underline-offset-4 hover:underline">
              See all
            </Link>
          </CardHeader>
          <CardContent>
            {dashboard.recentAttempts.length === 0 ? (
              <EmptyState
                title="No attempts yet"
                description="A short practice set is the fastest way to see where you stand."
                icon={<Sparkles aria-hidden="true" className="size-6" />}
                action={
                  <Button asChild>
                    <Link href="/practice">Start a practice set</Link>
                  </Button>
                }
              />
            ) : (
              <ul className="divide-y divide-border">
                {dashboard.recentAttempts.map((attempt) => (
                  <li key={attempt.id} className="flex items-center justify-between gap-3 py-3 first:pt-0 last:pb-0">
                    <div className="min-w-0">
                      <p className="truncate font-medium">{attempt.title}</p>
                      <p className="text-xs text-muted-foreground">
                        {attempt.status === "in_progress" ? "In progress" : formatRelativeDate(attempt.submittedAt ?? attempt.startedAt)}
                      </p>
                    </div>
                    <div className="flex shrink-0 items-center gap-3">
                      {attempt.scorePercent !== null ? (
                        <Badge variant={attempt.scorePercent >= 70 ? "success" : attempt.scorePercent >= 50 ? "warning" : "destructive"}>
                          {formatPercent(attempt.scorePercent)}
                        </Badge>
                      ) : (
                        <Badge variant="outline">Open</Badge>
                      )}
                      <Button asChild variant="ghost" size="sm">
                        <Link href={attempt.status === "in_progress" ? `/attempt/${attempt.id}` : `/results/${attempt.id}`}>
                          View <ArrowRight aria-hidden="true" />
                        </Link>
                      </Button>
                    </div>
                  </li>
                ))}
              </ul>
            )}
          </CardContent>
        </Card>

        <div className="space-y-4">
          <Card>
            <CardHeader>
              <CardTitle>Focus next</CardTitle>
            </CardHeader>
            <CardContent className="space-y-3">
              {dashboard.weakTopics.length === 0 ? (
                <p className="text-sm text-muted-foreground">
                  Complete a few practice sets and we&apos;ll name the topics costing you marks.
                </p>
              ) : (
                dashboard.weakTopics.map((topic) => (
                  <div key={topic.topicId} className="space-y-1">
                    <div className="flex items-center justify-between gap-2 text-sm">
                      <span className="truncate font-medium">{topic.topicName}</span>
                      <span className="text-muted-foreground">{topic.accuracy}%</span>
                    </div>
                    <Progress value={topic.accuracy} aria-label={`${topic.topicName} accuracy ${topic.accuracy} percent`} />
                    <p className="text-xs text-muted-foreground">{topic.subjectName}</p>
                  </div>
                ))
              )}
              <Button asChild className="w-full" variant="secondary">
                <Link href="/practice">Practise weak topics</Link>
              </Button>
            </CardContent>
          </Card>

          {dashboard.strongTopics.length > 0 ? (
            <Card>
              <CardHeader>
                <CardTitle>Going well</CardTitle>
              </CardHeader>
              <CardContent className="space-y-2">
                {dashboard.strongTopics.map((topic) => (
                  <div key={topic.topicId} className="flex items-center justify-between text-sm">
                    <span className="truncate">{topic.topicName}</span>
                    <Badge variant="success">{topic.accuracy}%</Badge>
                  </div>
                ))}
              </CardContent>
            </Card>
          ) : null}
        </div>
      </div>

      <p className="text-xs text-muted-foreground">
        Timers on this screen are display-only. An attempt ends when the server deadline passes,
        whether or not this page is open.
      </p>
    </div>
  );
}

function StatCard({
  icon: Icon,
  label,
  value,
  hint,
}: {
  icon: typeof TrendingUp;
  label: string;
  value: string;
  hint: string;
}) {
  return (
    <Card>
      <CardContent className="space-y-1 p-4">
        <Icon aria-hidden="true" className="size-4 text-primary" />
        <p className="text-xs uppercase tracking-wide text-muted-foreground">{label}</p>
        <p className="font-serif text-xl">{value}</p>
        <p className="text-xs text-muted-foreground">{hint}</p>
      </CardContent>
    </Card>
  );
}
