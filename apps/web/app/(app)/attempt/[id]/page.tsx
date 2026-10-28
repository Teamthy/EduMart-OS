import type { Metadata } from "next";
import { notFound, redirect } from "next/navigation";

import type { AssessmentSessionView } from "@edumart/shared";

import { AttemptRunner } from "@/components/attempt/attempt-runner";
import { Alert } from "@/components/ui/alert";
import { ServerApiError, serverFetch } from "@/lib/server-api";
import { requireUser } from "@/lib/session";

export const metadata: Metadata = { title: "Attempt" };
export const dynamic = "force-dynamic";

export default async function AttemptPage({ params }: { params: Promise<{ id: string }> }) {
  const user = await requireUser();
  const { id } = await params;

  let session: AssessmentSessionView;
  try {
    ({ session } = await serverFetch<{ session: AssessmentSessionView }>(`/v1/assessment-sessions/${id}`));
  } catch (error) {
    if (error instanceof ServerApiError && error.isNotFound) notFound();
    if (error instanceof ServerApiError && error.isForbidden) redirect("/permission-denied");
    return (
      <Alert tone="error" title="We couldn't open this attempt">
        {error instanceof ServerApiError ? error.message : "Please try again."}
      </Alert>
    );
  }

  if (session.status !== "in_progress") {
    // A closed attempt is a result, never a runner.
    redirect(`/results/${session.session_id}`);
  }

  return (
    <>
      <p className="mb-3 text-xs text-muted-foreground">
        Signed in as {user.displayName}. Attempt #{session.attempt_no}.
      </p>
      <AttemptRunner session={session} />
    </>
  );
}
