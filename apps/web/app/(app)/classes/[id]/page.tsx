import type { Metadata } from "next";
import Link from "next/link";
import { notFound, redirect } from "next/navigation";
import { CalendarClock, Clock } from "lucide-react";

import { formatDate, type Assignment, type ClassMember } from "@edumart/shared";

import { Alert } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { EmptyState } from "@/components/ui/empty-state";
import { ServerApiError, serverFetch } from "@/lib/server-api";
import { requireUser } from "@/lib/session";
import { StartAssignment } from "./start-assignment";

export const metadata: Metadata = { title: "Class" };
export const dynamic = "force-dynamic";

export default async function ClassPage({ params }: { params: Promise<{ id: string }> }) {
  const user = await requireUser();
  const { id } = await params;

  let assignments: Assignment[] = [];
  let members: ClassMember[] | null = null;

  try {
    ({ assignments } = await serverFetch<{ assignments: Assignment[] }>(`/v1/classes/${id}/assignments`));
    // The roster is teacher-only server-side; a 403 here is expected for learners.
    try {
      ({ members } = await serverFetch<{ members: ClassMember[] }>(`/v1/classes/${id}/roster`));
    } catch (error) {
      if (!(error instanceof ServerApiError) || !error.isForbidden) throw error;
    }
  } catch (error) {
    if (error instanceof ServerApiError && error.isNotFound) notFound();
    if (error instanceof ServerApiError && error.isForbidden) redirect("/permission-denied");
    throw error;
  }

  return (
    <div className="space-y-6">
      <nav aria-label="Breadcrumb" className="text-sm text-muted-foreground">
        <Link href="/classes" className="hover:underline">
          Classes
        </Link>{" "}
        / <span aria-current="page">Assignments</span>
      </nav>

      <header>
        <h1 className="text-2xl">Assignments</h1>
        <p className="text-sm text-muted-foreground">
          Timed assessments set by your teacher. Each one starts its own server clock.
        </p>
      </header>

      {assignments.length === 0 ? (
        <EmptyState
          title="No assignments yet"
          description="When your teacher publishes one, it will appear here with its deadline."
          icon={<CalendarClock aria-hidden="true" className="size-6" />}
        />
      ) : (
        <ul className="space-y-3">
          {assignments.map((assignment) => (
            <li key={assignment.definitionId}>
              <Card>
                <CardHeader className="flex-row items-start justify-between gap-3 pb-2">
                  <div>
                    <CardTitle className="text-base">{assignment.title}</CardTitle>
                    <p className="text-xs text-muted-foreground">
                      <Clock aria-hidden="true" className="mr-1 inline size-3" />
                      {Math.round(assignment.durationSeconds / 60)} minutes · due {formatDate(assignment.dueAt)}
                    </p>
                  </div>
                  <Badge variant={assignment.myStatus === "submitted" ? "success" : assignment.myStatus === "in_progress" ? "warning" : "outline"}>
                    {assignment.myStatus === "submitted" ? "Completed" : assignment.myStatus === "in_progress" ? "In progress" : "Not started"}
                  </Badge>
                </CardHeader>
                <CardContent className="flex flex-wrap items-center justify-between gap-3">
                  <p className="text-xs text-muted-foreground">
                    {assignment.submissions}/{assignment.roster} learners have submitted
                  </p>
                  <StartAssignment
                    definitionId={assignment.definitionId}
                    label={assignment.myStatus === "submitted" ? "Start another attempt" : "Start assessment"}
                  />
                </CardContent>
              </Card>
            </li>
          ))}
        </ul>
      )}

      {members ? (
        <Card>
          <CardHeader>
            <CardTitle>Roster ({members.length})</CardTitle>
          </CardHeader>
          <CardContent>
            <ul className="divide-y divide-border">
              {members.map((member) => (
                <li key={member.userId} className="flex items-center justify-between gap-3 py-2.5 first:pt-0 last:pb-0">
                  <div className="min-w-0">
                    <p className="truncate text-sm font-medium">{member.displayName}</p>
                    <p className="truncate text-xs text-muted-foreground">{member.email}</p>
                  </div>
                  <div className="flex shrink-0 items-center gap-2">
                    <span className="text-xs text-muted-foreground">
                      {member.attempts} attempt{member.attempts === 1 ? "" : "s"}
                    </span>
                    <Badge variant="outline">{member.role}</Badge>
                  </div>
                </li>
              ))}
            </ul>
          </CardContent>
        </Card>
      ) : null}

      {user.roles.includes("teacher") ? (
        <Alert tone="info" title="Teacher view">
          You can see the roster and submission counts. Learners only ever see their own attempt.
        </Alert>
      ) : null}
    </div>
  );
}
