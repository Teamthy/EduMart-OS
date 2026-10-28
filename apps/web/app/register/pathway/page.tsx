import { redirect } from "next/navigation";

import { SIGNUP_STEPS } from "@edumart/shared";

import { SignupStepper } from "@/components/signup/stepper";
import { serverFetch } from "@/lib/server-api";
import { readDraft } from "@/lib/signup-draft";
import { draftToForm } from "@/lib/signup-types";
import { PathwayForm } from "./pathway-form";

interface PathwaysResponse {
  pathways: { id: string; key: string; name: string; body: string; blurb: string; subjects: number }[];
}

export default async function PathwayStep() {
  const draft = await readDraft();
  if (!draft.draft) redirect("/register");

  const { pathways } = await serverFetch<PathwaysResponse>("/v1/exam-pathways");
  const step = SIGNUP_STEPS[0]!;

  return (
    <div>
      <SignupStepper current="pathway" />
      <h1 className="mb-1 text-2xl">{step.title}</h1>
      <p className="mb-6 text-sm text-muted-foreground">{step.description}</p>
      <PathwayForm pathways={pathways} draft={draftToForm(draft.draft)} />
    </div>
  );
}
