import { redirect } from "next/navigation";

import { SIGNUP_STEPS } from "@edumart/shared";

import { SignupStepper } from "@/components/signup/stepper";
import { readDraft } from "@/lib/signup-draft";
import { draftToForm } from "@/lib/signup-types";
import { ScheduleForm } from "./schedule-form";

export default async function ScheduleStep() {
  const draft = await readDraft();
  if (!draft.draft) redirect("/register");
  const step = SIGNUP_STEPS[2]!;

  return (
    <div>
      <SignupStepper current="schedule" />
      <h1 className="mb-1 text-2xl">{step.title}</h1>
      <p className="mb-6 text-sm text-muted-foreground">{step.description}</p>
      <ScheduleForm draft={draftToForm(draft.draft)} />
    </div>
  );
}
