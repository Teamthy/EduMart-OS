import type { AccountType, SignupDraft } from "@edumart/shared";

/** Server representation of the wizard answers (subset that matters pre-account). */
export type Draft = {
  accountType?: AccountType;
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
    accountType: draft?.accountType ?? "student",
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

/**
 * Combines the answers posted by the current step with the answers already saved on the server.
 *
 * A wizard step only renders its own fields, so trusting the form alone would silently erase the
 * earlier pages — for example the account step would register without the pathway and subjects.
 * Empty values never overwrite saved ones, and the subject checkbox group is treated as authoritative
 * only when the form says it was rendered (so unticking everything is still possible).
 */
export function mergeDraft(
  saved: SignupDraft,
  posted: Partial<SignupDraft>,
  subjectsRendered: boolean,
): SignupDraft {
  const merged = { ...saved };
  for (const [key, value] of Object.entries(posted)) {
    if (key === "subjectIds") continue;
    if (value === "" || value === undefined || value === null) continue;
    (merged as Record<string, unknown>)[key] = value;
  }
  if (subjectsRendered) {
    merged.subjectIds = posted.subjectIds ?? [];
  }
  return merged;
}
