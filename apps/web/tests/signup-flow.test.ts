import { describe, expect, it } from "vitest";

import {
  SIGNUP_STEPS,
  emptyDraft,
  isStepValid,
  nextStep,
  previousStep,
  studyLoadAdvice,
  toRegisterPayload,
  validateStep,
  type SignupDraft,
} from "@edumart/shared";

function draft(overrides: Partial<SignupDraft> = {}): SignupDraft {
  return { ...emptyDraft(), ...overrides };
}

describe("signup wizard definition", () => {
  it("has five ordered steps with unique ids", () => {
    expect(SIGNUP_STEPS).toHaveLength(5);
    expect(SIGNUP_STEPS.map((step) => step.id)).toEqual(["pathway", "subjects", "schedule", "account", "confirm"]);
    expect(SIGNUP_STEPS.map((step) => step.index)).toEqual([1, 2, 3, 4, 5]);
  });

  it("navigates forwards and backwards without leaving the flow", () => {
    expect(nextStep("pathway")).toBe("subjects");
    expect(nextStep("confirm")).toBeNull();
    expect(previousStep("pathway")).toBeNull();
    expect(previousStep("confirm")).toBe("account");
  });
});

describe("per-step validation", () => {
  it("requires an exam pathway on step 1", () => {
    expect(validateStep("pathway", draft()).examPathwayId).toBeDefined();
    expect(isStepValid("pathway", draft({ examPathwayId: "pw_1", examPathwayKey: "jamb" }))).toBe(true);
  });

  it("requires at least one subject on step 2", () => {
    expect(validateStep("subjects", draft()).subjectIds).toBeDefined();
    expect(isStepValid("subjects", draft({ subjectIds: ["subj_1"] }))).toBe(true);
  });

  it("rejects a past exam date and out-of-range study time", () => {
    const day = 86_400_000;
    const toDate = (offsetDays: number) => new Date(Date.now() + offsetDays * day).toISOString().slice(0, 10);

    expect(validateStep("schedule", draft({ targetDate: toDate(-1) })).targetDate).toBeDefined();
    expect(validateStep("schedule", draft({ targetDate: "", dailyMinutes: 30 })).targetDate).toBeDefined();
    // More than two years out is rejected so the plan cannot be nonsense.
    expect(validateStep("schedule", draft({ targetDate: toDate(1000), dailyMinutes: 45 })).targetDate).toBeDefined();
    expect(validateStep("schedule", draft({ targetDate: toDate(200), dailyMinutes: 5 })).dailyMinutes).toBeDefined();
    expect(validateStep("schedule", draft({ targetDate: toDate(200), dailyMinutes: 45 }))).toEqual({});
  });

  it("requires a name, a real email and a mixed password", () => {
    expect(validateStep("account", draft({ displayName: "A", email: "nope", password: "short" }))).toMatchObject({
      displayName: expect.any(String),
      email: expect.any(String),
      password: expect.any(String),
    });
    expect(validateStep("account", draft({ displayName: "Amara O", email: "a@b.co", password: "letters" })).password).toBeDefined();
    expect(
      validateStep("account", draft({ displayName: "Amara O", email: "a@b.co", password: "Password123" })),
    ).toEqual({});
  });
});

describe("register payload", () => {
  it("normalises the email, trims the name and dates, and never invents a password", () => {
    const payload = toRegisterPayload(
      draft({
        displayName: "  Amara Okafor ",
        email: " AMARA@Example.com ",
        password: "Password123",
        examPathwayId: "pw_1",
        targetDate: "2026-06-01",
        subjectIds: ["subj_1", "subj_2"],
        dailyMinutes: 45,
        referralCode: "amara-2026",
      }),
    );

    expect(payload).toMatchObject({
      displayName: "Amara Okafor",
      email: "amara@example.com",
      password: "Password123",
      examPathwayId: "pw_1",
      targetDate: "2026-06-01",
      subjectIds: ["subj_1", "subj_2"],
      dailyMinutes: 45,
      referralCode: "AMARA-2026",
      onboardingStep: 5,
      client: "web",
    });
  });

  it("sends null rather than an empty string for an unset pathway", () => {
    const payload = toRegisterPayload(draft({ examPathwayId: "" }));
    expect(payload.examPathwayId).toBeNull();
  });
});

describe("study load advice", () => {
  it("changes with the commitment", () => {
    expect(studyLoadAdvice(15)).toMatch(/light/i);
    expect(studyLoadAdvice(30)).toMatch(/steady/i);
    expect(studyLoadAdvice(60)).toMatch(/focused/i);
    expect(studyLoadAdvice(120)).toMatch(/intensive/i);
  });
});
