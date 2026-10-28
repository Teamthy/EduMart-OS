import type { SignupDraft } from "@edumart/shared";

/** Server representation of the wizard answers (subset that matters pre-account). */
export type Draft = {
  examPathwayId?: string;
  examPathwayKey?: string;
  subjectIds?: string[];
  targetDate?: string;
  dailyMinutes?: number;
  displayName?: string;
  email?: string;
  timezone?: string;
};

export function draftToForm(draft: Draft | null): SignupDraft {
  return {
    examPathwayKey: draft?.examPathwayKey ?? "",
    examPathwayId: draft?.examPathwayId ?? "",
    subjectIds: draft?.subjectIds ?? [],
    targetDate: draft?.targetDate ?? "",
    dailyMinutes: draft?.dailyMinutes ?? 30,
    displayName: draft?.displayName ?? "",
    email: draft?.email ?? "",
    password: "",
    referralCode: "",
    timezone: draft?.timezone ?? "Africa/Lagos",
  };
}
