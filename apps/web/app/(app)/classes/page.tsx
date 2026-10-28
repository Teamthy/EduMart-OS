import type { Metadata } from "next";
import Link from "next/link";
import { Users } from "lucide-react";

import type { ClassSummary } from "@edumart/shared";

import { Alert } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent } from "@/components/ui/card";
import { EmptyState } from "@/components/ui/empty-state";
import { JoinClassForm } from "./join-form";
import { ServerApiError, serverFetch } from "@/lib/server-api";
import { requireUser } from "@/lib/session";

export const metadata: Metadata = { title: "Classes" };
export const dynamic = "force-dynamic";

export default async function ClassesPage() {
  await requireUser("/classes");

  let classes: ClassSummary[] = [];
  try {
    ({ classes } = await serverFetch<{ classes: ClassSummary[] }>("/v1/classes"));
  } catch (error) {
    if (!(error instanceof ServerApiError)) throw error;
    return <Alert tone="error" title="We couldn't load your classes">{error.message}</Alert>;
  }

  return (
    <div className="space-y-6">
      <header className="space-y-1">
        <h1 className="text-2xl">Classes</h1>
        <p className="text-sm text-muted-foreground">
          Assignments set by your teachers appear here. Ask your teacher for the class code.
        </p>
      </header>

      <Card>
        <CardContent className="p-4">
          <JoinClassForm />
        </CardContent>
      </Card>

      {classes.length === 0 ? (
        <EmptyState
          title="You're not in a class yet"
          description="Join with a six-character code from your teacher, and their assignments will show up here."
          icon={<Users aria-hidden="true" className="size-6" />}
        />
      ) : (
        <ul className="space-y-3">
          {classes.map((item) => (
            <li key={item.id}>
              <Card>
                <CardContent className="flex flex-wrap items-center justify-between gap-3 p-4">
                  <div className="min-w-0">
                    <p className="truncate font-semibold">{item.name}</p>
                    <p className="text-xs text-muted-foreground">
                      {item.members} member{item.members === 1 ? "" : "s"} · {item.assignments} assignment
                      {item.assignments === 1 ? "" : "s"}
                    </p>
                  </div>
                  <div className="flex items-center gap-2">
                    <Badge variant="outline">{item.role}</Badge>
                    <Button asChild variant="secondary" size="sm">
                      <Link href={`/classes/${item.id}`}>Open</Link>
                    </Button>
                  </div>
                </CardContent>
              </Card>
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}
