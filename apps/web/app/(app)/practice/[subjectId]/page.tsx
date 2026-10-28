import type { Metadata } from "next";
import Link from "next/link";
import { notFound } from "next/navigation";

import { Subject, Topic } from "@edumart/shared";

import { Alert } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Card, CardContent } from "@/components/ui/card";
import { EmptyState } from "@/components/ui/empty-state";
import { ServerApiError, serverFetch } from "@/lib/server-api";
import { requireUser } from "@/lib/session";
import { StartPracticeForm } from "./start-form";

export const metadata: Metadata = { title: "Start practice" };
export const dynamic = "force-dynamic";

export default async function StartPracticePage({ params }: { params: Promise<{ subjectId: string }> }) {
  await requireUser();
  const { subjectId } = await params;

  let subject: Subject;
  let topics: Topic[];

  try {
    const response = await serverFetch<{ subject: Subject; topics: Topic[] }>(`/v1/subjects/${subjectId}/topics`);
    subject = response.subject;
    topics = response.topics;
  } catch (error) {
    if (error instanceof ServerApiError && error.isNotFound) notFound();
    return (
      <Alert tone="error" title="We couldn't load that subject">
        {error instanceof ServerApiError ? error.message : "Please try again."}
      </Alert>
    );
  }

  return (
    <div className="space-y-6">
      <nav aria-label="Breadcrumb" className="text-sm text-muted-foreground">
        <Link href="/practice" className="hover:underline">
          Practice
        </Link>{" "}
        / <span aria-current="page">{subject.name}</span>
      </nav>

      <header className="space-y-1">
        <h1 className="text-2xl">Practise {subject.name}</h1>
        <p className="text-sm text-muted-foreground">
          Choose how many questions and whether to focus on specific topics.
        </p>
      </header>

      {topics.length === 0 ? (
        <EmptyState
          title="No topics yet"
          description="This subject has no reviewed topics. Try another subject while we finish reviewing content."
          action={
            <Button asChild variant="outline">
              <Link href="/practice">Choose another subject</Link>
            </Button>
          }
        />
      ) : (
        <Card>
          <CardContent className="p-5">
            <StartPracticeForm subject={subject} topics={topics.filter((topic) => topic.questionCount > 0)} />
          </CardContent>
        </Card>
      )}
    </div>
  );
}
