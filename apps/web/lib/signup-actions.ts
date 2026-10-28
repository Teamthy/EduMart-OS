"use server";

import { cookies } from "next/headers";
import { redirect } from "next/navigation";

import { SIGNUP_STEPS, validateStep, type SignupDraft, type SignupStepId } from "@edumart/shared";

import { ServerApiError, serverFetch } from "./server-api";
import { createDraft, patchDraft, readDraft } from "./signup-draft";
import { draftToForm, mergeDraft } from "./signup-types";

import { DRAFT_COOKIE } from "./signup-cookie";

import type { StepState } from "./signup-state";

/**
 * The wizard entry point lives in `app/api/signup/start/route.ts`, not in a server action: starting
 * a draft must work with JavaScript disabled, and it has to set a cookie, which a GET render cannot
 * do. This keeps the first step a plain form POST.
 */

async function advance(step: SignupStepId, patch: Record<string, unknown>): Promise<StepState> {
  try {
    await patchDraft({ ...patch, currentStep: step });
    return { ok: true, errors: {} };
  } catch (error) {
    if (error instanceof ServerApiError) {
      const errors: Record<string, string> = {};
      if (Array.isArray(error.details)) {
        for (const detail of error.details as { field: string; message: string }[]) {
          if (detail.field) errors[detail.field] = detail.message;
        }
      }
      return { ok: false, errors, message: error.message, requestId: error.requestId };
    }
    return { ok: false, errors: {}, message: "Could not save your progress. Check your connection and try again." };
  }
}

export async function savePathway(_prev: StepState, formData: FormData): Promise<StepState> {
  const draft = await mergedDraft(formData);
  const errors = validateStep("pathway", draft);
  if (Object.keys(errors).length > 0) return { ok: false, errors: errors as Record<string, string> };

  const result = await advance("subjects", {
    examPathwayId: draft.examPathwayId,
    examPathwayKey: draft.examPathwayKey,
  });
  if (result.ok) redirect("/register/subjects");
  return result;
}

export async function saveSubjects(_prev: StepState, formData: FormData): Promise<StepState> {
  const draft = await mergedDraft(formData);
  const errors = validateStep("subjects", draft);
  if (Object.keys(errors).length > 0) return { ok: false, errors: errors as Record<string, string> };

  const result = await advance("schedule", { subjectIds: draft.subjectIds });
  if (result.ok) redirect("/register/schedule");
  return result;
}

export async function saveSchedule(_prev: StepState, formData: FormData): Promise<StepState> {
  const draft = await mergedDraft(formData);
  const errors = validateStep("schedule", draft);
  if (Object.keys(errors).length > 0) return { ok: false, errors: errors as Record<string, string> };

  const result = await advance("account", {
    targetDate: draft.targetDate,
    dailyMinutes: draft.dailyMinutes,
  });
  if (result.ok) redirect("/register/account");
  return result;
}

/**
 * The account step creates the user. The password is never written to the draft — it goes straight
 * to `POST /v1/auth/register` in the same request that finishes the wizard.
 */
export async function createAccount(_prev: StepState, formData: FormData): Promise<StepState> {
  const draft = await mergedDraft(formData);
  const errors = validateStep("account", draft);
  if (Object.keys(errors).length > 0) return { ok: false, errors: errors as Record<string, string> };

  const nameStep = await advance("confirm", {
    displayName: draft.displayName,
    email: draft.email,
    referralCode: draft.referralCode,
  });
  if (!nameStep.ok) return nameStep;

  try {
    const response = await serverFetch<{ token: string; user: { id: string }; next: string }>("/v1/auth/register", {
      method: "POST",
      body: {
        displayName: draft.displayName.trim(),
        email: draft.email.trim().toLowerCase(),
        password: draft.password,
        examPathwayId: draft.examPathwayId || null,
        targetDate: draft.targetDate || null,
        subjectIds: draft.subjectIds,
        dailyMinutes: draft.dailyMinutes,
        referralCode: draft.referralCode.trim().toUpperCase(),
        timezone: draft.timezone,
        client: "web",
        onboardingStep: SIGNUP_STEPS.length,
      },
    });

    const store = await cookies();
    store.set("edumart_session", response.token, {
      httpOnly: true,
      sameSite: "lax",
      path: "/",
      maxAge: 60 * 60 * 24 * 7,
      secure: process.env.NODE_ENV === "production",
    });
    // The draft cookie has served its purpose; the account now owns this progress.
    store.delete(DRAFT_COOKIE);
  } catch (error) {
    if (error instanceof ServerApiError) {
      const fieldErrors: Record<string, string> = {};
      if (Array.isArray(error.details)) {
        for (const detail of error.details as { field: string; message: string }[]) {
          if (detail.field) fieldErrors[detail.field] = detail.message;
        }
      }
      return { ok: false, errors: fieldErrors, message: error.message, requestId: error.requestId };
    }
    return { ok: false, errors: {}, message: "We couldn't create your account. Please try again." };
  }

  redirect("/register/ready");
}

/**
 * The form only carries the fields for the current step, so every action first re-reads the draft
 * from the API and merges the posted fields on top of it.
 */
async function mergedDraft(formData: FormData) {
  const persisted = await readDraft();
  return mergeDraft(
    draftToForm(persisted.draft),
    draftFromForm(formData),
    formData.has("subjectsTouched"),
  );
}

/**
 * Server actions receive the form fields as strings; this rebuilds the draft shape the shared
 * validators expect (checkbox groups arrive as repeated `subjectIds` entries).
 */
function draftFromForm(formData: FormData): SignupDraft {
  const raw = formData.get("draft");
  let base: SignupDraft = {
    examPathwayKey: "",
    examPathwayId: "",
    subjectIds: [],
    targetDate: "",
    dailyMinutes: 30,
    displayName: "",
    email: "",
    password: "",
    referralCode: "",
    timezone: "Africa/Lagos",
  };
  if (typeof raw === "string" && raw.length > 0) {
    try {
      base = { ...base, ...(JSON.parse(raw) as Partial<SignupDraft>) };
    } catch {
      // A malformed hidden field must not crash the wizard; fields below still apply.
    }
  }

  const value = (key: string) => {
    const entry = formData.get(key);
    return typeof entry === "string" ? entry : undefined;
  };

  return {
    ...base,
    examPathwayKey: value("examPathwayKey") ?? base.examPathwayKey,
    examPathwayId: value("examPathwayId") ?? base.examPathwayId,
    targetDate: value("targetDate") ?? base.targetDate,
    dailyMinutes: value("dailyMinutes") ? Number(value("dailyMinutes")) : base.dailyMinutes,
    displayName: value("displayName") ?? base.displayName,
    email: value("email") ?? base.email,
    password: value("password") ?? "",
    referralCode: value("referralCode") ?? base.referralCode,
    timezone: value("timezone") ?? base.timezone,
    subjectIds: formData.getAll("subjectIds").filter((entry): entry is string => typeof entry === "string"),
  };
}

/** Abandoning the wizard clears the draft cookie so a new attempt starts clean. */
export async function resetSignup(): Promise<void> {
  const store = await cookies();
  store.delete(DRAFT_COOKIE);
  redirect("/register/pathway");
}
