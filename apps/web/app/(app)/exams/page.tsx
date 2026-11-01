import type { Metadata } from "next";
import Link from "next/link";
import { ClipboardList, Shuffle, Timer, Users } from "lucide-react";

import { formatDateTimeSafe } from "@edumart/shared";

import { Alert } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { EmptyState } from "@/components/ui/empty-state";
import { ServerApiError, serverFetch } from "@/lib/server-api";
import { requireUser } from "@/lib/session";
import type { Exam } from "@/lib/live-api";
import { ExamForm } from "./exam-form";

export const metadata: Metadata = { title: "Exams" };
export const dynamic = "force-dynamic";

export default async function ExamsPage() {
  await requireUser("/exams");

  let exams: Exam[] = [];
  try {
    ({ exams } = await serverFetch<{ exams: Exam[] }>("/v1/exams"));
  } catch (error) {
    if (!(error instanceof ServerApiError)) throw error;
    return <Alert tone="error" title="We couldn't load your exams">{error.message}</Alert>;
  }

  return (
    <div className="space-y-6">
      <header className="space-y-1">
        <h1 className="text-2xl">Synchronised exams</h1>
        <p className="text-sm text-muted-foreground">
          One paper, one window. Everybody starts inside the same period, gets the same questions in their own
          order and the same length, and the server records when each attempt started and was submitted.
        </p>
      </header>

      <Card>
        <CardHeader>
          <CardTitle>Schedule a sitting</CardTitle>
        </CardHeader>
        <CardContent>
          <ExamForm />
        </CardContent>
      </Card>

      {exams.length === 0 ? (
        <EmptyState
          title="No sittings yet"
          description="Schedule one above and you get a unique link. Candidates open it, sign in, and sit the paper."
          icon={<ClipboardList aria-hidden="true" className="size-6" />}
        />
      ) : (
        <ul className="space-y-3">
          {exams.map((exam) => (
            <li key={exam.id}>
              <Card>
                <CardContent className="flex flex-wrap items-center justify-between gap-3 p-4">
                  <div className="min-w-0">
                    <div className="flex flex-wrap items-center gap-2">
                      <p className="truncate font-semibold">{exam.title}</p>
                      {exam.role === "host" ? <Badge variant="default">you host</Badge> : null}
                      <Badge variant={exam.open ? "success" : "outline"}>{exam.open ? "open" : "closed"}</Badge>
                      <Badge variant="outline">
                        <Timer aria-hidden="true" className="mr-1 size-3" />
                        {exam.durationMinutes} min
                      </Badge>
                      <Badge variant="outline">
                        <Users aria-hidden="true" className="mr-1 size-3" />
                        {exam.candidates} sat
                      </Badge>
                      {exam.shuffled ? (
                        <Badge variant="outline">
                          <Shuffle aria-hidden="true" className="mr-1 size-3" />
                          shuffled
                        </Badge>
                      ) : null}
                    </div>
                    <p className="text-xs text-muted-foreground">
                      {formatDateTimeSafe(exam.startsAt)} → {formatDateTimeSafe(exam.endsAt)} · {exam.questionCount}{" "}
                      questions · host {exam.hostName}
                    </p>
                    <p className="truncate font-mono text-xs text-muted-foreground">{exam.url}</p>
                  </div>
                  <Button asChild size="sm" variant="outline">
                    <Link href={`/exams/${exam.slug}`}>{exam.role === "host" ? "Open report" : "Open sitting"}</Link>
                  </Button>
                </CardContent>
              </Card>
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}
