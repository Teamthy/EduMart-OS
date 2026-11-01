import type { Metadata } from "next";
import Link from "next/link";
import { CalendarClock, Lock, Shuffle, Timer, Users } from "lucide-react";

import { formatDateTimeSafe } from "@edumart/shared";

import { Alert } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { ServerApiError, serverFetch } from "@/lib/server-api";
import { getCurrentUser } from "@/lib/session";
import type { Exam } from "@/lib/live-api";
import { ExamResults, ExamRoom } from "./room";

export const metadata: Metadata = { title: "Sitting" };
export const dynamic = "force-dynamic";

/**
 * The landing page for a shared exam link.
 *
 * Like a class invite it is not behind the authenticated shell: a link that demands an account before saying
 * what it is for is a link nobody opens twice. What it will not do is leak the paper — the questions, their
 * answers and their explanations all stay behind the start endpoint, where the attempt and the deadline exist.
 */
export default async function ExamPage({ params }: { params: Promise<{ slug: string }> }) {
  const { slug } = await params;

  let invite: Exam;
  try {
    const { exam } = await serverFetch<{ exam: Exam }>(`/v1/exams/link/${encodeURIComponent(slug)}`);
    invite = exam;
  } catch (error) {
    if (error instanceof ServerApiError && error.isNotFound) {
      return (
        <main className="mx-auto max-w-2xl p-6">
          <Alert tone="error" title="That exam link does not exist">
            The sitting may have been removed, or the code was copied with a character missing.
          </Alert>
        </main>
      );
    }
    throw error;
  }

  const user = await getCurrentUser();
  let mine: Exam | null = null;
  if (user) {
    try {
      const { exam } = await serverFetch<{ exam: Exam }>(`/v1/exams/${encodeURIComponent(invite.id)}`);
      mine = exam;
    } catch {
      mine = null;
    }
  }

  return (
    <main className="mx-auto max-w-3xl space-y-4 p-4 sm:p-6">
      <header className="space-y-2">
        <div className="flex flex-wrap items-center gap-2">
          <Badge variant={invite.open ? "success" : "outline"}>{invite.open ? "open now" : "not open"}</Badge>
          <Badge variant="outline">
            <Timer aria-hidden="true" className="mr-1 size-3" />
            {invite.durationMinutes} minutes
          </Badge>
          <Badge variant="outline">
            <Users aria-hidden="true" className="mr-1 size-3" />
            {invite.questionCount} questions
          </Badge>
          {invite.shuffled ? (
            <Badge variant="outline">
              <Shuffle aria-hidden="true" className="mr-1 size-3" />
              shuffled per candidate
            </Badge>
          ) : null}
        </div>
        <h1 className="text-2xl">{invite.title}</h1>
        <p className="flex flex-wrap items-center gap-1.5 text-sm text-muted-foreground">
          <CalendarClock aria-hidden="true" className="size-3" />
          {formatDateTimeSafe(invite.startsAt)} → {formatDateTimeSafe(invite.endsAt)} · set by {invite.hostName}
        </p>
      </header>

      {mine ? (
        mine.role === "host" ? (
          <ExamResults exam={mine} />
        ) : (
          <ExamRoom exam={mine} />
        )
      ) : (
        <Card>
          <CardHeader>
            <CardTitle>Sign in to sit this exam</CardTitle>
          </CardHeader>
          <CardContent className="space-y-3">
            <p className="text-sm text-muted-foreground">
              The sitting is one paper for everybody: the same questions at the same time, in your own order, for
              the same length. Your attempt, its deadline and its score are recorded on the server.
            </p>
            <div className="flex flex-wrap gap-2">
              <Button asChild>
                <Link href={`/login?next=${encodeURIComponent(`/exams/${invite.slug}`)}`}>Sign in</Link>
              </Button>
              <Button asChild variant="outline">
                <Link href={`/register?next=${encodeURIComponent(`/exams/${invite.slug}`)}`}>Create an account</Link>
              </Button>
            </div>
            <p className="flex items-center gap-1.5 text-xs text-muted-foreground">
              <Lock aria-hidden="true" className="size-3" />
              Answer keys never leave the server before the release policy allows.
            </p>
          </CardContent>
        </Card>
      )}
    </main>
  );
}
