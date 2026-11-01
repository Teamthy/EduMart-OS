/**
 * The signup wizard is defined once here and rendered by both the web and the mobile app.
 *
 * State is deliberately NOT kept in the step definition: each step is a pure description of
 * fields plus validation, and the *answers* are persisted on the server (`PATCH /v1/me` after the
 * account exists, or an in-progress registration draft before it does). That is what makes the
 * flow resumable across devices and able to survive a refresh.
 */

export type SignupStepId = "pathway" | "subjects" | "schedule" | "account" | "confirm";

export interface SignupStep {
  id: SignupStepId;
  /** 1-based position, shown as "Step 2 of 5". */
  index: number;
  title: string;
  description: string;
  /** Short label for the stepper. */
  label: string;
  /** The primary call to action. */
  cta: string;
}

export const SIGNUP_STEPS: readonly SignupStep[] = [
  {
    id: "pathway",
    index: 1,
    title: "Which exam are you preparing for?",
    description: "We use this to build the right question mix and countdown.",
    label: "Exam",
    cta: "Continue",
  },
  {
    id: "subjects",
    index: 2,
    title: "Choose your subjects",
    description: "Pick at least one. You can change these later in Settings.",
    label: "Subjects",
    cta: "Continue",
  },
  {
    id: "schedule",
    index: 3,
    title: "When is your exam, and how much time do you have?",
    description: "Your daily minutes shape the study plan and reminder cadence.",
    label: "Schedule",
    cta: "Continue",
  },
  {
    id: "account",
    index: 4,
    title: "Create your account",
    description: "Your progress is saved as you go, so you can finish on any device.",
    label: "Account",
    cta: "Create account",
  },
  {
    id: "confirm",
    index: 5,
    title: "You're all set",
    description: "We've built your plan. Start with a short practice set to calibrate it.",
    label: "Ready",
    cta: "Go to dashboard",
  },
] as const;

export const TOTAL_SIGNUP_STEPS = SIGNUP_STEPS.length;

/**
 * What kind of account the person is creating.
 *
 * This is intent, not authority. It routes onboarding — an institution gets the organisation setup path, a tutor
 * gets the invitation path — and it never grants a role: organisation admins are approved by a platform
 * administrator, and teachers are invited by an organisation owner.
 */
export type AccountType = "student" | "tutor" | "institution";

export const ACCOUNT_TYPES: { value: AccountType; label: string; description: string }[] = [
  { value: "student", label: "Student", description: "Preparing for an exam. Practise, take mocks, track progress." },
  { value: "tutor", label: "Tutor", description: "Teach learners — solo, or inside a school that invites you." },
  { value: "institution", label: "Institution", description: "Run classes for a school or centre. Verified before it goes live." },
];

export interface SignupDraft {
  accountType: AccountType;
  examPathwayKey: string;
  examPathwayId: string;
  subjectIds: string[];
  targetDate: string;
  dailyMinutes: number;
  displayName: string;
  email: string;
  password: string;
  referralCode: string;
  timezone: string;
}

export function emptyDraft(timezone = "Africa/Lagos"): SignupDraft {
  const target = new Date();
  target.setDate(target.getDate() + 60);
  return {
    accountType: "student",
    examPathwayKey: "",
    examPathwayId: "",
    subjectIds: [],
    targetDate: target.toISOString().slice(0, 10),
    dailyMinutes: 30,
    displayName: "",
    email: "",
    password: "",
    referralCode: "",
    timezone,
  };
}

export type SignupErrors = Partial<Record<keyof SignupDraft, string>>;

const EMAIL_PATTERN = /^[^@\s]+@[^@\s]+\.[^@\s]+$/;

/**
 * Validates one step. Field errors are keyed by draft field so a form can render them inline,
 * and the same messages are produced by the API for server-side validation.
 */
export function validateStep(step: SignupStepId, draft: SignupDraft): SignupErrors {
  const errors: SignupErrors = {};

  if (step === "pathway" && !draft.examPathwayId) {
    errors.examPathwayId = "Choose the exam you're preparing for.";
  }

  if (step === "subjects" && draft.subjectIds.length === 0) {
    errors.subjectIds = "Pick at least one subject.";
  }

  if (step === "schedule") {
    if (!draft.targetDate) {
      errors.targetDate = "Choose your exam date, or leave the suggested one.";
    } else {
      const target = new Date(`${draft.targetDate}T00:00:00Z`);
      const today = new Date();
      const startOfToday = Date.UTC(today.getUTCFullYear(), today.getUTCMonth(), today.getUTCDate());
      if (Number.isNaN(target.getTime())) {
        errors.targetDate = "Enter a valid date.";
      } else if (target.getTime() < startOfToday) {
        errors.targetDate = "Your exam date cannot be in the past.";
      } else if (target.getTime() > startOfToday + 730 * 86_400_000) {
        errors.targetDate = "Choose a date within the next two years.";
      }
    }
    if (draft.dailyMinutes < 10 || draft.dailyMinutes > 240) {
      errors.dailyMinutes = "Choose between 10 and 240 minutes a day.";
    }
  }

  if (step === "account") {
    if (draft.displayName.trim().length < 2) {
      errors.displayName = "Enter your name as you'd like it shown.";
    }
    if (!EMAIL_PATTERN.test(draft.email.trim())) {
      errors.email = "Enter a valid email address.";
    }
    if (!ACCOUNT_TYPES.some((option) => option.value === draft.accountType)) {
      errors.accountType = "Choose the kind of account you're creating.";
    }
    if (draft.password.length < 8) {
      errors.password = "Use at least 8 characters.";
    } else if (!/[a-zA-Z]/.test(draft.password) || !/[0-9]/.test(draft.password)) {
      errors.password = "Include at least one letter and one number.";
    }
  }

  return errors;
}

export function isStepValid(step: SignupStepId, draft: SignupDraft): boolean {
  return Object.keys(validateStep(step, draft)).length === 0;
}

/** Advice shown under the daily-minutes control, derived rather than hard-coded per option. */
export function studyLoadAdvice(dailyMinutes: number): string {
  if (dailyMinutes < 20) return "Light: about one practice set every other day.";
  if (dailyMinutes < 45) return "Steady: a practice set most days, one mock a week.";
  if (dailyMinutes < 90) return "Focused: daily practice plus a timed mock each week.";
  return "Intensive: near-daily mocks — make sure you schedule rest days.";
}

/** The next step id, or null when the wizard is complete. */
export function nextStep(current: SignupStepId): SignupStepId | null {
  const index = SIGNUP_STEPS.findIndex((step) => step.id === current);
  return index >= 0 && index < SIGNUP_STEPS.length - 1 ? SIGNUP_STEPS[index + 1]!.id : null;
}

export function previousStep(current: SignupStepId): SignupStepId | null {
  const index = SIGNUP_STEPS.findIndex((step) => step.id === current);
  return index > 0 ? SIGNUP_STEPS[index - 1]!.id : null;
}

/** Payload for POST /v1/auth/register. */
export function toRegisterPayload(draft: SignupDraft) {
  return {
    accountType: draft.accountType,
    displayName: draft.displayName.trim(),
    email: draft.email.trim().toLowerCase(),
    password: draft.password,
    examPathwayId: draft.examPathwayId || null,
    targetDate: draft.targetDate || null,
    subjectIds: draft.subjectIds,
    dailyMinutes: draft.dailyMinutes,
    referralCode: draft.referralCode.trim().toUpperCase(),
    timezone: draft.timezone,
    client: "web" as const,
    onboardingStep: TOTAL_SIGNUP_STEPS,
  };
}
