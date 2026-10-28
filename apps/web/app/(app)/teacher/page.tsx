import type { Metadata } from "next";
import Link from "next/link";
import { GraduationCap, Plus } from "lucide-react";

import { formatPercent, type ClassSummary } from "@edumart/shared";

import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { EmptyState } from "@/components/ui/empty-state";
import { CreateClassForm } from "./create-class";
import { ServerApiError, serverFetch } from "@/lib/server-api";
import { requireRole } from "@/lib/session";

export const metadata: Metadata = { title: "Teaching" };
export const dynamic = "force-dynamic";

export default async function TeacherPage() {
  await requireRole(["teacher", "org_admin"], "/teacher");

  let classes: ClassSummary[] = [];
  try {
    ({ classes } = await serverFetch<{ classes: ClassSummary[] }>("/v1/classes"));
  } catch (error) {
    if (!(error instanceof ServerApiError)) throw error;
  }

  const teaching = classes.filter((item) => item.role !== "student");

  return (
    <div className="space-y-6">
      <header className="space-y-1">
        <h1 className="text-2xl">Teaching</h1>
        <p className="text-sm text-muted-foreground">
          Create a class, share its code, and publish timed assessments. Rosters and submission
          counts are visible to you only.
        </p>
      </header>

      <Card>
        <CardHeader className="flex-row items-center justify-between">
          <CardTitle className="flex items-center gap-2">
            <Plus aria-hidden="true" className="size-4" /> New class
          </CardTitle>
        </CardHeader>
        <CardContent>
          <CreateClassForm />
        </CardContent>
      </Card>

      <section className="space-y-3">
        <h2 className="font-serif text-lg">Classes you teach</h2>
        {teaching.length === 0 ? (
          <EmptyState
            title="No classes yet"
            description="Create a class above to get a join code you can share with learners."
            icon={<GraduationCap aria-hidden="true" className="size-6" />}
          />
        ) : (
          <ul className="space-y-3">
            {teaching.map((item) => (
              <li key={item.id}>
                <Card>
                  <CardContent className="flex flex-wrap items-center justify-between gap-3 p-4">
                    <div>
                      <p className="font-semibold">{item.name}</p>
                      <p className="text-xs text-muted-foreground">
                        Join code <span className="font-mono tracking-widest">{item.joinCode}</span> ·{" "}
                        {item.members} member{item.members === 1 ? "" : "s"} · {item.assignments} published
                      </p>
                    </div>
                    <div className="flex items-center gap-2">
                      <Badge variant="outline">{formatPercent(null)} completion data</Badge>
                      <Button asChild size="sm" variant="secondary">
                        <Link href={`/classes/${item.id}`}>Open class</Link>
                      </Button>
                    </div>
                  </CardContent>
                </Card>
              </li>
            ))}
          </ul>
        )}
      </section>
    </div>
  );
}
