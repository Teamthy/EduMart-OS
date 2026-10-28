import { redirect } from "next/navigation";

import { SIGNUP_STEPS, formatDate } from "@edumart/shared";

import { SignupStepper } from "@/components/signup/stepper";
import { Card, CardContent } from "@/components/ui/card";
import { serverFetch } from "@/lib/server-api";
import { readDraft } from "@/lib/signup-draft";
import { draftToForm } from "@/lib/signup-types";
import { AccountForm } from "./account-form";

interface SubjectsResponse {
  subjects: { id: string; name: string }[];
}

export default async function AccountStep({ searchParams }: { searchParams: Promise<{ ref?: string }> }) {
  const [draft, params] = await Promise.all([readDraft(), searchParams]);
  if (!draft.draft) redirect("/register");

  const { subjects } = await serverFetch<SubjectsResponse>("/v1/subjects");
  const names = subjects.filter((subject) => (draft.draft?.subjectIds ?? []).includes(subject.id)).map((subject) => subject.name);
  const step = SIGNUP_STEPS[3]!;
  const form = draftToForm(draft.draft);
  if (!form.referralCode && params.ref) form.referralCode = params.ref.toUpperCase();

  return (
    <div>
      <SignupStepper current="account" />
      <h1 className="mb-1 text-2xl">{step.title}</h1>
      <p className="mb-6 text-sm text-muted-foreground">{step.description}</p>

      <Card className="mb-6">
        <CardContent className="space-y-1 p-4 text-sm">
          <p className="text-xs font-semibold uppercase tracking-wide text-muted-foreground">Your plan so far</p>
          <p>
            <strong>{names.length > 0 ? names.join(", ") : "No subjects yet"}</strong>
          </p>
          <p className="text-muted-foreground">
            Exam on {form.targetDate ? formatDate(form.targetDate) : "a date you can set later"} ·{" "}
            {form.dailyMinutes} minutes a day
          </p>
        </CardContent>
      </Card>

      <AccountForm draft={form} />
    </div>
  );
}
