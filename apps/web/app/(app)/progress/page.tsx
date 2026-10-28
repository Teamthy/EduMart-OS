import type { Metadata } from "next";
import Link from "next/link";
import { BarChart3, TrendingUp } from "lucide-react";

import { formatPercent, formatRelativeDate, type LearningSummary } from "@edumart/shared";

import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { EmptyState } from "@/components/ui/empty-state";
import { Progress } from "@/components/ui/progress";
import { ServerApiError, serverFetch } from "@/lib/server-api";
import { requireUser } from "@/lib/session";
import { Alert } from "@/components/ui/alert";

export const metadata: Metadata = { title: "Progress" };
export const dynamic = "force-dynamic";

export default async function ProgressPage() {
  await requireUser("/progress");

  let summary: LearningSummary;
  try {
    ({ summary } = await serverFetch<{ summary: LearningSummary }>("/v1/learning-summary"));
  } catch (error) {
    return (
      <Alert tone="error" title="We couldn't load your progress">
        {error instanceof ServerApiError ? error.message : "Please try again."}
      </Alert>
    );
  }

  return (
    <div className="space-y-6">
      <header className="space-y-1">
        <h1 className="text-2xl">Progress</h1>
        <p className="text-sm text-muted-foreground">
          Derived from every response you have submitted — nothing here is self-reported.
        </p>
      </header>

      <section className="grid grid-cols-2 gap-3 md:grid-cols-4">
        <Metric label="Attempts" value={String(summary.totalAttempts)} />
        <Metric label="Questions answered" value={String(summary.questionsAnswered)} />
        <Metric label="Average" value={formatPercent(summary.averagePercent)} />
        <Metric label="Best" value={formatPercent(summary.bestPercent)} />
      </section>

      {summary.bySubject.length > 0 ? (
        <Card>
          <CardHeader>
            <CardTitle>Accuracy by subject</CardTitle>
          </CardHeader>
          <CardContent className="space-y-3">
            {summary.bySubject.map((subject) => (
              <div key={subject.subjectId} className="space-y-1">
                <div className="flex items-center justify-between text-sm">
                  <span className="font-medium">{subject.subjectName}</span>
                  <span className="text-muted-foreground">
                    {formatPercent(subject.accuracy)} · {subject.attempts} answers
                  </span>
                </div>
                <Progress value={subject.accuracy} aria-label={`${subject.subjectName} accuracy`} />
              </div>
            ))}
          </CardContent>
        </Card>
      ) : null}

      <div className="grid gap-4 lg:grid-cols-2">
        <Card>
          <CardHeader>
            <CardTitle>Topic mastery</CardTitle>
          </CardHeader>
          <CardContent>
            {summary.topics.length === 0 ? (
              <EmptyState
                title="No topic data yet"
                description="Answer a practice set and we'll break your accuracy down by topic."
                icon={<BarChart3 aria-hidden="true" className="size-6" />}
                action={
                  <Button asChild>
                    <Link href="/practice">Start practising</Link>
                  </Button>
                }
              />
            ) : (
              <ul className="divide-y divide-border">
                {summary.topics.map((topic) => (
                  <li key={topic.topicId} className="flex items-center justify-between gap-3 py-2.5 first:pt-0 last:pb-0">
                    <div className="min-w-0">
                      <p className="truncate text-sm font-medium">{topic.topicName}</p>
                      <p className="text-xs text-muted-foreground">
                        {topic.subjectName} · {topic.attempts} answered
                      </p>
                    </div>
                    <Badge
                      variant={
                        topic.trend === "strong" ? "success" : topic.trend === "steady" ? "default" : topic.trend === "new" ? "outline" : "destructive"
                      }
                    >
                      {topic.accuracy}%
                    </Badge>
                  </li>
                ))}
              </ul>
            )}
          </CardContent>
        </Card>

        <Card>
          <CardHeader>
            <CardTitle>Attempt history</CardTitle>
          </CardHeader>
          <CardContent>
            {summary.attempts.length === 0 ? (
              <EmptyState
                title="Nothing submitted yet"
                description="Your first completed set will appear here with its score."
                icon={<TrendingUp aria-hidden="true" className="size-6" />}
              />
            ) : (
              <ul className="divide-y divide-border">
                {summary.attempts.map((attempt) => (
                  <li key={attempt.id} className="flex items-center justify-between gap-3 py-2.5 first:pt-0 last:pb-0">
                    <div className="min-w-0">
                      <p className="truncate text-sm font-medium">{attempt.title}</p>
                      <p className="text-xs text-muted-foreground">
                        {attempt.status} · {formatRelativeDate(attempt.submittedAt ?? attempt.startedAt)}
                      </p>
                    </div>
                    <Button asChild variant="ghost" size="sm">
                      <Link href={attempt.status === "in_progress" ? `/attempt/${attempt.id}` : `/results/${attempt.id}`}>
                        {formatPercent(attempt.scorePercent)}
                      </Link>
                    </Button>
                  </li>
                ))}
              </ul>
            )}
          </CardContent>
        </Card>
      </div>
    </div>
  );
}

function Metric({ label, value }: { label: string; value: string }) {
  return (
    <Card>
      <CardContent className="p-4">
        <p className="text-xs uppercase tracking-wide text-muted-foreground">{label}</p>
        <p className="font-serif text-xl">{value}</p>
      </CardContent>
    </Card>
  );
}
