import { redirect } from "next/navigation";

import { SIGNUP_STEPS } from "@edumart/shared";

import { SignupStepper } from "@/components/signup/stepper";
import { serverFetch } from "@/lib/server-api";
import { readDraft } from "@/lib/signup-draft";
import { draftToForm } from "@/lib/signup-types";
import { SubjectsForm } from "./subjects-form";

interface SubjectsResponse {
  subjects: { id: string; key: string; name: string; icon: string; pathwayKey: string; questionCount: number }[];
}

export default async function SubjectsStep() {
  const draft = await readDraft();
  if (!draft.draft) redirect("/register");

  const pathwayKey = draft.draft.examPathwayKey;
  const query = pathwayKey ? `?pathwayKey=${encodeURIComponent(pathwayKey)}` : "";
  const { subjects } = await serverFetch<SubjectsResponse>(`/v1/subjects${query}`);
  const step = SIGNUP_STEPS[1]!;

  return (
    <div>
      <SignupStepper current="subjects" />
      <h1 className="mb-1 text-2xl">{step.title}</h1>
      <p className="mb-6 text-sm text-muted-foreground">{step.description}</p>
      <SubjectsForm
        subjects={subjects.length > 0 ? subjects : (await serverFetch<SubjectsResponse>("/v1/subjects")).subjects}
        draft={draftToForm(draft.draft)}
      />
    </div>
  );
}
