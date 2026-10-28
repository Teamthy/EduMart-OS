import type { Metadata } from "next";
import { AlertTriangle, Banknote, BookCheck, Users } from "lucide-react";

import { formatMoney, formatRelativeDate, type PlatformMetrics, type ReviewQueueItem, type SupportCase } from "@edumart/shared";

import { Alert } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { EmptyState } from "@/components/ui/empty-state";
import { ReviewActions } from "./review-actions";
import { CaseActions } from "./case-actions";
import { ServerApiError, serverFetch } from "@/lib/server-api";
import { requireRole } from "@/lib/session";

export const metadata: Metadata = { title: "Operations" };
export const dynamic = "force-dynamic";

export default async function AdminPage() {
  const user = await requireRole(["content_editor", "platform_admin", "support", "finance"], "/admin");

  const canReview = user.roles.some((role) => role === "content_editor" || role === "platform_admin");
  const canSeeMetrics = user.roles.some((role) => role === "platform_admin" || role === "finance");
  const canSeeCases = user.roles.some((role) => role === "support" || role === "platform_admin");

  const errors: string[] = [];
  let metrics: PlatformMetrics | null = null;
  let queue: ReviewQueueItem[] = [];
  let cases: SupportCase[] = [];

  if (canSeeMetrics) {
    try {
      ({ metrics } = await serverFetch<{ metrics: PlatformMetrics }>("/v1/admin/metrics"));
    } catch (error) {
      if (error instanceof ServerApiError) errors.push(error.message);
      else throw error;
    }
  }
  if (canReview) {
    try {
      ({ items: queue } = await serverFetch<{ items: ReviewQueueItem[] }>("/v1/admin/review-queue"));
    } catch (error) {
      if (error instanceof ServerApiError) errors.push(error.message);
      else throw error;
    }
  }
  if (canSeeCases) {
    try {
      ({ cases } = await serverFetch<{ cases: SupportCase[] }>("/v1/admin/cases?status=open"));
    } catch (error) {
      if (error instanceof ServerApiError) errors.push(error.message);
      else throw error;
    }
  }

  return (
    <div className="space-y-6">
      <header className="space-y-1">
        <h1 className="text-2xl">Operations</h1>
        <p className="text-sm text-muted-foreground">
          Content review, payment health and learner support. Every action here is audited with your
          user id and the reason you give.
        </p>
      </header>

      {errors.map((message) => (
        <Alert key={message} tone="warning" title="Some panels could not load">
          {message}
        </Alert>
      ))}

      {metrics ? (
        <section className="grid grid-cols-2 gap-3 md:grid-cols-4">
          <Metric icon={Users} label="Active learners (7d)" value={String(metrics.activeLearners)} hint={`${metrics.users} accounts`} />
          <Metric icon={BookCheck} label="Attempts today" value={String(metrics.attemptsToday)} hint={`${metrics.attempts} all time`} />
          <Metric icon={Banknote} label="Revenue" value={formatMoney(metrics.revenueMinor, metrics.currency)} hint={`${metrics.paidOrders} paid orders`} />
          <Metric icon={AlertTriangle} label="Awaiting review" value={String(metrics.pendingReview)} hint={`${metrics.publishedQuestions} published`} />
        </section>
      ) : null}

      {canReview ? (
        <Card>
          <CardHeader>
            <CardTitle>Content review queue</CardTitle>
          </CardHeader>
          <CardContent>
            {queue.length === 0 ? (
              <EmptyState title="Queue is clear" description="Every submitted question version has been reviewed." />
            ) : (
              <ul className="space-y-3">
                {queue.map((item) => (
                  <li key={item.qvId} className="rounded-lg border border-border p-3">
                    <div className="mb-2 flex flex-wrap items-center gap-2 text-xs text-muted-foreground">
                      <Badge variant="warning">{item.reviewState.replace("_", " ")}</Badge>
                      <span>{item.subjectName}</span>
                      {item.topicName ? <span>· {item.topicName}</span> : null}
                      <span>· v{item.version}</span>
                      <span>· {item.difficulty}</span>
                    </div>
                    <p className="mb-2 text-sm">{item.stem}</p>
                    <p className="mb-2 text-xs text-muted-foreground">
                      Source: {item.sourceName || "—"} · licence: {item.licenseBasis || "—"}
                      {item.attribution ? ` · ${item.attribution}` : ""}
                    </p>
                    <ReviewActions qvId={item.qvId} />
                  </li>
                ))}
              </ul>
            )}
          </CardContent>
        </Card>
      ) : null}

      {canSeeCases ? (
        <Card>
          <CardHeader>
            <CardTitle>Open support cases</CardTitle>
          </CardHeader>
          <CardContent>
            {cases.length === 0 ? (
              <EmptyState title="No open cases" description="Learner requests will appear here." />
            ) : (
              <ul className="divide-y divide-border">
                {cases.map((supportCase) => (
                  <li key={supportCase.id} className="flex flex-wrap items-center justify-between gap-2 py-2.5 first:pt-0 last:pb-0">
                    <div className="min-w-0">
                      <p className="truncate text-sm font-medium">{supportCase.subject}</p>
                      <p className="text-xs text-muted-foreground">
                        {supportCase.category} · {formatRelativeDate(supportCase.createdAt)}
                      </p>
                    </div>
                    <div className="flex items-center gap-2">
                      <Badge variant="outline">{supportCase.priority}</Badge>
                      <CaseActions caseId={supportCase.id} />
                    </div>
                  </li>
                ))}
              </ul>
            )}
          </CardContent>
        </Card>
      ) : null}
    </div>
  );
}

function Metric({
  icon: Icon,
  label,
  value,
  hint,
}: {
  icon: typeof Users;
  label: string;
  value: string;
  hint: string;
}) {
  return (
    <Card>
      <CardContent className="p-4">
        <Icon aria-hidden="true" className="mb-1 size-4 text-primary" />
        <p className="text-xs uppercase tracking-wide text-muted-foreground">{label}</p>
        <p className="font-serif text-xl">{value}</p>
        <p className="text-xs text-muted-foreground">{hint}</p>
      </CardContent>
    </Card>
  );
}
