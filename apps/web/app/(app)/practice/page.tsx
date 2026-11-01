import type { Metadata } from "next";
import Link from "next/link";
import { AlertTriangle, ArrowRight, Sparkles } from "lucide-react";

import { formatPercent, type ClassSummary, type Dashboard, type Pathway, type Subject } from "@edumart/shared";

import { Alert } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { EmptyState } from "@/components/ui/empty-state";
import { ServerApiError, serverFetch } from "@/lib/server-api";
import { requireUser } from "@/lib/session";
import { QuickStart } from "./quick-start";

export const metadata: Metadata = { title: "Practice" };
export const dynamic = "force-dynamic";

export default async function PracticePage() {
  const user = await requireUser("/practice");

  let subjects: Subject[] = [];
  let dashboard: Dashboard | null = null;
  let pathways: Pathway[] = [];
  let classes: ClassSummary[] = [];

  try {
    const [subjectResponse, dashboardResponse, pathwayResponse, classResponse] = await Promise.all([
      serverFetch<{ subjects: Subject[] }>("/v1/subjects"),
      serverFetch<{ dashboard: Dashboard }>("/v1/dashboard"),
      serverFetch<{ pathways: Pathway[] }>("/v1/exam-pathways"),
      serverFetch<{ classes: ClassSummary[] }>("/v1/classes"),
    ]);
    subjects = subjectResponse.subjects;
    dashboard = dashboardResponse.dashboard;
    pathways = pathwayResponse.pathways;
    classes = classResponse.classes;
  } catch (error) {
    return (
      <Alert tone="error" title="Practice is unavailable right now">
        {error instanceof ServerApiError ? error.message : "Please try again shortly."}
      </Alert>
    );
  }

  // Only subjects the learner chose are offered first; the rest stay available below.
  const preferred = subjects.filter((subject) => user.subjectIds.includes(subject.id));
  const others = subjects.filter((subject) => !user.subjectIds.includes(subject.id));
  const focus = dashboard.weakTopics.slice(0, 3);

  return (
    <div className="space-y-6">
      <header className="space-y-1">
        <h1 className="text-2xl">Practice</h1>
        <p className="text-sm text-muted-foreground">
          Untimed, 10 questions by default. Every answer saves as you tap it, and the answer key is
          shown only after you finish.
        </p>
      </header>

      <QuickStart
        pathways={pathways.filter((pathway) => pathway.subjects > 0)}
        classes={classes.map((room) => ({ id: room.id, name: room.name }))}
      />

      {focus.length > 0 ? (
        <Card>
          <CardHeader>
            <CardTitle className="flex items-center gap-2">
              <AlertTriangle aria-hidden="true" className="size-4 text-warning" />
              Weakest topics
            </CardTitle>
          </CardHeader>
          <CardContent className="space-y-2">
            {focus.map((topic) => (
              <div key={topic.topicId} className="flex items-center justify-between gap-3 text-sm">
                <span className="truncate">
                  {topic.topicName} <span className="text-muted-foreground">· {topic.subjectName}</span>
                </span>
                <Badge variant={topic.accuracy < 50 ? "destructive" : "warning"}>{formatPercent(topic.accuracy)}</Badge>
              </div>
            ))}
            <p className="text-xs text-muted-foreground">
              Choose the subject below and pick these topics to focus the set.
            </p>
          </CardContent>
        </Card>
      ) : null}

      <section aria-labelledby="your-subjects" className="space-y-3">
        <h2 id="your-subjects" className="font-serif text-lg">
          Your subjects
        </h2>
        {preferred.length === 0 ? (
          <EmptyState
            title="No subjects selected yet"
            description="Add subjects in Settings, or start with any subject below."
            action={
              <Button asChild variant="outline">
                <Link href="/settings">Update subjects</Link>
              </Button>
            }
          />
        ) : (
          <div className="grid gap-3 sm:grid-cols-2">
            {preferred.map((subject) => (
              <SubjectCard key={subject.id} subject={subject} />
            ))}
          </div>
        )}
      </section>

      {others.length > 0 ? (
        <section aria-labelledby="other-subjects" className="space-y-3">
          <h2 id="other-subjects" className="font-serif text-lg">
            All subjects
          </h2>
          <div className="grid gap-3 sm:grid-cols-2">
            {others.map((subject) => (
              <SubjectCard key={subject.id} subject={subject} />
            ))}
          </div>
        </section>
      ) : null}
    </div>
  );
}

function SubjectCard({ subject }: { subject: Subject }) {
  const empty = subject.questionCount === 0;
  return (
    <Card>
      <CardContent className="flex items-center justify-between gap-3 p-4">
        <div className="min-w-0">
          <p className="truncate font-semibold">
            <span aria-hidden="true" className="mr-1.5">{subject.icon}</span>
            {subject.name}
          </p>
          <p className="text-xs text-muted-foreground">
            {empty ? "No reviewed questions yet" : `${subject.questionCount} reviewed questions`}
          </p>
        </div>
        {empty ? (
          <Badge variant="outline">Coming soon</Badge>
        ) : (
          <Button asChild size="sm">
            <Link href={`/practice/${subject.id}`}>
              Start <ArrowRight aria-hidden="true" />
            </Link>
          </Button>
        )}
      </CardContent>
    </Card>
  );
}

export type { Topic } from "@edumart/shared";
